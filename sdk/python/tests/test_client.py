import json
import unittest
from unittest.mock import MagicMock, patch

from cordon import CordonClient, CordonError, Result, Tool


class TestCordonTypes(unittest.TestCase):
    def test_tool_deserialization(self):
        data = {
            "name": "bash",
            "description": "Run shell command",
            "input_schema": {"type": "object", "properties": {"command": {"type": "string"}}},
        }
        t = Tool.from_dict(data)
        self.assertEqual(t.name, "bash")
        self.assertEqual(t.description, "Run shell command")
        self.assertEqual(t.input_schema["type"], "object")

    def test_result_properties(self):
        r1 = Result(stdout="hello", stderr="", exit_code=0, is_error=False)
        self.assertEqual(r1.output, "hello")

        r2 = Result(stdout="", stderr="failed", exit_code=1, is_error=True)
        self.assertEqual(r2.output, "failed")

        r3 = Result(stdout="out", stderr="err", exit_code=1, is_error=True)
        self.assertEqual(r3.output, "out\nerr")


class TestCordonClient(unittest.TestCase):
    def setUp(self):
        self.client = CordonClient("http://127.0.0.1:8080")

    @patch("urllib.request.urlopen")
    def test_health(self, mock_urlopen):
        mock_resp = MagicMock()
        mock_resp.read.return_value = json.dumps({"status": "ok"}).encode("utf-8")
        mock_resp.__enter__.return_value = mock_resp
        mock_urlopen.return_value = mock_resp

        res = self.client.health()
        self.assertEqual(res, {"status": "ok"})

    @patch("urllib.request.urlopen")
    def test_list_tools(self, mock_urlopen):
        mock_resp = MagicMock()
        mock_resp.read.return_value = json.dumps({
            "tools": [
                {"name": "bash", "description": "Execute bash", "input_schema": {}},
                {"name": "python", "description": "Execute python", "input_schema": {}},
            ]
        }).encode("utf-8")
        mock_resp.__enter__.return_value = mock_resp
        mock_urlopen.return_value = mock_resp

        tools = self.client.list_tools()
        self.assertEqual(len(tools), 2)
        self.assertEqual(tools[0].name, "bash")
        self.assertEqual(tools[1].name, "python")

    @patch("urllib.request.urlopen")
    def test_call_tool(self, mock_urlopen):
        mock_resp = MagicMock()
        mock_resp.read.return_value = json.dumps({
            "stdout": "result: 42\n",
            "stderr": "",
            "exit_code": 0,
            "is_error": False,
        }).encode("utf-8")
        mock_resp.__enter__.return_value = mock_resp
        mock_urlopen.return_value = mock_resp

        res = self.client.call_tool("python", {"code": "print('result: 42')"})
        self.assertFalse(res.is_error)
        self.assertEqual(res.exit_code, 0)
        self.assertEqual(res.stdout, "result: 42\n")

    @patch("urllib.request.urlopen")
    def test_exec_bash(self, mock_urlopen):
        mock_resp = MagicMock()
        mock_resp.read.return_value = json.dumps({
            "stdout": "hello\n",
            "stderr": "",
            "exit_code": 0,
            "is_error": False,
        }).encode("utf-8")
        mock_resp.__enter__.return_value = mock_resp
        mock_urlopen.return_value = mock_resp

        res = self.client.exec_bash("echo 'hello'")
        self.assertEqual(res.stdout, "hello\n")
        self.assertEqual(res.exit_code, 0)


if __name__ == "__main__":
    unittest.main()
