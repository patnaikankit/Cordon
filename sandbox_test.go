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
