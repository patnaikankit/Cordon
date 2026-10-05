package cordon_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/command"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/internal/status"
)

func TestZeroPolicyDeniesExecution(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{})
	if err != nil {
		t.Fatalf("New(zero Policy) unexpected error: %v", err)
	}

	ctx := context.Background()

	// Direct ExecBash with zero policy
	res, err := sb.ExecBash(ctx, "echo hi")
	if err != nil {
		t.Fatalf("ExecBash returned unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected res.IsError == true under zero policy, got false")
	}
	if res.ExitCode != status.StatusPolicyDenied {
		t.Errorf("expected exit code %d, got %d", status.StatusPolicyDenied, res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "policy denied execution") {
		t.Errorf("expected Stderr to report policy denial, got %q", res.Stderr)
	}

	// CallTool with bash under zero policy
	toolInput := json.RawMessage(`{"command":"echo hi"}`)
	toolRes, err := sb.CallTool(ctx, "bash", toolInput)
	if err != nil {
		t.Fatalf("CallTool returned unexpected Go error: %v", err)
	}
	if !toolRes.IsError {
		t.Errorf("expected toolRes.IsError == true, got false")
	}
	if toolRes.ExitCode != status.StatusPolicyDenied {
		t.Errorf("expected exit code %d, got %d", status.StatusPolicyDenied, toolRes.ExitCode)
	}
}

func TestMalformedToolInput(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := context.Background()
	_, err = sb.CallTool(ctx, "bash", json.RawMessage(`{invalid json`))
	if err == nil {
		t.Fatalf("expected dispatch error for malformed input, got nil")
	}
	if !errors.Is(err, status.ErrMalformedInput) {
		t.Errorf("expected ErrMalformedInput in error chain, got %v", err)
	}
}

func TestUnknownToolName(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := context.Background()
	_, err = sb.CallTool(ctx, "unknown_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("expected dispatch error for unknown tool, got nil")
	}
	if !errors.Is(err, status.ErrUnknownTool) {
		t.Errorf("expected ErrUnknownTool in error chain, got %v", err)
	}
}

