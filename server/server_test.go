package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/commands"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/script"
	"github.com/cordon-dev/cordon/server"
)

func newTestHandler(t *testing.T) http.Handler {
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...).With(
			script.PythonCommand(),
			script.NodeCommand(),
		),
		FS: fs.Mem(),
		Tools: []cordon.ToolBinding{
			script.Python(),
			script.JS(),
		},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}
	return server.NewHandler(sb)
}

func TestServer_Healthz(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var data map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&data)
	if data["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", data["status"])
	}
}

func TestServer_ListTools(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest("GET", "/v1/tools", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Tools []cordon.Tool `json:"tools"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode tools: %v", err)
	}

	toolNames := make(map[string]bool)
	for _, tool := range body.Tools {
		toolNames[tool.Name] = true
	}

	if !toolNames["bash"] || !toolNames["python"] || !toolNames["js"] {
		t.Errorf("expected bash, python, and js tools, got: %+v", body.Tools)
	}
}

func TestServer_CallTool_Bash(t *testing.T) {
	h := newTestHandler(t)
	payload, _ := json.Marshal(map[string]any{
		"name": "bash",
		"input": map[string]string{
			"command": "echo 'server-bash-test'",
		},
	})

	req := httptest.NewRequest("POST", "/v1/tools/call", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}

	var res cordon.Result
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if res.IsError || res.ExitCode != 0 {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if !strings.Contains(res.Stdout, "server-bash-test") {
		t.Fatalf("expected 'server-bash-test', got %q", res.Stdout)
	}
}

func TestServer_CallTool_Python(t *testing.T) {
	h := newTestHandler(t)
	payload, _ := json.Marshal(map[string]any{
		"name": "python",
		"input": map[string]string{
			"code": "print(10 + 32)",
		},
	})

	req := httptest.NewRequest("POST", "/v1/tools/call", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}

	var res cordon.Result
	_ = json.NewDecoder(rec.Body).Decode(&res)

	if res.IsError || res.ExitCode != 0 {
		t.Fatalf("unexpected error: %+v", res)
	}
	if !strings.Contains(res.Stdout, "42") {
		t.Fatalf("expected 42, got %q", res.Stdout)
	}
}

func TestServer_ExecBash(t *testing.T) {
	h := newTestHandler(t)
	payload, _ := json.Marshal(map[string]string{
		"command": "echo 'direct-exec' | tr 'a-z' 'A-Z'",
	})

	req := httptest.NewRequest("POST", "/v1/exec", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}

	var res cordon.Result
	_ = json.NewDecoder(rec.Body).Decode(&res)

	if res.IsError || res.ExitCode != 0 {
		t.Fatalf("unexpected error: %+v", res)
	}
	if !strings.Contains(res.Stdout, "DIRECT-EXEC") {
		t.Fatalf("expected 'DIRECT-EXEC', got %q", res.Stdout)
	}
}
