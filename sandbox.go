package cordon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/cordon-dev/cordon/command"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/internal/status"
	"github.com/cordon-dev/cordon/netpolicy"
)

// bashInputSchema defines the JSON schema for the bash tool input.
const bashInputSchema = `{"type":"object","properties":{"command":{"type":"string","description":"The bash command to execute."}},"required":["command"]}`

// Sandbox is an isolated execution environment enforcing filesystem, network, and limit policies.
type Sandbox struct {
	policy   Policy
	fs       fs.FS
	net      netpolicy.Policy
	registry *command.Registry
	mu       sync.RWMutex
}

// New creates a new Sandbox configured with the specified Policy.
// Safe zero-values are guaranteed: no commands, empty virtual filesystem,
// no network access, and safe execution limits.
func New(policy Policy) (*Sandbox, error) {
	reg := command.NewRegistry()
	if len(policy.Commands.Allow) > 0 {
		reg.Register(policy.Commands.Allow...)
	}

	fsys := policy.FS
	if fsys == nil {
		fsys = fs.Nop()
	}

	workDir := policy.Commands.WorkDir
	if workDir == "" {
		workDir = "/"
	}

	return &Sandbox{
		policy:   policy,
		fs:       fsys,
		net:      policy.Network,
		registry: reg,
	}, nil
}

// Resources returns the capability bundle for custom tools without exposing Sandbox internals.
func (s *Sandbox) Resources() Capabilities {
	return NewCapabilities(s.fs, s.policy.Limits.Timeout)
}

// Tools advertises every enabled execution surface.
func (s *Sandbox) Tools() []Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var tools []Tool

	cmdNames := s.registry.Names()
	sort.Strings(cmdNames)

	var desc string
	if len(cmdNames) == 0 {
		desc = "Execute a bash command in a sandboxed shell (no commands permitted by policy)."
	} else {
		desc = fmt.Sprintf("Execute a bash command in a sandboxed shell. Available commands: %s.", strings.Join(cmdNames, ", "))
	}

	tools = append(tools, Tool{
		Name:        "bash",
		Description: desc,
		InputSchema: json.RawMessage(bashInputSchema),
	})

	return tools
}

// CallTool dispatches a tool call by name with JSON-encoded arguments.
// A dispatch failure (unknown tool or malformed input) returns a Go error.
// A tool execution failure (policy denial, exit code, limit breach) returns a normal Result.
func (s *Sandbox) CallTool(ctx context.Context, name string, input json.RawMessage) (Result, error) {
	switch name {
	case "bash":
		var in struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return Result{}, fmt.Errorf("%w: %s", status.ErrMalformedInput, err.Error())
		}
		return s.ExecBash(ctx, in.Command)
	default:
		return Result{}, fmt.Errorf("%w: %q", status.ErrUnknownTool, name)
	}
}

// ExecBash runs a shell command string in the sandbox under policy supervision.
// In Phase 1, this operates as a call supervisor stub dispatching allowlisted commands
// without pulling in a full shell or network dependency.
func (s *Sandbox) ExecBash(ctx context.Context, commandStr string) (Result, error) {
	sup := newCallSupervisor(ctx, s.policy.Limits)

	res := sup.Execute(func(callCtx context.Context, stdout, stderr io.Writer, cb *commandBudget) (int, error) {
		trimmed := strings.TrimSpace(commandStr)
		if trimmed == "" {
			return status.StatusOK, nil
		}

		// Deny-by-default invariant: zero-value or empty command policy denies execution.
		if s.registry.Len() == 0 {
			return status.StatusPolicyDenied, status.ErrPolicyDenied
		}

		// Simple command tokenization for Phase 1 stub execution.
		// Phase 3 will introduce mvdan.cc/sh integration for full bash syntax.
		parts := strings.Fields(trimmed)
		cmdName := parts[0]
		args := parts[1:]

		// Enforce command budget
		if err := cb.Acquire(); err != nil {
			return status.StatusLimitExceeded, err
		}

		cmd, ok := s.registry.Lookup(cmdName)
		if !ok {
			fmt.Fprintf(stderr, "cordon: command not found: %s\n", cmdName)
			return status.StatusNotFound, status.ErrCommandNotFound
		}

		env := make(map[string]string)
		for k, v := range s.policy.Commands.Env {
			env[k] = v
		}

		workDir := s.policy.Commands.WorkDir
		if workDir == "" {
			workDir = "/"
		}

		ec := &command.Context{
			Args:    append([]string{cmdName}, args...),
			Env:     env,
			WorkDir: workDir,
			Stdin:   strings.NewReader(""),
			Stdout:  stdout,
			Stderr:  stderr,
			FS:      s.fs,
		}

		err := cmd.Run(callCtx, ec)
		if err != nil {
			var exitErr *command.ExitError
			if errors.As(err, &exitErr) {
				return exitErr.Code, nil
			}
			return status.StatusError, err
		}

		return status.StatusOK, nil
	})

	return res, nil
}
