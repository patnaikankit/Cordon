package script_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/commands"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/script"
)

func TestScriptInterpreters_UnifiedCapabilityPlane(t *testing.T) {
	mem := fs.Mem()
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...).With(script.PythonCommand(), script.NodeCommand()),
		FS:       mem,
		Tools: []cordon.ToolBinding{
			script.Python(),
			script.JS(),
		},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	// 1. Verify Tools() advertises bash, python, and js
	tools := sb.Tools()
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}
	if !toolNames["bash"] || !toolNames["python"] || !toolNames["js"] {
		t.Fatalf("expected tools to include bash, python, and js, got: %+v", tools)
	}

	ctx := context.Background()

	// 2. Python tool creates a dataset
	pyCode := `
data = {"users": [{"id": 1, "name": "alice"}, {"id": 2, "name": "bob"}]}
# format as comma-separated
lines = [u["name"] for u in data["users"]]
write_file("/pipeline/users.txt", "\n".join(lines) + "\n")
`
	pyInput, _ := json.Marshal(map[string]string{"code": pyCode})
	resPy, err := sb.CallTool(ctx, "python", pyInput)
	if err != nil || resPy.IsError {
		t.Fatalf("python tool execution failed: %v, stderr: %s", err, resPy.Stderr)
	}

	// 3. JS tool reads the file, transforms to uppercase, writes back
	jsCode := `
const content = fs.readFileSync("/pipeline/users.txt").trim();
const upper = content.split("\n").map(s => s.toUpperCase()).join("\n");
fs.writeFileSync("/pipeline/users_upper.txt", upper + "\n");
`
	jsInput, _ := json.Marshal(map[string]string{"code": jsCode})
	resJS, err := sb.CallTool(ctx, "js", jsInput)
	if err != nil || resJS.IsError {
		t.Fatalf("js tool execution failed: %v, stderr: %s", err, resJS.Stderr)
	}

	// 4. Bash tool reads and filters the result
	resBash, err := sb.ExecBash(ctx, "grep ALICE /pipeline/users_upper.txt")
	if err != nil || resBash.IsError {
		t.Fatalf("bash grep failed: %v, stderr: %s", err, resBash.Stderr)
	}
	if !strings.Contains(resBash.Stdout, "ALICE") {
		t.Fatalf("expected 'ALICE', got %q", resBash.Stdout)
	}

	// 5. Test Register helper on fresh sandbox
	freshSB, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
	})
	if err != nil {
		t.Fatalf("failed to create fresh sandbox: %v", err)
	}
	if len(freshSB.Tools()) != 1 { // only bash
		t.Fatalf("expected 1 tool before registration, got %d", len(freshSB.Tools()))
	}
	script.Register(freshSB)
	if len(freshSB.Tools()) != 3 { // bash, python, js
		t.Fatalf("expected 3 tools after registration, got %d", len(freshSB.Tools()))
	}
}
