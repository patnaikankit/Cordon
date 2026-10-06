package adapters_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/commands"
	"github.com/cordon-dev/cordon/examples/adapters"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/script"
)

func TestAdapters_OpenAI(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		FS:       fs.Mem().Seed(map[string]string{"/hello.txt": "hello openai adapter"}),
		Tools: []cordon.ToolBinding{
			script.Python(),
		},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	// 1. Convert tools to OpenAI format
	openAITools := adapters.ToOpenAITools(sb.Tools())
	if len(openAITools) != 2 { // bash and python
		t.Fatalf("expected 2 tools, got %d", len(openAITools))
	}
	if openAITools[0].Type != "function" || openAITools[0].Function.Name != "bash" {
		t.Errorf("unexpected first tool: %+v", openAITools[0])
	}

	// 2. Simulate model calling tool
	call := adapters.OpenAIToolCall{
		ID:   "call_abc123",
		Type: "function",
	}
	call.Function.Name = "bash"
	call.Function.Arguments = `{"command": "cat /hello.txt"}`

	msg, err := adapters.HandleOpenAIToolCall(context.Background(), sb, call)
	if err != nil {
		t.Fatalf("HandleOpenAIToolCall failed: %v", err)
	}

	if msg.Role != "tool" || msg.ToolCallID != "call_abc123" {
		t.Errorf("unexpected message metadata: %+v", msg)
	}
	if !strings.Contains(msg.Content, "hello openai adapter") {
		t.Errorf("expected 'hello openai adapter', got %q", msg.Content)
	}
}

func TestAdapters_Anthropic(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		FS:       fs.Mem().Seed(map[string]string{"/hello.txt": "hello claude adapter"}),
		Tools: []cordon.ToolBinding{
			script.JS(),
		},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	// 1. Convert tools to Anthropic format
	claudeTools := adapters.ToAnthropicTools(sb.Tools())
	if len(claudeTools) != 2 { // bash and js
		t.Fatalf("expected 2 tools, got %d", len(claudeTools))
	}

	// 2. Simulate Claude tool_use block
	block := adapters.AnthropicToolUseBlock{
		Type:  "tool_use",
		ID:    "toolu_01XYZ",
		Name:  "bash",
		Input: json.RawMessage(`{"command": "cat /hello.txt"}`),
	}

	resultBlock, err := adapters.HandleAnthropicToolUse(context.Background(), sb, block)
	if err != nil {
		t.Fatalf("HandleAnthropicToolUse failed: %v", err)
	}

	if resultBlock.Type != "tool_result" || resultBlock.ToolUseID != "toolu_01XYZ" {
		t.Errorf("unexpected block metadata: %+v", resultBlock)
	}
	if resultBlock.IsError {
		t.Errorf("unexpected error flag in result block")
	}
	if !strings.Contains(resultBlock.Content, "hello claude adapter") {
		t.Errorf("expected 'hello claude adapter', got %q", resultBlock.Content)
	}
}
