package python

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/command"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/internal/status"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

const pythonInputSchema = `{"type":"object","properties":{"code":{"type":"string","description":"Python code to execute."},"script":{"type":"string","description":"Alias for code."}}}`

// Tool returns a cordon.ToolBinding for executing sandboxed Python code.
func Tool() cordon.ToolBinding {
	return cordon.ToolBinding{
		Tool: cordon.Tool{
			Name:        "python",
			Description: "Execute a sandboxed Python script with filesystem and network policies applied.",
			InputSchema: json.RawMessage(pythonInputSchema),
		},
		Handler: RunTool,
	}
}

// RunTool is the cordon.ToolHandler for Python execution.
func RunTool(ctx context.Context, input json.RawMessage, caps cordon.Capabilities, stdout, stderr io.Writer) (int, error) {
	var in struct {
		Code   string `json:"code"`
		Script string `json:"script"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return 0, fmt.Errorf("%w: %s", status.ErrMalformedInput, err.Error())
	}

	code := in.Code
	if code == "" {
		code = in.Script
	}
	if code == "" {
		return 0, fmt.Errorf("%w: missing required 'code' or 'script' field", status.ErrMalformedInput)
	}

	return runScript(ctx, "<tool:python>", code, caps, stdout, stderr)
}

// Command returns a command.Command for running python from the bash shell.
func Command() command.Command {
	return &pythonCommand{name: "python"}
}

// Python3Command returns a command.Command for running python3 from the bash shell.
func Python3Command() command.Command {
	return &pythonCommand{name: "python3"}
}

type pythonCommand struct {
	name string
}

func (c *pythonCommand) Name() string {
	return c.name
}

func (c *pythonCommand) Run(ctx context.Context, ec *command.Context) error {
	caps := cordon.NewCapabilities(ec.FS, ec.Network, 0)

	args := ec.Args
	if len(args) > 0 && (args[0] == c.name || strings.HasSuffix(args[0], "/"+c.name)) {
		args = args[1:]
	}
	var code int
	var err error

	if len(args) == 0 {
		// Read from Stdin
		input, readErr := io.ReadAll(ec.Stdin)
		if readErr != nil {
			fmt.Fprintf(ec.Stderr, "%s: error reading stdin: %v\n", c.name, readErr)
			return command.Exit(status.StatusError)
		}
		code, err = runScript(ctx, "<stdin>", string(input), caps, ec.Stdout, ec.Stderr)
	} else if args[0] == "-c" {
		if len(args) < 2 {
			fmt.Fprintf(ec.Stderr, "%s: option -c requires an argument\n", c.name)
			return command.Exit(status.StatusError)
		}
		code, err = runScript(ctx, "<cmd>", args[1], caps, ec.Stdout, ec.Stderr)
	} else {
		// Read from file path in sandboxed FS
		filePath := args[0]
		data, readErr := ec.FS.ReadFile(filePath)
		if readErr != nil {
			fmt.Fprintf(ec.Stderr, "%s: cannot open file %q: %v\n", c.name, filePath, readErr)
			return command.Exit(status.StatusError)
		}
		code, err = runScript(ctx, filePath, string(data), caps, ec.Stdout, ec.Stderr)
	}

	if err != nil {
		return err
	}
	if code != 0 {
		return command.Exit(code)
	}
	return nil
}

// runScript executes Python/Starlark code under context and capability constraints.
func runScript(ctx context.Context, filename, code string, caps cordon.Capabilities, stdout, stderr io.Writer) (int, error) {
	thread := &starlark.Thread{
		Name: filename,
		Print: func(_ *starlark.Thread, msg string) {
			fmt.Fprintln(stdout, msg)
		},
		Load: func(_ *starlark.Thread, module string) (starlark.StringDict, error) {
			// Invariant: No host imports, subprocesses, or sockets.
			return nil, fmt.Errorf("module import %q is forbidden by policy", module)
		},
	}

	// Cooperative cancellation monitor
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			thread.Cancel(ctx.Err().Error())
		case <-stop:
		}
	}()

	predeclared := buildPredeclared(ctx, caps, stdout, stderr)

	opts := &syntax.FileOptions{
		Set:             true,
		GlobalReassign:  true,
		Recursion:       true,
		While:           true,
		TopLevelControl: true,
	}

	_, err := starlark.ExecFileOptions(opts, thread, filename, code, predeclared)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return status.StatusTimeout, ctx.Err()
		}
		// Write error to stderr and return error code
		fmt.Fprintf(stderr, "%v\n", err)
		return status.StatusError, nil
	}

	return status.StatusOK, nil
}

// buildPredeclared constructs the sandbox built-ins exposed to Python code.
func buildPredeclared(ctx context.Context, caps cordon.Capabilities, stdout, stderr io.Writer) starlark.StringDict {
	d := make(starlark.StringDict)

	// open(path, mode="r")
	d["open"] = starlark.NewBuiltin("open", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var filePath string
		var mode string = "r"
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "path", &filePath, "mode?", &mode); err != nil {
			return nil, err
		}

		cleanPath := fs.Clean(filePath)

		var f fs.File
		var openErr error

		switch mode {
		case "r", "rb":
			f, openErr = caps.FS().OpenFile(cleanPath, os.O_RDONLY, 0)
		case "w", "wb":
			_ = caps.FS().MkdirAll(path.Dir(cleanPath), 0755)
			f, openErr = caps.FS().OpenFile(cleanPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		case "a", "ab":
			_ = caps.FS().MkdirAll(path.Dir(cleanPath), 0755)
			f, openErr = caps.FS().OpenFile(cleanPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		default:
			return nil, fmt.Errorf("open: unsupported mode %q", mode)
		}

		if openErr != nil {
			return nil, openErr
		}

		return &fileHandle{path: cleanPath, f: f, mode: mode}, nil
	})

	// read_file(path)
	d["read_file"] = starlark.NewBuiltin("read_file", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var filePath string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "path", &filePath); err != nil {
			return nil, err
		}
		data, err := caps.FS().ReadFile(fs.Clean(filePath))
		if err != nil {
			return nil, err
		}
		return starlark.String(string(data)), nil
	})

	// write_file(path, content)
	d["write_file"] = starlark.NewBuiltin("write_file", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var filePath string
		var content string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "path", &filePath, "content", &content); err != nil {
			return nil, err
		}
		cleanPath := fs.Clean(filePath)
		_ = caps.FS().MkdirAll(path.Dir(cleanPath), 0755)
		err := caps.FS().WriteFile(cleanPath, []byte(content), 0644)
		if err != nil {
			return nil, err
		}
		return starlark.None, nil
	})

	// list_dir(path="/")
	d["list_dir"] = starlark.NewBuiltin("list_dir", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var dirPath string = "/"
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "path?", &dirPath); err != nil {
			return nil, err
		}
		entries, err := caps.FS().ReadDir(fs.Clean(dirPath))
		if err != nil {
			return nil, err
		}
		var list []starlark.Value
		for _, e := range entries {
			list = append(list, starlark.String(e.Name()))
		}
		return starlark.NewList(list), nil
	})

	// exists(path)
	d["exists"] = starlark.NewBuiltin("exists", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var filePath string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "path", &filePath); err != nil {
			return nil, err
		}
		_, err := caps.FS().Stat(fs.Clean(filePath))
		return starlark.Bool(err == nil), nil
	})

	// remove(path)
	d["remove"] = starlark.NewBuiltin("remove", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var filePath string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "path", &filePath); err != nil {
			return nil, err
		}
		err := caps.FS().Remove(fs.Clean(filePath))
		if err != nil {
			return nil, err
		}
		return starlark.None, nil
	})

	// fetch(url, method="GET", headers=None, body="")
	d["fetch"] = starlark.NewBuiltin("fetch", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var urlStr string
		var method string = "GET"
		var headersVal starlark.Value = starlark.None
		var bodyStr string = ""

		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "url", &urlStr, "method?", &method, "headers?", &headersVal, "body?", &bodyStr); err != nil {
			return nil, err
		}

		var bodyReader io.Reader
		if bodyStr != "" {
			bodyReader = strings.NewReader(bodyStr)
		}

		req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), urlStr, bodyReader)
		if err != nil {
			return nil, err
		}

		if dict, ok := headersVal.(*starlark.Dict); ok {
			for _, item := range dict.Items() {
				key, kOk := starlark.AsString(item.Index(0))
				val, vOk := starlark.AsString(item.Index(1))
				if kOk && vOk {
					req.Header.Set(key, val)
				}
			}
		}

		resp, err := caps.HTTPClient().Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch error: %w", err)
		}
		defer resp.Body.Close()

		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("fetch read error: %w", err)
		}

		respDict := starlark.NewDict(4)
		_ = respDict.SetKey(starlark.String("status_code"), starlark.MakeInt(resp.StatusCode))
		_ = respDict.SetKey(starlark.String("ok"), starlark.Bool(resp.StatusCode >= 200 && resp.StatusCode < 300))
		_ = respDict.SetKey(starlark.String("text"), starlark.String(string(respBytes)))

		headerDict := starlark.NewDict(len(resp.Header))
		for k, v := range resp.Header {
			_ = headerDict.SetKey(starlark.String(k), starlark.String(strings.Join(v, ", ")))
		}
		_ = respDict.SetKey(starlark.String("headers"), headerDict)

		return respDict, nil
	})

	return d
}

// fileHandle wraps a cordon fs.File into a Starlark value.
type fileHandle struct {
	path string
	f    fs.File
	mode string
}

func (fh *fileHandle) String() string        { return fmt.Sprintf("<file %q mode=%q>", fh.path, fh.mode) }
func (fh *fileHandle) Type() string          { return "file" }
func (fh *fileHandle) Freeze()               {}
func (fh *fileHandle) Truth() starlark.Bool  { return true }
func (fh *fileHandle) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: file") }

func (fh *fileHandle) AttrNames() []string {
	return []string{"read", "readline", "readlines", "write", "close"}
}

func (fh *fileHandle) Attr(name string) (starlark.Value, error) {
	switch name {
	case "read":
		return starlark.NewBuiltin("file.read", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			var n int = -1
			if err := starlark.UnpackArgs(b.Name(), args, kwargs, "n?", &n); err != nil {
				return nil, err
			}
			if n >= 0 {
				buf := make([]byte, n)
				num, err := fh.f.Read(buf)
				if err != nil && !errors.Is(err, io.EOF) {
					return nil, err
				}
				return starlark.String(string(buf[:num])), nil
			}
			data, err := io.ReadAll(fh.f)
			if err != nil {
				return nil, err
			}
			return starlark.String(string(data)), nil
		}), nil

	case "readline":
		return starlark.NewBuiltin("file.readline", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			var line bytes.Buffer
			buf := make([]byte, 1)
			for {
				n, err := fh.f.Read(buf)
				if n > 0 {
					line.WriteByte(buf[0])
					if buf[0] == '\n' {
						break
					}
				}
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					return nil, err
				}
			}
			return starlark.String(line.String()), nil
		}), nil

	case "readlines":
		return starlark.NewBuiltin("file.readlines", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			var lines []starlark.Value
			data, err := io.ReadAll(fh.f)
			if err != nil {
				return nil, err
			}
			rawLines := strings.Split(string(data), "\n")
			for i, l := range rawLines {
				if i < len(rawLines)-1 {
					lines = append(lines, starlark.String(l+"\n"))
				} else if len(l) > 0 {
					lines = append(lines, starlark.String(l))
				}
			}
			return starlark.NewList(lines), nil
		}), nil

	case "write":
		return starlark.NewBuiltin("file.write", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			var data string
			if err := starlark.UnpackArgs(b.Name(), args, kwargs, "data", &data); err != nil {
				return nil, err
			}
			n, err := fh.f.Write([]byte(data))
			if err != nil {
				return nil, err
			}
			return starlark.MakeInt(n), nil
		}), nil

	case "close":
		return starlark.NewBuiltin("file.close", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			err := fh.f.Close()
			if err != nil {
				return nil, err
			}
			return starlark.None, nil
		}), nil

	default:
		return nil, nil
	}
}

// Compile-time checks
var _ starlark.Value = (*fileHandle)(nil)
var _ starlark.HasAttrs = (*fileHandle)(nil)
var _ command.Command = (*pythonCommand)(nil)

// Suppress unused imports
var _ = path.Clean
var _ iofs.File = nil
