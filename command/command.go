package command

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/goccy/sh/v3/expand"

	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/netpolicy"
)

// Context carries everything an executing command may access.
// Commands must use these handles rather than the host os/net packages
// to ensure that Cordon's capability boundaries and policy confinement are preserved.
type Context struct {
	Ctx     context.Context
	Args    []string
	Env     expand.Environ
	Dir     string
	WorkDir string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	FS      fs.FS
	Network netpolicy.Policy
}

// GetEnv returns the value of an environment variable from the sandboxed environment.
func (c *Context) GetEnv(name string) string {
	if c.Env == nil {
		return ""
	}
	return c.Env.Get(name).String()
}

// Resolve resolves a relative or absolute path relative to the command's current working directory.
func (c *Context) Resolve(name string) string {
	dir := c.Dir
	if dir == "" {
		dir = c.WorkDir
	}
	if dir == "" {
		dir = "/"
	}
	return fs.Resolve(dir, name)
}

// StdinReader returns Stdin or an empty reader if Stdin is nil,
// preventing nil-reader panics in commands that read from standard input.
func (c *Context) StdinReader() io.Reader {
	if c.Stdin == nil {
		return strings.NewReader("")
	}
	return c.Stdin
}

// Errorf writes a formatted error message to standard error.
func (c *Context) Errorf(format string, a ...any) {
	fmt.Fprintf(c.Stderr, format, a...)
}

// Fail writes a formatted diagnostic to standard error and returns an ExitError.
func (c *Context) Fail(code int, format string, a ...any) error {
	fmt.Fprintf(c.Stderr, format, a...)
	return Exit(code)
}

// Command is a single executable command (such as cat, grep, or a user-defined command).
type Command interface {
	Name() string
	Run(ctx context.Context, ec *Context) error
}

// commandFunc adapts a plain function into a Command.
type commandFunc struct {
	name string
	fn   func(ctx context.Context, ec *Context) error
}

func (f commandFunc) Name() string { return f.name }
func (f commandFunc) Run(ctx context.Context, ec *Context) error {
	return f.fn(ctx, ec)
}

// New adapts a plain function into a Command with the given name.
func New(name string, fn func(ctx context.Context, ec *Context) error) Command {
	return commandFunc{name: name, fn: fn}
}

// ExitError carries a non-zero exit code from a command.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("exit status %d", e.Code)
}

// Exit returns an ExitError representing the given exit code, or nil if code is 0.
func Exit(code int) error {
	if code == 0 {
		return nil
	}
	return &ExitError{Code: code}
}
