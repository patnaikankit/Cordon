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
	"github.com/cordon-dev/cordon/shell"
)

// bashInputSchema defines the JSON schema for the bash tool input.
const bashInputSchema = `{"type":"object","properties":{"command":{"type":"string","description":"The bash command to execute."}},"required":["command"]}`

// Sandbox is an isolated execution environment enforcing filesystem, network, and limit policies.
type Sandbox struct {
	policy       Policy
	fs           fs.FS
	net          netpolicy.Policy
	registry     *command.Registry
	engine       *shell.Engine
	toolBindings map[string]ToolBinding
	toolNames    []string
	mu           sync.RWMutex
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

	engine := shell.New(shell.Config{
		FS:                fsys,
		Reg:               reg,
		WorkDir:           workDir,
		Env:               policy.Commands.Env,
		MaxCommandCount:   policy.Limits.MaxCommandCount,
		MaxMemoryBytes:    policy.Limits.MaxMemoryBytes,
		MaxRecursionDepth: policy.Limits.MaxRecursionDepth,
		Network:           policy.Network,
	})

	toolBindings := make(map[string]ToolBinding)
	var toolNames []string
	for _, b := range policy.Tools {
		if b.Tool.Name != "" && b.Handler != nil {
			if _, exists := toolBindings[b.Tool.Name]; !exists {
				toolNames = append(toolNames, b.Tool.Name)
			}
			toolBindings[b.Tool.Name] = b
		}
	}

	return &Sandbox{
		policy:       policy,
		fs:           fsys,
		net:          policy.Network,
		registry:     reg,
		engine:       engine,
		toolBindings: toolBindings,
		toolNames:    toolNames,
	}, nil
}

// RegisterTool registers an additional tool binding on the sandbox.
func (s *Sandbox) RegisterTool(b ToolBinding) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if b.Tool.Name == "" || b.Handler == nil {
		return
	}
	if _, exists := s.toolBindings[b.Tool.Name]; !exists {
		s.toolNames = append(s.toolNames, b.Tool.Name)
	}
	s.toolBindings[b.Tool.Name] = b
}

// Resources returns the capability bundle for custom tools without exposing Sandbox internals.
func (s *Sandbox) Resources() Capabilities {
	return NewCapabilities(s.fs, s.net, s.policy.Limits.Timeout)
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

	for _, name := range s.toolNames {
		tools = append(tools, s.toolBindings[name].Tool)
	}

	return tools
}

// CallTool dispatches a tool call by name with JSON-encoded arguments.
// A dispatch failure (unknown tool or malformed input) returns a Go error.
// A tool execution failure (policy denial, exit code, limit breach) returns a normal Result.
func (s *Sandbox) CallTool(ctx context.Context, name string, input json.RawMessage) (Result, error) {
	if s.policy.Limits.MaxInputBytes > 0 && int64(len(input)) > s.policy.Limits.MaxInputBytes {
		return Result{
			Stderr:   "cordon: input byte limit exceeded\n",
			ExitCode: status.StatusLimitExceeded,
			IsError:  true,
		}, nil
	}

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
		s.mu.RLock()
		b, ok := s.toolBindings[name]
		s.mu.RUnlock()

		if !ok {
			return Result{}, fmt.Errorf("%w: %q", status.ErrUnknownTool, name)
		}

		var malformedErr error
		sup := newCallSupervisor(ctx, s.policy.Limits)
		res := sup.Execute(func(callCtx context.Context, stdout, stderr io.Writer, cb *commandBudget) (int, error) {
			code, err := b.Handler(callCtx, input, s.Resources(), stdout, stderr)
			if err != nil && errors.Is(err, status.ErrMalformedInput) {
				malformedErr = err
				return 0, nil
			}
			return code, err
		})

		if malformedErr != nil {
			return Result{}, malformedErr
		}
		return res, nil
	}
}

// ExecBash runs a shell command string in the sandbox under policy supervision.
// Shell commands run against an isolated virtual filesystem and explicitly allowlisted
// Go commands—never falling back to the host PATH.
func (s *Sandbox) ExecBash(ctx context.Context, commandStr string) (Result, error) {
	if s.policy.Limits.MaxInputBytes > 0 && int64(len(commandStr)) > s.policy.Limits.MaxInputBytes {
		return Result{
			Stderr:   "cordon: input byte limit exceeded\n",
			ExitCode: status.StatusLimitExceeded,
			IsError:  true,
		}, nil
	}

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

		// Execute through the sandboxed shell engine with per-call command budget.
		code, err := s.engine.RunWithBudget(callCtx, commandStr, strings.NewReader(""), stdout, stderr, cb)
		return code, err
	})

	return res, nil
}
