package js_test

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
	"github.com/cordon-dev/cordon/script/js"
)

func TestJS_BasicExecution(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{
		Tools: []cordon.ToolBinding{js.Tool()},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	code := `
function add(a, b) {
    return a + b;
}
console.log("result:", add(20, 22));
`
	input, _ := json.Marshal(map[string]string{"code": code})
	res, err := sb.CallTool(context.Background(), "js", input)
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

func TestJS_SharedFilesWithBash(t *testing.T) {
	mem := fs.Mem()
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		FS:       mem,
		Tools:    []cordon.ToolBinding{js.Tool()},
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

	// 2. JS reads file and writes output
	jsCode := `
const content = fs.readFileSync("/shared.txt").trim();
console.log("read:", content);
fs.writeFileSync("/from_js.txt", content + "-js-processed");
`
	input, _ := json.Marshal(map[string]string{"code": jsCode})
	resJS, err := sb.CallTool(ctx, "js", input)
	if err != nil || resJS.IsError {
		t.Fatalf("js execution failed: %v, stderr: %s", err, resJS.Stderr)
	}
	if !strings.Contains(resJS.Stdout, "read: data-from-bash") {
		t.Fatalf("expected 'read: data-from-bash', got %q", resJS.Stdout)
	}

	// 3. Bash reads back file written by JS
	resBash2, err := sb.ExecBash(ctx, "cat /from_js.txt")
	if err != nil || resBash2.IsError {
		t.Fatalf("bash cat failed: %v, stderr: %s", err, resBash2.Stderr)
	}
	if !strings.Contains(resBash2.Stdout, "data-from-bash-js-processed") {
		t.Fatalf("expected 'data-from-bash-js-processed', got %q", resBash2.Stdout)
	}
}

func TestJS_HostFilesAreInaccessibleByDefault(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{
		Tools: []cordon.ToolBinding{js.Tool()},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	code := `
fs.readFileSync("/etc/passwd");
`
	input, _ := json.Marshal(map[string]string{"code": code})
	res, err := sb.CallTool(context.Background(), "js", input)
	if err != nil {
		t.Fatalf("CallTool unexpected dispatch error: %v", err)
	}
	if !res.IsError || res.ExitCode == 0 {
		t.Fatalf("expected error when accessing host /etc/passwd, got success: %q", res.Stdout)
	}
}

func TestJS_NetworkPolicyEnforced(t *testing.T) {
	// Deny by default
	sb, err := cordon.New(cordon.Policy{
		Tools: []cordon.ToolBinding{js.Tool()},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	code := `
const res = fetch("http://example.com");
console.log(res.status);
`
	input, _ := json.Marshal(map[string]string{"code": code})
	res, err := sb.CallTool(context.Background(), "js", input)
	if err != nil {
		t.Fatalf("CallTool dispatch error: %v", err)
	}
	if !res.IsError || res.ExitCode == 0 {
		t.Fatalf("expected fetch to be denied by default, got success: %s", res.Stdout)
	}

	// Allowed network
	pipeServer, pipeClient := net.Pipe()
	defer pipeServer.Close()
	defer pipeClient.Close()

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"js-ok"}`))
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
		Tools: []cordon.ToolBinding{js.Tool()},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox with network: %v", err)
	}

	allowedCode := `
const res = fetch("http://api.cordon.dev/test");
console.log("status:", res.status);
console.log("body:", res.text());
`
	inputNet, _ := json.Marshal(map[string]string{"code": allowedCode})
	resNet, err := sbNet.CallTool(context.Background(), "js", inputNet)
	if err != nil || resNet.IsError {
		t.Fatalf("expected fetch to succeed, got err=%v, stderr=%s", err, resNet.Stderr)
	}
	if !strings.Contains(resNet.Stdout, "status: 200") || !strings.Contains(resNet.Stdout, "js-ok") {
		t.Fatalf("unexpected stdout: %q", resNet.Stdout)
	}
}

func TestJS_TimeoutAndLimits(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{
		Limits: cordon.Limits{
			Timeout:        50 * time.Millisecond,
			MaxOutputBytes: 64,
		},
		Tools: []cordon.ToolBinding{js.Tool()},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	// 1. Timeout on infinite loop
	loopCode := `
while (true) {}
`
	inputLoop, _ := json.Marshal(map[string]string{"code": loopCode})
	resLoop, err := sb.CallTool(context.Background(), "js", inputLoop)
	if err != nil {
		t.Fatalf("CallTool dispatch error: %v", err)
	}
	if !resLoop.IsError || resLoop.ExitCode != status.StatusTimeout {
		t.Fatalf("expected timeout exit code %d, got %d (err: %s)", status.StatusTimeout, resLoop.ExitCode, resLoop.Stderr)
	}

	// 2. Output limit cutoff
	outCode := `
for (let i = 0; i < 1000; i++) {
    console.log("flood_of_text_that_exceeds_sixty_four_bytes");
}
`
	inputOut, _ := json.Marshal(map[string]string{"code": outCode})
	resOut, err := sb.CallTool(context.Background(), "js", inputOut)
	if err != nil {
		t.Fatalf("CallTool dispatch error: %v", err)
	}
	if !resOut.IsError || !strings.Contains(resOut.Stderr, "output byte limit exceeded") {
		t.Fatalf("expected output byte limit exceeded in stderr, got err=%v, stdout=%q, stderr=%q", resOut.IsError, resOut.Stdout, resOut.Stderr)
	}
}

func TestJS_CannotEscapeThroughImportsOrSubprocesses(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{
		Tools: []cordon.ToolBinding{js.Tool()},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	escapeAttempts := []string{
		`require("child_process")`,
		`require("os")`,
		`require("net")`,
		`process.binding("spawn_sync")`,
		`eval('require("child_process")')`,
	}

	for _, code := range escapeAttempts {
		input, _ := json.Marshal(map[string]string{"code": code})
		res, err := sb.CallTool(context.Background(), "js", input)
		if err != nil {
			t.Fatalf("CallTool error: %v", err)
		}
		if !res.IsError || res.ExitCode == 0 {
			t.Errorf("expected code %q to fail, got success: %s", code, res.Stdout)
		}
	}
}

func TestJS_CommandInBash(t *testing.T) {
	mem := fs.Mem()
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...).With(js.Command(), js.JSCommand()),
		FS:       mem,
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	// 1. Run node -e
	resNode, err := sb.ExecBash(context.Background(), `node -e "console.log('hello from node-in-bash')"` )
	if err != nil || resNode.IsError {
		t.Fatalf("ExecBash node failed: %v, stderr: %s", err, resNode.Stderr)
	}
	if !strings.Contains(resNode.Stdout, "hello from node-in-bash") {
		t.Fatalf("expected 'hello from node-in-bash', got %q", resNode.Stdout)
	}

	// 2. Run js -e
	resJS, err := sb.ExecBash(context.Background(), `js -e "console.log('hello from js-in-bash')"` )
	if err != nil || resJS.IsError {
		t.Fatalf("ExecBash js failed: %v, stderr: %s", err, resJS.Stderr)
	}
	if !strings.Contains(resJS.Stdout, "hello from js-in-bash") {
		t.Fatalf("expected 'hello from js-in-bash', got %q", resJS.Stdout)
	}
}

// Helpers
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
