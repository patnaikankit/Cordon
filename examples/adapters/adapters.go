package adapters

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cordon-dev/cordon"
)

// --- OpenAI Adapters ---

// OpenAIFunction represents an OpenAI tool function descriptor.
type OpenAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// OpenAITool represents an OpenAI tool declaration.
type OpenAITool struct {
	Type     string         `json:"type"`
	Function OpenAIFunction `json:"function"`
}

// OpenAIToolCall represents a tool invocation requested by an OpenAI model.
type OpenAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// OpenAIToolMessage represents the tool result message sent back to OpenAI.
type OpenAIToolMessage struct {
	Role       string `json:"role"`
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}

// ToOpenAITools converts Cordon tools into OpenAI tool schemas.
func ToOpenAITools(tools []cordon.Tool) []OpenAITool {
	var out []OpenAITool
	for _, t := range tools {
		out = append(out, OpenAITool{
			Type: "function",
			Function: OpenAIFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return out
}

// HandleOpenAIToolCall executes an OpenAI tool call against the Cordon Sandbox.
func HandleOpenAIToolCall(ctx context.Context, sb *cordon.Sandbox, call OpenAIToolCall) (OpenAIToolMessage, error) {
	res, err := sb.CallTool(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
	if err != nil {
		return OpenAIToolMessage{}, fmt.Errorf("dispatch error: %w", err)
	}

	content := res.Stdout
	if res.IsError && content == "" {
		content = res.Stderr
	}

	return OpenAIToolMessage{
		Role:       "tool",
		ToolCallID: call.ID,
		Content:    content,
	}, nil
}

// --- Anthropic Adapters ---

// AnthropicTool represents an Anthropic Claude tool schema.
type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// AnthropicToolUseBlock represents a tool_use block received from Claude.
type AnthropicToolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// AnthropicToolResultBlock represents a tool_result content block returned to Claude.
type AnthropicToolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ToAnthropicTools converts Cordon tools into Anthropic tool schemas.
func ToAnthropicTools(tools []cordon.Tool) []AnthropicTool {
	var out []AnthropicTool
	for _, t := range tools {
		out = append(out, AnthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return out
}

// HandleAnthropicToolUse executes an Anthropic tool_use block against the Cordon Sandbox.
func HandleAnthropicToolUse(ctx context.Context, sb *cordon.Sandbox, block AnthropicToolUseBlock) (AnthropicToolResultBlock, error) {
	res, err := sb.CallTool(ctx, block.Name, block.Input)
	if err != nil {
		return AnthropicToolResultBlock{}, fmt.Errorf("dispatch error: %w", err)
	}

	content := res.Stdout
	if res.IsError && content == "" {
		content = res.Stderr
	}

	return AnthropicToolResultBlock{
		Type:      "tool_result",
		ToolUseID: block.ID,
		Content:   content,
		IsError:   res.IsError,
	}, nil
}
