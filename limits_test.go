package cordon_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/command"
	"github.com/cordon-dev/cordon/commands"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/internal/status"
)

// TestLimits_InfiniteLoopCutoff verifies that a run-away shell loop is stopped
// cooperatively when the wall-clock timeout expires.
func TestLimits_InfiniteLoopCutoff(t *testing.T) {
	echoCmd := command.New("echo", func(ctx context.Context, ec *command.Context) error {
		fmt.Fprintln(ec.Stdout, strings.Join(ec.Args[1:], " "))
		return nil
	})

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(echoCmd),
		Limits: cordon.Limits{
			Timeout: 60 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	start := time.Now()
	res, err := sb.ExecBash(context.Background(), `while true; do :; done`)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true on infinite loop timeout")
	}
	if res.ExitCode != status.StatusTimeout {
		t.Errorf("expected exit code %d (StatusTimeout), got %d", status.StatusTimeout, res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "timed out") {
		t.Errorf("expected Stderr to mention 'timed out', got %q", res.Stderr)
	}
	if elapsed > 1*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

// TestLimits_OutputBudgetCutoff verifies that unbounded output generation
// is truncated strictly to MaxOutputBytes and sets IsError=true.
func TestLimits_OutputBudgetCutoff(t *testing.T) {
	floodCmd := command.New("flood", func(ctx context.Context, ec *command.Context) error {
		chunk := strings.Repeat("A", 1024)
		for {
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			if _, err := io.WriteString(ec.Stdout, chunk); err != nil {
				return err
			}
		}
	})

	const maxBytes = 256
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(floodCmd),
		Limits: cordon.Limits{
			MaxOutputBytes: maxBytes,
			Timeout:        2 * time.Second,
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	res, err := sb.ExecBash(context.Background(), "flood")
	if err != nil {
		t.Fatalf("ExecBash unexpected dispatch error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true when output budget exceeded")
	}
	if res.ExitCode != status.StatusLimitExceeded {
		t.Errorf("expected exit code %d (StatusLimitExceeded), got %d", status.StatusLimitExceeded, res.ExitCode)
	}
	if int64(len(res.Stdout)) != maxBytes {
		t.Errorf("expected exactly %d bytes in stdout, got %d", maxBytes, len(res.Stdout))
	}
	if !strings.Contains(res.Stderr, "output byte limit exceeded") {
		t.Errorf("expected Stderr to report limit exceeded, got %q", res.Stderr)
	}
}

// TestLimits_PipelineSharedOutputBudget verifies that concurrent pipeline stages
// cannot bypass the combined output budget.
func TestLimits_PipelineSharedOutputBudget(t *testing.T) {
	emitCmd := command.New("emit", func(ctx context.Context, ec *command.Context) error {
		for i := 0; i < 50; i++ {
			if _, err := fmt.Fprintf(ec.Stdout, "stage1 line %d\n", i); err != nil {
				return err
			}
		}
		return nil
	})

	const maxBudget = 100
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(emitCmd, commands.Cat),
		Limits: cordon.Limits{
			MaxOutputBytes: maxBudget,
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	res, err := sb.ExecBash(context.Background(), "emit | cat")
	if err != nil {
		t.Fatalf("ExecBash unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true when pipeline exceeds output budget")
	}
	if res.ExitCode != status.StatusLimitExceeded {
		t.Errorf("expected exit code %d (StatusLimitExceeded), got %d", status.StatusLimitExceeded, res.ExitCode)
	}
	if int64(len(res.Stdout)) > maxBudget {
		t.Errorf("stdout length %d exceeded maxBudget %d", len(res.Stdout), maxBudget)
	}
}

// TestLimits_CommandCountBudgetInPipeline verifies that command executions
// across a pipeline share and decrement the call's command budget.
func TestLimits_CommandCountBudgetInPipeline(t *testing.T) {
	echoCmd := command.New("echo", func(ctx context.Context, ec *command.Context) error {
		fmt.Fprintln(ec.Stdout, strings.Join(ec.Args[1:], " "))
		return nil
	})

	// Pipeline has 3 commands: echo, cat, wc. Limit is 2.
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(echoCmd, commands.Cat, commands.Wc),
		Limits: cordon.Limits{
			MaxCommandCount: 2,
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	res, err := sb.ExecBash(context.Background(), `echo "hello" | cat | wc -l`)
	if err != nil {
		t.Fatalf("ExecBash unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true when pipeline command count exceeds budget")
	}
	if res.ExitCode != status.StatusLimitExceeded {
		t.Errorf("expected exit code %d (StatusLimitExceeded), got %d", status.StatusLimitExceeded, res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "command limit exceeded") {
		t.Errorf("expected Stderr to report command limit exceeded, got %q", res.Stderr)
	}
}

// TestLimits_CommandCountBudgetInLoop verifies that shell loop invocations
// are bounded by MaxCommandCount.
func TestLimits_CommandCountBudgetInLoop(t *testing.T) {
	echoCmd := command.New("echo", func(ctx context.Context, ec *command.Context) error {
		fmt.Fprintln(ec.Stdout, strings.Join(ec.Args[1:], " "))
		return nil
	})

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(echoCmd),
		Limits: cordon.Limits{
			MaxCommandCount: 3,
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	res, err := sb.ExecBash(context.Background(), `for i in 1 2 3 4 5; do echo "iter $i"; done`)
	if err != nil {
		t.Fatalf("ExecBash unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true when loop exceeds MaxCommandCount")
	}
	if res.ExitCode != status.StatusLimitExceeded {
		t.Errorf("expected exit code %d (StatusLimitExceeded), got %d", status.StatusLimitExceeded, res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "command limit exceeded") {
		t.Errorf("expected Stderr to report command limit, got %q", res.Stderr)
	}
}

// TestLimits_MaxInputBytesEnforced verifies that excessively large input
// strings or JSON payloads are rejected before execution.
func TestLimits_MaxInputBytesEnforced(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Cat),
		Limits: cordon.Limits{
			MaxInputBytes: 25,
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// 1. ExecBash with command longer than MaxInputBytes
	longCommand := "cat << 'EOF'\n" + strings.Repeat("x", 50) + "\nEOF"
	res, err := sb.ExecBash(context.Background(), longCommand)
	if err != nil {
		t.Fatalf("ExecBash returned dispatch error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true on oversized input")
	}
	if res.ExitCode != status.StatusLimitExceeded {
		t.Errorf("expected exit code %d, got %d", status.StatusLimitExceeded, res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "input byte limit exceeded") {
		t.Errorf("expected Stderr to report input limit exceeded, got %q", res.Stderr)
	}

	// 2. CallTool with JSON payload longer than MaxInputBytes
	bigPayload, _ := json.Marshal(map[string]string{"command": strings.Repeat("A", 40)})
	res, err = sb.CallTool(context.Background(), "bash", bigPayload)
	if err != nil {
		t.Fatalf("CallTool returned dispatch error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true on oversized CallTool input")
	}
	if res.ExitCode != status.StatusLimitExceeded {
		t.Errorf("expected exit code %d, got %d", status.StatusLimitExceeded, res.ExitCode)
	}
}

// TestLimits_BackgroundWorkCannotWriteAfterCleanup verifies that detached background
// goroutines cannot append to call output buffers once call supervisor cleanup has executed.
func TestLimits_BackgroundWorkCannotWriteAfterCleanup(t *testing.T) {
	writeErrChan := make(chan error, 1)

	orphanCmd := command.New("orphan", func(ctx context.Context, ec *command.Context) error {
		outWriter := ec.Stdout
		// Start detached background work
		go func() {
			time.Sleep(40 * time.Millisecond)
			_, err := io.WriteString(outWriter, "rogue background write")
			writeErrChan <- err
		}()
		fmt.Fprintln(ec.Stdout, "call completed")
		return nil
	})

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(orphanCmd),
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	res, err := sb.ExecBash(context.Background(), "orphan")
	if err != nil {
		t.Fatalf("ExecBash unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("ExecBash unexpected result error: %v", res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "call completed" {
		t.Errorf("expected 'call completed', got %q", res.Stdout)
	}

	// Wait for background routine to attempt writing
	select {
	case werr := <-writeErrChan:
		if werr == nil {
			t.Errorf("expected write error from detached background write after call cleanup, got nil")
		}
		if !errors.Is(werr, status.ErrCallClosed) {
			t.Errorf("expected status.ErrCallClosed, got %v", werr)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for background routine write attempt")
	}

	// Verify that the result stdout was never modified by the background write
	if strings.Contains(res.Stdout, "rogue") {
		t.Errorf("rogue write leaked into Result: %q", res.Stdout)
	}
}

// TestLimits_CanceledCallsDoNotCorruptFS verifies that canceling a call mid-write
// leaves the filesystem in a consistent state and doesn't break subsequent calls.
func TestLimits_CanceledCallsDoNotCorruptFS(t *testing.T) {
	mem := fs.Mem().Seed(map[string]string{
		"/original.txt": "pristine content\n",
	})

	writeSlowCmd := command.New("writeslow", func(ctx context.Context, ec *command.Context) error {
		f, err := ec.FS.OpenFile("/slow.txt", 0x2|0x40|0x200, 0644) // O_RDWR|O_CREAT|O_TRUNC
		if err != nil {
			return err
		}
		defer f.Close()

		for i := 0; i < 100; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				_, _ = fmt.Fprintf(f, "chunk %d\n", i)
				time.Sleep(2 * time.Millisecond)
			}
		}
		return nil
	})

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(writeSlowCmd, commands.Cat),
		FS:       mem,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Call 1: Cancel mid-write
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	res1, _ := sb.ExecBash(ctx, "writeslow")
	if !res1.IsError {
		t.Errorf("expected call 1 to be canceled/errored")
	}

	// Call 2: Verify original files are intact and uncorrupted
	res2, err := sb.ExecBash(context.Background(), "cat /original.txt")
	if err != nil || res2.IsError {
		t.Fatalf("call 2 failed: err=%v, res=%+v", err, res2)
	}
	if strings.TrimSpace(res2.Stdout) != "pristine content" {
		t.Errorf("filesystem corrupted: got %q, want 'pristine content'", res2.Stdout)
	}
}

// TestLimits_ConcurrentCallsDoNotLeakBudgets verifies that multiple concurrent calls
// maintain completely independent command and output budgets under high contention.
func TestLimits_ConcurrentCallsDoNotLeakBudgets(t *testing.T) {
	echoCmd := command.New("echo", func(ctx context.Context, ec *command.Context) error {
		fmt.Fprintln(ec.Stdout, strings.Join(ec.Args[1:], " "))
		return nil
	})

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(echoCmd, commands.Cat),
		Limits: cordon.Limits{
			MaxCommandCount: 2,
			MaxOutputBytes:  50,
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Each call executes 2 commands (echo and cat), output ~20 bytes.
			// Both are within the 2-command and 50-byte limits.
			// If budgets were shared or leaked, later calls would immediately fail!
			res, err := sb.ExecBash(context.Background(), fmt.Sprintf(`echo "run %d" | cat`, idx))
			if err != nil {
				t.Errorf("worker %d returned error: %v", idx, err)
				return
			}
			if res.IsError {
				t.Errorf("worker %d failed unexpectedly: %s", idx, res.Stderr)
			}
		}(i)
	}
	wg.Wait()
}