func TestCallerCancellationPropagates(t *testing.T) {
	// A fake command that blocks until context is done
	blockingCmd := command.New("block", func(ctx context.Context, ec *command.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(blockingCmd),
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	res, err := sb.ExecBash(ctx, "block")
	if err != nil {
		t.Fatalf("ExecBash unexpected dispatch error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError == true on canceled call, got false")
	}
	if res.ExitCode != status.StatusCanceled {
		t.Errorf("expected exit code %d (StatusCanceled), got %d", status.StatusCanceled, res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "canceled") {
		t.Errorf("expected Stderr to mention canceled, got %q", res.Stderr)
	}
}

func TestTimeoutEnforced(t *testing.T) {
	slowCmd := command.New("slow", func(ctx context.Context, ec *command.Context) error {
		select {
		case <-time.After(1 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(slowCmd),
		Limits: cordon.Limits{
			Timeout: 40 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	res, err := sb.ExecBash(context.Background(), "slow")
	if err != nil {
		t.Fatalf("ExecBash unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError == true on timeout, got false")
	}
	if res.ExitCode != status.StatusTimeout {
		t.Errorf("expected exit code %d (StatusTimeout), got %d", status.StatusTimeout, res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "timed out") {
		t.Errorf("expected Stderr to mention timed out, got %q", res.Stderr)
	}
}

func TestBudgetIsolationBetweenCalls(t *testing.T) {
	// Fake command that writes given amount of bytes
	writeCmd := command.New("emit", func(ctx context.Context, ec *command.Context) error {
		n := 15
		if len(ec.Args) > 1 && ec.Args[1] == "large" {
			n = 30
		}
		data := strings.Repeat("A", n)
		_, err := io.WriteString(ec.Stdout, data)
		return err
	})

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(writeCmd),
		Limits: cordon.Limits{
			MaxOutputBytes:  20,
			MaxCommandCount: 1,
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := context.Background()

	// Call 1: uses 15 of 20 bytes output budget, and 1 of 1 command budget
	res1, err := sb.ExecBash(ctx, "emit")
	if err != nil {
		t.Fatalf("call 1 failed: %v", err)
	}
	if res1.IsError {
		t.Errorf("call 1 should succeed, got IsError=true, stderr=%q", res1.Stderr)
	}
	if len(res1.Stdout) != 15 {
		t.Errorf("call 1 expected 15 bytes stdout, got %d", len(res1.Stdout))
	}

	// Call 2: should receive a fresh output and command budget
	// If budgets were leaked/reused, this would fail the 20-byte limit (15+15=30)
	// and the 1-command limit (1+1=2)
	res2, err := sb.ExecBash(ctx, "emit")
	if err != nil {
		t.Fatalf("call 2 failed: %v", err)
	}
	if res2.IsError {
		t.Errorf("call 2 should succeed with independent budget, got IsError=true, stderr=%q", res2.Stderr)
	}
	if len(res2.Stdout) != 15 {
		t.Errorf("call 2 expected 15 bytes stdout, got %d", len(res2.Stdout))
	}

	// Call 3: exceeds output budget within a single call (requests 30 bytes against 20 byte max)
	res3, err := sb.ExecBash(ctx, "emit large")
	if err != nil {
		t.Fatalf("call 3 failed: %v", err)
	}
	if !res3.IsError {
		t.Errorf("call 3 expected IsError=true due to output cap, got false")
	}
	if len(res3.Stdout) != 20 {
		t.Errorf("call 3 expected exactly 20 bytes capped stdout, got %d", len(res3.Stdout))
	}
}

func TestFakeCommandExecution(t *testing.T) {
	echoCmd := command.New("echo", func(ctx context.Context, ec *command.Context) error {
		args := ec.Args[1:]
		fmt.Fprintln(ec.Stdout, strings.Join(args, " "))
		return nil
	})

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(echoCmd),
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	res, err := sb.ExecBash(context.Background(), "echo hello cordon sandbox")
	if err != nil {
		t.Fatalf("ExecBash unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("ExecBash unexpected error result: %v", res.Stderr)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if strings.TrimSpace(res.Stdout) != "hello cordon sandbox" {
		t.Errorf("unexpected stdout: %q", res.Stdout)
	}
	if res.String() != "hello cordon sandbox\n" {
		t.Errorf("unexpected Result.String(): %q", res.String())
	}
}

func TestPanicRecovery(t *testing.T) {
	panicCmd := command.New("panic", func(ctx context.Context, ec *command.Context) error {
		panic("deliberate command panic")
	})

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(panicCmd),
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	res, err := sb.ExecBash(context.Background(), "panic")
	if err != nil {
		t.Fatalf("ExecBash returned unexpected dispatch error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true on recovered panic")
	}
	if !strings.Contains(res.Stderr, "deliberate command panic") {
		t.Errorf("expected Stderr to contain panic message, got %q", res.Stderr)
	}
}

func TestCapabilitiesBundle(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{
		Limits: cordon.Limits{
			Timeout: 100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	caps := sb.Resources()
	if caps.FS() == nil {
		t.Errorf("expected non-nil FS from Resources()")
	}

	childCtx, cancel := caps.Context(context.Background())
	defer cancel()

	deadline, ok := childCtx.Deadline()
	if !ok {
		t.Errorf("expected deadline on derived context from Capabilities")
	}
	if time.Until(deadline) <= 0 || time.Until(deadline) > 150*time.Millisecond {
		t.Errorf("unexpected deadline duration: %v", time.Until(deadline))
	}
}

func TestSandbox_FSPersistenceAcrossCalls(t *testing.T) {
	writeCmd := command.New("write", func(ctx context.Context, ec *command.Context) error {
		if len(ec.Args) < 3 {
			return ec.Fail(1, "usage: write <path> <content>\n")
		}
		p := ec.Args[1]
		content := ec.Args[2]
		dir := "/"
		if lastSlash := strings.LastIndex(p, "/"); lastSlash > 0 {
			dir = p[:lastSlash]
		}
		_ = ec.FS.MkdirAll(dir, 0o755)
		return ec.FS.WriteFile(p, []byte(content), 0o644)
	})

	readCmd := command.New("readfile", func(ctx context.Context, ec *command.Context) error {
		if len(ec.Args) < 2 {
			return ec.Fail(1, "usage: readfile <path>\n")
		}
		p := ec.Args[1]
		data, err := ec.FS.ReadFile(p)
		if err != nil {
			return ec.Fail(1, "read: %v\n", err)
		}
		_, err = ec.Stdout.Write(data)
		return err
	})

	mem := fs.Mem()
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(writeCmd, readCmd),
		FS:       mem,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := context.Background()

	// Call 1: write a file
	res1, err := sb.ExecBash(ctx, "write /data/session.log session_active")
	if err != nil || res1.IsError {
		t.Fatalf("call 1 write failed: %v, stderr=%q", err, res1.Stderr)
	}

	// Call 2: read the file back
	res2, err := sb.ExecBash(ctx, "readfile /data/session.log")
	if err != nil || res2.IsError {
		t.Fatalf("call 2 read failed: %v, stderr=%q", err, res2.Stderr)
	}
	if res2.Stdout != "session_active" {
		t.Errorf("got %q, want 'session_active'", res2.Stdout)
	}
}

func TestSandbox_SharedFSWithCustomTools(t *testing.T) {
	catCmd := command.New("cat", func(ctx context.Context, ec *command.Context) error {
		data, err := ec.FS.ReadFile(ec.Args[1])
		if err != nil {
			return ec.Fail(1, "cat error: %v\n", err)
		}
		_, err = ec.Stdout.Write(data)
		return err
	})

	mem := fs.Mem()
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(catCmd),
		FS:       mem,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Custom tool writes via Resources().FS()
	caps := sb.Resources()
	err = caps.FS().WriteFile("/shared_by_tool.txt", []byte("hello from custom tool"), 0o644)
	if err != nil {
		t.Fatalf("custom tool WriteFile failed: %v", err)
	}

	// ExecBash reads the file created by the custom tool
	res, err := sb.ExecBash(context.Background(), "cat /shared_by_tool.txt")
	if err != nil || res.IsError {
		t.Fatalf("ExecBash cat failed: %v, stderr=%q", err, res.Stderr)
	}
	if res.Stdout != "hello from custom tool" {
		t.Errorf("got %q, want 'hello from custom tool'", res.Stdout)
	}
}

