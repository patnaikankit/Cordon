package cordon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/cordon-dev/cordon/internal/status"
)

// outputBudget tracks and limits the combined stdout + stderr byte count for a call.
// It also serializes writes across concurrent streams or pipeline stages.
type outputBudget struct {
	mu      sync.Mutex
	written int64
	max     int64
	limited bool
}

// limitedWriter wraps an io.Writer to enforce a shared outputBudget.
type limitedWriter struct {
	w      io.Writer
	budget *outputBudget
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	l.budget.mu.Lock()
	defer l.budget.mu.Unlock()

	// If max <= 0, writes are serialized but uncapped.
	if l.budget.max <= 0 {
		n, err := l.w.Write(p)
		l.budget.written += int64(n)
		return n, err
	}

	remaining := l.budget.max - l.budget.written
	if remaining <= 0 {
		l.budget.limited = true
		return 0, status.ErrOutputLimit
	}

	if int64(len(p)) > remaining {
		n, _ := l.w.Write(p[:remaining])
		l.budget.written += int64(n)
		l.budget.limited = true
		return n, status.ErrOutputLimit
	}

	n, err := l.w.Write(p)
	l.budget.written += int64(n)
	return n, err
}

// commandBudget bounds the total number of command executions within a single call.
type commandBudget struct {
	max     int
	count   int64
	limited atomic.Bool
}

func newCommandBudget(max int) *commandBudget {
	return &commandBudget{max: max}
}

// Acquire reserves a single command execution slot.
// Returns status.ErrCommandLimit if the budget is exhausted.
func (b *commandBudget) Acquire() error {
	if b.max <= 0 {
		return nil
	}
	newCount := atomic.AddInt64(&b.count, 1)
	if newCount > int64(b.max) {
		b.limited.Store(true)
		return status.ErrCommandLimit
	}
	return nil
}

// IsLimited reports whether the command budget was exceeded.
func (b *commandBudget) IsLimited() bool {
	return b.limited.Load()
}

// callSupervisor oversees execution lifecycle, cancellation, and budgets for a single call.
type callSupervisor struct {
	ctx       context.Context
	cancel    context.CancelFunc
	stdoutBuf *bytes.Buffer
	stderrBuf *bytes.Buffer
	stdout    io.Writer
	stderr    io.Writer
	outBudget *outputBudget
	cmdBudget *commandBudget
	cleanup   func()
}

// newCallSupervisor creates a supervisor instance dedicated to a single call.
// Budgets and contexts are never shared across calls.
func newCallSupervisor(parent context.Context, limits Limits) *callSupervisor {
	var ctx context.Context
	var cancel context.CancelFunc

	if limits.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, limits.Timeout)
	} else {
		ctx, cancel = context.WithCancel(parent)
	}

	outBudget := &outputBudget{max: limits.MaxOutputBytes}
	cmdBudget := newCommandBudget(limits.MaxCommandCount)

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	stdout := &limitedWriter{w: stdoutBuf, budget: outBudget}
	stderr := &limitedWriter{w: stderrBuf, budget: outBudget}

	cleanup := func() {
		cancel()
	}

	return &callSupervisor{
		ctx:       ctx,
		cancel:    cancel,
		stdoutBuf: stdoutBuf,
		stderrBuf: stderrBuf,
		stdout:    stdout,
		stderr:    stderr,
		outBudget: outBudget,
		cmdBudget: cmdBudget,
		cleanup:   cleanup,
	}
}

// runFn is the signature of work executed under call supervision.
type runFn func(ctx context.Context, stdout, stderr io.Writer, cb *commandBudget) (int, error)

// Execute runs the work function under supervision, enforcing cancellation, timeouts,
// output bounds, command count budgets, and panic containment.
func (cs *callSupervisor) Execute(fn runFn) Result {
	defer cs.cleanup()

	type outcome struct {
		code int
		err  error
	}

	done := make(chan outcome, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				var err error
				if perr, ok := r.(error); ok {
					err = fmt.Errorf("%w: %w", status.ErrPanic, perr)
				} else {
					err = fmt.Errorf("%w: %v", status.ErrPanic, r)
				}
				done <- outcome{code: status.StatusError, err: err}
			}
		}()

		code, err := fn(cs.ctx, cs.stdout, cs.stderr, cs.cmdBudget)
		done <- outcome{code: code, err: err}
	}()

	select {
	case <-cs.ctx.Done():
		cs.outBudget.mu.Lock()
		stdoutStr := cs.stdoutBuf.String()
		stderrStr := cs.stderrBuf.String()
		cs.outBudget.mu.Unlock()

		exitCode := status.StatusError
		if errors.Is(cs.ctx.Err(), context.DeadlineExceeded) {
			exitCode = status.StatusTimeout
			if stderrStr == "" {
				stderrStr = "cordon: execution timed out\n"
			}
		} else if errors.Is(cs.ctx.Err(), context.Canceled) {
			exitCode = status.StatusCanceled
			if stderrStr == "" {
				stderrStr = "cordon: execution canceled\n"
			}
		}

		return Result{
			Stdout:   stdoutStr,
			Stderr:   stderrStr,
			ExitCode: exitCode,
			IsError:  true,
		}

	case res := <-done:
		cs.outBudget.mu.Lock()
		stdoutStr := cs.stdoutBuf.String()
		stderrStr := cs.stderrBuf.String()
		wasLimited := cs.outBudget.limited
		cs.outBudget.mu.Unlock()

		exitCode := res.code
		isErr := res.err != nil || exitCode != 0 || wasLimited || cs.cmdBudget.IsLimited()

		switch {
		case errors.Is(res.err, status.ErrPolicyDenied):
			if exitCode == 0 {
				exitCode = status.StatusPolicyDenied
			}
			if stderrStr == "" {
				stderrStr = res.err.Error() + "\n"
			}
		case errors.Is(res.err, status.ErrCommandLimit) || cs.cmdBudget.IsLimited():
			if exitCode == 0 {
				exitCode = status.StatusLimitExceeded
			}
			if stderrStr == "" {
				stderrStr = "cordon: command limit exceeded\n"
			}
		case errors.Is(res.err, status.ErrOutputLimit) || wasLimited:
			if exitCode == 0 {
				exitCode = status.StatusLimitExceeded
			}
		case errors.Is(res.err, status.ErrCommandNotFound):
			if exitCode == 0 {
				exitCode = status.StatusNotFound
			}
			if stderrStr == "" {
				stderrStr = res.err.Error() + "\n"
			}
		case res.err != nil:
			if exitCode == 0 {
				exitCode = status.StatusError
			}
			if stderrStr == "" {
				stderrStr = res.err.Error() + "\n"
			}
		}

		return Result{
			Stdout:   stdoutStr,
			Stderr:   stderrStr,
			ExitCode: exitCode,
			IsError:  isErr,
		}
	}
}
