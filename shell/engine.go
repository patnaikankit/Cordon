package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"maps"
	"os"
	"strings"
	"sync/atomic"

	"github.com/goccy/sh/v3/expand"
	"github.com/goccy/sh/v3/interp"
	"github.com/goccy/sh/v3/syntax"

	"github.com/cordon-dev/cordon/command"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/internal/status"
)

// Exit codes conforming to standard shell behavior.
const (
	ExitOK       = 0
	ExitFailure  = 1
	ExitUsage    = 2
	ExitNotFound = 127
	MaxExitCode  = 255
)

// ErrCommandLimit is returned when command executions exceed MaxCommandCount.
var ErrCommandLimit = status.ErrCommandLimit

// CommandAcquirer defines an interface for acquiring execution budget slots.
type CommandAcquirer interface {
	Acquire() error
}

// errHostProbe is returned when a host-probing builtin (such as type or command -v) is invoked.
var errHostProbe = errors.New("host-probing builtin refused")

// Config specifies the configuration for the sandboxed shell Engine.
type Config struct {
	FS                fs.FS
	Reg               *command.Registry
	WorkDir           string
	Env               map[string]string
	MaxCommandCount   int
	MaxMemoryBytes    int64
	MaxRecursionDepth int
}

// Engine executes shell commands inside an isolated environment.
// It uses pure-Go parser and interpreter mechanics, routing all commands
// to an explicit allowlist and all filesystem operations to a virtual FS.
type Engine struct {
	fs           fs.FS
	reg          *command.Registry
	dir          string
	env          expand.Environ
	maxCommands  int
	maxMemory    int64
	maxRecursion int
}

// New constructs an Engine with safe environment isolation.
func New(cfg Config) *Engine {
	dir := cfg.WorkDir
	if dir == "" {
		dir = "/"
	}

	// Seed sandbox defaults so host environment variables (such as host $HOME, $USER, $UID)
	// are never leaked into the sandbox environment.
	envMap := map[string]string{
		"HOME":    dir,
		"PWD":     dir,
		"USER":    "sandbox",
		"LOGNAME": "sandbox",
		"UID":     "1000",
		"EUID":    "1000",
		"GID":     "1000",
	}

	maps.Copy(envMap, cfg.Env)

	// Block host process-substitution FIFOs by mapping TMPDIR to os.DevNull.
	envMap["TMPDIR"] = os.DevNull

	pairs := make([]string, 0, len(envMap))
	for k, v := range envMap {
		pairs = append(pairs, k+"="+v)
	}

	return &Engine{
		fs:           cfg.FS,
		reg:          cfg.Reg,
		dir:          dir,
		env:          expand.ListEnviron(pairs...),
		maxCommands:  cfg.MaxCommandCount,
		maxMemory:    cfg.MaxMemoryBytes,
		maxRecursion: cfg.MaxRecursionDepth,
	}
}

// Run parses and executes a command string under policy enforcement.
func (e *Engine) Run(ctx context.Context, commandStr string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	return e.RunWithBudget(ctx, commandStr, stdin, stdout, stderr, nil)
}

// RunWithBudget parses and executes a command string under policy enforcement using a specific command budget.
func (e *Engine) RunWithBudget(ctx context.Context, commandStr string, stdin io.Reader, stdout, stderr io.Writer, cb CommandAcquirer) (int, error) {
	recLimit := e.maxRecursion
	if recLimit <= 0 {
		recLimit = 256
	}

	file, err := syntax.NewParser(syntax.RecursionLimit(recLimit)).Parse(strings.NewReader(commandStr), "")
	if err != nil {
		return ExitUsage, err
	}

	// Reject process substitution <(...) or coproc which attempt to create host FIFOs.
	if feature := unsupportedShellFeature(file); feature != "" {
		fmt.Fprintf(stderr, "%s: not supported in the sandbox\n", feature)
		return ExitUsage, nil
	}

	var count atomic.Int64
	runner, err := interp.New(
		interp.StdIO(stdin, stdout, stderr),
		interp.Env(e.env),
		interp.MaxExpandBytes(e.maxMemory),
		interp.RecursionLimit(recLimit),
		interp.ExecHandlers(e.execMiddleware),
		interp.OpenHandler(e.openHandler),
		interp.StatHandler(e.statHandler),
		interp.ReadDirHandler2(e.readDirHandler),
		interp.CallHandler(func(_ context.Context, args []string) ([]string, error) {
			if cb != nil {
				if err := cb.Acquire(); err != nil {
					return nil, err
				}
			} else if e.maxCommands > 0 && count.Add(1) > int64(e.maxCommands) {
				return nil, ErrCommandLimit
			}
			if name := hostProbeName(args); name != "" {
				fmt.Fprintf(stderr, "%s: not available in the sandbox\n", name)
				return nil, errHostProbe
			}
			return args, nil
		}),
	)
	if err != nil {
		return ExitUsage, err
	}

	// Set virtual working directory directly to avoid host directory checks.
	runner.Dir = e.dir

	if err := runner.Run(ctx, file); err != nil {
		if errors.Is(err, errHostProbe) {
			return ExitNotFound, nil
		}
		var exitStatus interp.ExitStatus
		if errors.As(err, &exitStatus) {
			return int(exitStatus), nil
		}
		return ExitFailure, err
	}

	return ExitOK, nil
}

