import json
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Dict, List, Optional, Union

from .types import Result, Tool


class CordonError(Exception):
    """Base exception for Cordon client errors."""
    pass


class CordonClient:
    """Python client for Cordon's local RPC / HTTP daemon."""

    def __init__(self, base_url: str = "http://127.0.0.1:8080", timeout: float = 30.0):
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout

    def __enter__(self) -> "CordonClient":
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        pass

    def health(self) -> Dict[str, Any]:
        """Check server health."""
        return self._get("/healthz")

    def list_tools(self) -> List[Tool]:
        """Discover available tools advertised by the Cordon sandbox."""
        data = self._get("/v1/tools")
        raw_tools = data.get("tools", [])
        return [Tool.from_dict(t) for t in raw_tools]

    def call_tool(
        self,
        name: str,
        input_data: Optional[Union[Dict[str, Any], str]] = None,
        **kwargs: Any,
    ) -> Result:
        """
        Dispatch a tool invocation by name with JSON arguments.
        """
        payload_input = input_data
        if payload_input is None and kwargs:
            payload_input = kwargs
        elif payload_input is None:
            payload_input = {}

        body = {
            "name": name,
            "input": payload_input,
        }
        res_dict = self._post("/v1/tools/call", body)
        return Result.from_dict(res_dict)

    def exec_bash(self, command: str) -> Result:
        """
        Execute a bash command in the Cordon sandbox.
        """
        body = {"command": command}
        res_dict = self._post("/v1/exec", body)
        return Result.from_dict(res_dict)

    def _get(self, endpoint: str) -> Dict[str, Any]:
        url = f"{self.base_url}{endpoint}"
        req = urllib.request.Request(url, headers={"Accept": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                data = resp.read().decode("utf-8")
                return json.loads(data)
        except urllib.error.HTTPError as e:
            err_body = e.read().decode("utf-8")
            raise CordonError(f"HTTP {e.code}: {err_body}") from e
        except urllib.error.URLError as e:
            raise CordonError(f"Connection failed: {e.reason}") from e

    def _post(self, endpoint: str, body: Dict[str, Any]) -> Dict[str, Any]:
        url = f"{self.base_url}{endpoint}"
        payload_bytes = json.dumps(body).encode("utf-8")
        req = urllib.request.Request(
            url,
            data=payload_bytes,
            headers={
                "Content-Type": "application/json",
                "Accept": "application/json",
            },
        )
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                data = resp.read().decode("utf-8")
                return json.loads(data)
        except urllib.error.HTTPError as e:
            err_body = e.read().decode("utf-8")
            raise CordonError(f"HTTP {e.code}: {err_body}") from e
        except urllib.error.URLError as e:
            raise CordonError(f"Connection failed: {e.reason}") from e
