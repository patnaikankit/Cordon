from dataclasses import dataclass, field
from typing import Any, Dict


@dataclass
class Tool:
    name: str
    description: str
    input_schema: Dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "Tool":
        return cls(
            name=data.get("name", ""),
            description=data.get("description", ""),
            input_schema=data.get("input_schema", {}),
        )


@dataclass
class Result:
    stdout: str
    stderr: str
    exit_code: int
    is_error: bool

    @property
    def output(self) -> str:
        if self.stdout and self.stderr:
            return f"{self.stdout}\n{self.stderr.rstrip()}"
        return self.stdout or self.stderr

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "Result":
        return cls(
            stdout=data.get("stdout", ""),
            stderr=data.get("stderr", ""),
            exit_code=data.get("exit_code", 0),
            is_error=data.get("is_error", False),
        )
