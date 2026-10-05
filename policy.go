package cordon

import (
	"time"

	"github.com/cordon-dev/cordon/command"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/netpolicy"
)

// Policy configures a Cordon Sandbox.
// Its zero value is a safe default: no commands, empty virtual filesystem,
// no network access, and default execution limits.
type Policy struct {
	// Commands configures which commands may run and their execution environment.
	Commands CommandPolicy

	// FS is the sandbox filesystem. A nil FS defaults to an isolated empty filesystem.
	FS fs.FS

	// Network is the network policy. The zero value denies all outbound network access.
	Network netpolicy.Policy

	// Limits bounds execution resources for each call.
	Limits Limits
}

// CommandPolicy selects enabled commands and their execution environment.
type CommandPolicy struct {
	// Allow is the explicit list of permitted commands.
	// Empty by default (deny-all).
	Allow []command.Command

	// Env provides the sandbox environment variables.
	// Host environment variables are never leaked into the sandbox.
	Env map[string]string

	// WorkDir is the initial working directory (defaults to "/").
	WorkDir string
}

// Commands is a convenience helper for constructing a CommandPolicy from an allowlist.
func Commands(cmds ...command.Command) CommandPolicy {
	return CommandPolicy{
		Allow: cmds,
	}
}

// Limits bounds execution resources of a single tool call.
type Limits struct {
	// Timeout, if non-zero, sets a wall-clock limit on call execution.
	Timeout time.Duration

	// MaxCommandCount, if non-zero, bounds how many command invocations a single call may run.
	MaxCommandCount int

	// MaxOutputBytes, if non-zero, caps the combined stdout + stderr byte output of a single call.
	MaxOutputBytes int64

	// MaxMemoryBytes, if non-zero, caps memory for runtimes/interpreters that support a hard limit.
	MaxMemoryBytes int64

	// MaxRecursionDepth, if non-zero, bounds parsing and recursion depth.
	MaxRecursionDepth int
}
