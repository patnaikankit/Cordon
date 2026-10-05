package python_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/commands"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/internal/status"
	"github.com/cordon-dev/cordon/netpolicy"
	"github.com/cordon-dev/cordon/script/python"
)

func TestPython_BasicExecution(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{
		Tools: []cordon.ToolBinding{python.Tool()},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	code := `
def add(a, b):
    return a + b

print("result:", add(20, 22))
`
	input, _ := json.Marshal(map[string]string{"code": code})
	res, err := sb.CallTool(context.Background(), "python", input)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError || res.ExitCode != 0 {
		t.Fatalf("unexpected error: %s (code %d)", res.Stderr, res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "result: 42") {
		t.Fatalf("expected 'result: 42', got %q", res.Stdout)
	}
}

func TestPython_SharedFilesWithBash(t *testing.T) {
	mem := fs.Mem()
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		FS:       mem,
		Tools:    []cordon.ToolBinding{python.Tool()},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	ctx := context.Background()

	// 1. Bash writes file
	resBash, err := sb.ExecBash(ctx, "echo 'data-from-bash' > /shared.txt")
	if err != nil || resBash.IsError {
		t.Fatalf("bash write failed: %v, stderr: %s", err, resBash.Stderr)
	}

	// 2. Python reads file
	pyReadCode := `
f = open("/shared.txt", "r")
content = f.read().strip()
f.close()
print("read:", content)

# Write output file from python
write_file("/from_python.txt", content + "-processed")
`
	input, _ := json.Marshal(map[string]string{"code": pyReadCode})
	resPy, err := sb.CallTool(ctx, "python", input)
	if err != nil || resPy.IsError {
		t.Fatalf("python execution failed: %v, stderr: %s", err, resPy.Stderr)
	}
	if !strings.Contains(resPy.Stdout, "read: data-from-bash") {
		t.Fatalf("expected 'read: data-from-bash', got %q", resPy.Stdout)
	}

	// 3. Bash reads back file written by python
	resBash2, err := sb.ExecBash(ctx, "cat /from_python.txt")
	if err != nil || resBash2.IsError {
		t.Fatalf("bash cat failed: %v, stderr: %s", err, resBash2.Stderr)
	}
	if !strings.Contains(resBash2.Stdout, "data-from-bash-processed") {
		t.Fatalf("expected 'data-from-bash-processed', got %q", resBash2.Stdout)
	}
}

func TestPython_HostFilesAreInaccessibleByDefault(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{
		Tools: []cordon.ToolBinding{python.Tool()},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	code := `
f = open("/etc/passwd", "r")
`
	input, _ := json.Marshal(map[string]string{"code": code})
	res, err := sb.CallTool(context.Background(), "python", input)
	if err != nil {
		t.Fatalf("CallTool unexpected dispatch error: %v", err)
	}
	if !res.IsError || res.ExitCode == 0 {
		t.Fatalf("expected error when accessing host /etc/passwd, got success: %q", res.Stdout)
	}
}

func TestPython_NetworkPolicyEnforced(t *testing.T) {
	// Deny by default
	sb, err := cordon.New(cordon.Policy{
		Tools: []cordon.ToolBinding{python.Tool()},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	code := `
resp = fetch("http://example.com")
print(resp["status_code"])
`
	input, _ := json.Marshal(map[string]string{"code": code})
	res, err := sb.CallTool(context.Background(), "python", input)
	if err != nil {
		t.Fatalf("CallTool dispatch error: %v", err)
	}
	if !res.IsError || res.ExitCode == 0 {
		t.Fatalf("expected fetch to be denied by default, got success: %s", res.Stdout)
	}

	// Test with allowed network
	pipeServer, pipeClient := net.Pipe()
	defer pipeServer.Close()
	defer pipeClient.Close()

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"sandbox-ok"}`))
		}),
	}
	go func() {
		_ = srv.Serve(&singleConnListener{conn: pipeServer})
	}()

	mockDialer := &pipeDialer{conn: pipeClient}
	mockResolver := &pipeResolver{
		ips: map[string][]net.IP{
			"api.cordon.dev": {net.ParseIP("198.51.100.1")},
		},
	}

	sbNet, err := cordon.New(cordon.Policy{
		Network: netpolicy.New(
			netpolicy.AllowHost("api.cordon.dev", 80),
			netpolicy.WithDialer(mockDialer),
			netpolicy.WithResolver(mockResolver),
		),
		Tools: []cordon.ToolBinding{python.Tool()},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox with network: %v", err)
	}

	allowedCode := `
resp = fetch("http://api.cordon.dev/test")
print("status:", resp["status_code"])
print("body:", resp["text"])
`
	inputNet, _ := json.Marshal(map[string]string{"code": allowedCode})
	resNet, err := sbNet.CallTool(context.Background(), "python", inputNet)
	if err != nil || resNet.IsError {
		t.Fatalf("expected fetch to succeed, got err=%v, stderr=%s", err, resNet.Stderr)
	}
	if !strings.Contains(resNet.Stdout, "status: 200") || !strings.Contains(resNet.Stdout, "sandbox-ok") {
		t.Fatalf("unexpected stdout: %q", resNet.Stdout)
	}
}

func TestPython_TimeoutAndLimits(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{
		Limits: cordon.Limits{
			Timeout:        50 * time.Millisecond,
			MaxOutputBytes: 64,
		},
		Tools: []cordon.ToolBinding{python.Tool()},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	// 1. Timeout on infinite loop
	loopCode := `
def loop():
    while True:
        pass
loop()
`
	inputLoop, _ := json.Marshal(map[string]string{"code": loopCode})
	resLoop, err := sb.CallTool(context.Background(), "python", inputLoop)
	if err != nil {
		t.Fatalf("CallTool dispatch error: %v", err)
	}
	if !resLoop.IsError || resLoop.ExitCode != status.StatusTimeout {
		t.Fatalf("expected timeout exit code %d, got %d (err: %s)", status.StatusTimeout, resLoop.ExitCode, resLoop.Stderr)
	}

	// 2. Output limit cutoff
	outCode := `
for i in range(1000):
    print("flood_of_text_that_exceeds_sixty_four_bytes")
`
	inputOut, _ := json.Marshal(map[string]string{"code": outCode})
	resOut, err := sb.CallTool(context.Background(), "python", inputOut)
	if err != nil {
		t.Fatalf("CallTool dispatch error: %v", err)
	}
	if !resOut.IsError || !strings.Contains(resOut.Stderr, "output byte limit exceeded") {
		t.Fatalf("expected output byte limit exceeded in stderr, got err=%v, stdout=%q, stderr=%q", resOut.IsError, resOut.Stdout, resOut.Stderr)
	}
}

func TestPython_CannotEscapeThroughImports(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{
		Tools: []cordon.ToolBinding{python.Tool()},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	escapeAttempts := []string{
		`import os`,
		`import subprocess`,
		`import socket`,
		`import sys`,
		`load("os", "system")`,
	}

	for _, code := range escapeAttempts {
		input, _ := json.Marshal(map[string]string{"code": code})
		res, err := sb.CallTool(context.Background(), "python", input)
		if err != nil {
			t.Fatalf("CallTool error: %v", err)
		}
		if !res.IsError || res.ExitCode == 0 {
			t.Errorf("expected code %q to fail, got success: %s", code, res.Stdout)
		}
	}
}

func TestPython_CommandInBash(t *testing.T) {
	mem := fs.Mem()
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...).With(python.Command()),
		FS:       mem,
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	// Run python -c
	res, err := sb.ExecBash(context.Background(), `python -c "print('hello from python-in-bash')"` )
	if err != nil || res.IsError {
		t.Fatalf("ExecBash failed: %v, stderr: %s", err, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "hello from python-in-bash") {
		t.Fatalf("expected 'hello from python-in-bash', got %q", res.Stdout)
	}
}

// Helper types for mock network
type singleConnListener struct {
	conn net.Conn
	once bool
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	if l.once {
		return nil, net.ErrClosed
	}
	l.once = true
	return l.conn, nil
}

func (l *singleConnListener) Close() error   { return nil }
func (l *singleConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

type pipeDialer struct {
	conn net.Conn
}

func (d *pipeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.conn, nil
}

type pipeResolver struct {
	ips map[string][]net.IP
}

func (r *pipeResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	if ips, ok := r.ips[host]; ok {
		return ips, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host}
}

