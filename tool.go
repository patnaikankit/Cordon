package cordon

import (
	"encoding/json"
	"strings"
)

// Tool is an SDK-neutral tool definition.
// It carries a name, description, and JSON Schema for input arguments,
// enabling direct usage with any AI SDK (Anthropic, OpenAI, custom) without adapter glue.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Result represents the outcome of executing a tool or command in the sandbox.
type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	IsError  bool   `json:"is_error"`
}

// String renders the result into a clean text representation:
// stdout followed by stderr (if present).
func (r Result) String() string {
	switch {
	case r.Stdout != "" && r.Stderr != "":
		return r.Stdout + "\n" + strings.TrimRight(r.Stderr, "\n")
	case r.Stderr != "":
		return strings.TrimRight(r.Stderr, "\n")
	default:
		return r.Stdout
	}
}