func unsupportedShellFeature(file *syntax.File) string {
	var found string
	syntax.Walk(file, func(node syntax.Node) bool {
		if found != "" {
			return false
		}
		switch node.(type) {
		case *syntax.ProcSubst:
			found = "process substitution"
		case *syntax.CoprocClause:
			found = "coproc"
		}
		return found == ""
	})
	return found
}

func hostProbeName(args []string) string {
	i := 0
	for i < len(args) && (args[i] == "builtin" || args[i] == "command") {
		if args[i] == "command" {
			for j := i + 1; j < len(args); j++ {
				if !strings.HasPrefix(args[j], "-") {
					break
				}
				if strings.ContainsAny(args[j], "vV") {
					return "command " + args[j]
				}
			}
		}
		i++
	}
	if i < len(args) && args[i] == "type" {
		return "type"
	}
	return ""
}

func (e *Engine) execMiddleware(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	_ = next
	return func(ctx context.Context, args []string) (rerr error) {
		hc := interp.HandlerCtx(ctx)
		if len(args) == 0 {
			return nil
		}

		cmd, ok := e.reg.Lookup(args[0])
		if !ok {
			fmt.Fprintf(hc.Stderr, "%s: command not found\n", args[0])
			return interp.ExitStatus(ExitNotFound)
		}

		ec := &command.Context{
			Ctx:     ctx,
			Args:    args,
			Env:     hc.Env,
			Dir:     hc.Dir,
			WorkDir: hc.Dir,
			Stdin:   hc.Stdin,
			Stdout:  hc.Stdout,
			Stderr:  hc.Stderr,
			FS:      newDevFS(e.fs, hc.Stdout, hc.Stderr),
		}

		defer func() {
			if r := recover(); r != nil {
				if perr, ok := r.(error); ok {
					rerr = fmt.Errorf("%s: recovered from panic: %w", args[0], perr)
				} else {
					rerr = fmt.Errorf("%s: recovered from panic: %v", args[0], r)
				}
			}
		}()

		err := cmd.Run(ctx, ec)
		if err == nil {
			return nil
		}

		var ee *command.ExitError
		if errors.As(err, &ee) {
			code := ee.Code
			if code < ExitOK || code > MaxExitCode {
				code = ExitFailure
			}
			return interp.ExitStatus(code)
		}

		fmt.Fprintf(hc.Stderr, "%s: %v\n", args[0], err)
		return interp.ExitStatus(ExitFailure)
	}
}

func (e *Engine) openHandler(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	hc := interp.HandlerCtx(ctx)
	resolved := fs.Resolve(hc.Dir, path)

	if dev, ok := openDev(resolved, hc.Stdout, hc.Stderr); ok {
		return dev, nil
	}

	f, err := e.fs.OpenFile(resolved, flag, perm)
	if err != nil {
		return nil, asPathError("open", path, err)
	}
	return f, nil
}

func (e *Engine) statHandler(_ context.Context, name string, _ bool) (iofs.FileInfo, error) {
	clean := fs.Clean(name)
	if dev, ok := openDev(clean, nil, nil); ok {
		return dev.Stat()
	}
	info, err := e.fs.Stat(clean)
	if err != nil {
		return nil, asPathError("stat", name, err)
	}
	return info, nil
}

func (e *Engine) readDirHandler(ctx context.Context, path string) ([]iofs.DirEntry, error) {
	hc := interp.HandlerCtx(ctx)
	entries, err := e.fs.ReadDir(fs.Resolve(hc.Dir, path))
	if err != nil {
		return nil, asPathError("readdir", path, err)
	}
	return entries, nil
}

func asPathError(op, path string, err error) error {
	var pe *iofs.PathError
	if errors.As(err, &pe) {
		return pe
	}
	return &iofs.PathError{Op: op, Path: path, Err: err}
}
