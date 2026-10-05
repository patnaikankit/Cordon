package js

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/command"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/internal/status"
	"github.com/dop251/goja"
)

const jsInputSchema = `{"type":"object","properties":{"code":{"type":"string","description":"JavaScript code to execute."},"script":{"type":"string","description":"Alias for code."}}}`

// Tool returns a cordon.ToolBinding for executing sandboxed JavaScript code.
func Tool() cordon.ToolBinding {
	return cordon.ToolBinding{
		Tool: cordon.Tool{
			Name:        "js",
			Description: "Execute a sandboxed JavaScript script with filesystem and network policies applied.",
			InputSchema: json.RawMessage(jsInputSchema),
		},
		Handler: RunTool,
	}
}

// RunTool is the cordon.ToolHandler for JavaScript execution.
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

	return runScript(ctx, "<tool:js>", code, caps, stdout, stderr)
}

// Command returns a command.Command for running node from the bash shell.
func Command() command.Command {
	return &jsCommand{name: "node"}
}

// JSCommand returns a command.Command for running js from the bash shell.
func JSCommand() command.Command {
	return &jsCommand{name: "js"}
}

type jsCommand struct {
	name string
}

func (c *jsCommand) Name() string {
	return c.name
}

func (c *jsCommand) Run(ctx context.Context, ec *command.Context) error {
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
	} else if args[0] == "-e" {
		if len(args) < 2 {
			fmt.Fprintf(ec.Stderr, "%s: option -e requires an argument\n", c.name)
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

// runScript executes JavaScript code under context and capability constraints using Goja.
func runScript(ctx context.Context, filename, code string, caps cordon.Capabilities, stdout, stderr io.Writer) (int, error) {
	vm := goja.New()
	vm.SetMaxCallStackSize(1000)

	// Cooperative cancellation monitor
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt(ctx.Err())
		case <-stop:
		}
	}()

	setupEnvironment(ctx, vm, caps, stdout, stderr)

	_, err := vm.RunScript(filename, code)
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

// setupEnvironment installs sandboxed console, fs, and fetch globals.
func setupEnvironment(ctx context.Context, vm *goja.Runtime, caps cordon.Capabilities, stdout, stderr io.Writer) {
	// console
	console := vm.NewObject()
	_ = console.Set("log", func(call goja.FunctionCall) goja.Value {
		var parts []string
		for _, arg := range call.Arguments {
			parts = append(parts, fmt.Sprint(arg.Export()))
		}
		fmt.Fprintln(stdout, strings.Join(parts, " "))
		return goja.Undefined()
	})
	_ = console.Set("error", func(call goja.FunctionCall) goja.Value {
		var parts []string
		for _, arg := range call.Arguments {
			parts = append(parts, fmt.Sprint(arg.Export()))
		}
		fmt.Fprintln(stderr, strings.Join(parts, " "))
		return goja.Undefined()
	})
	_ = console.Set("warn", func(call goja.FunctionCall) goja.Value {
		var parts []string
		for _, arg := range call.Arguments {
			parts = append(parts, fmt.Sprint(arg.Export()))
		}
		fmt.Fprintln(stderr, strings.Join(parts, " "))
		return goja.Undefined()
	})
	_ = vm.Set("console", console)

	// fs object
	fsObj := vm.NewObject()
	_ = fsObj.Set("readFileSync", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.ToValue("readFileSync: missing path argument"))
		}
		p := fs.Clean(call.Arguments[0].String())
		data, err := caps.FS().ReadFile(p)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("readFileSync %q: %v", p, err)))
		}
		return vm.ToValue(string(data))
	})
	_ = fsObj.Set("writeFileSync", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(vm.ToValue("writeFileSync: path and data arguments required"))
		}
		p := fs.Clean(call.Arguments[0].String())
		data := call.Arguments[1].String()
		_ = caps.FS().MkdirAll(path.Dir(p), 0755)
		if err := caps.FS().WriteFile(p, []byte(data), 0644); err != nil {
			panic(vm.ToValue(fmt.Sprintf("writeFileSync %q: %v", p, err)))
		}
		return goja.Undefined()
	})
	_ = fsObj.Set("existsSync", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(false)
		}
		p := fs.Clean(call.Arguments[0].String())
		_, err := caps.FS().Stat(p)
		return vm.ToValue(err == nil)
	})
	_ = fsObj.Set("readdirSync", func(call goja.FunctionCall) goja.Value {
		dir := "/"
		if len(call.Arguments) > 0 {
			dir = fs.Clean(call.Arguments[0].String())
		}
		entries, err := caps.FS().ReadDir(dir)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("readdirSync %q: %v", dir, err)))
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return vm.ToValue(names)
	})
	_ = fsObj.Set("unlinkSync", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.ToValue("unlinkSync: missing path argument"))
		}
		p := fs.Clean(call.Arguments[0].String())
		if err := caps.FS().Remove(p); err != nil {
			panic(vm.ToValue(fmt.Sprintf("unlinkSync %q: %v", p, err)))
		}
		return goja.Undefined()
	})
	_ = fsObj.Set("mkdirSync", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.ToValue("mkdirSync: missing path argument"))
		}
		p := fs.Clean(call.Arguments[0].String())
		if err := caps.FS().MkdirAll(p, 0755); err != nil {
			panic(vm.ToValue(fmt.Sprintf("mkdirSync %q: %v", p, err)))
		}
		return goja.Undefined()
	})

	_ = vm.Set("fs", fsObj)
	_ = vm.Set("readFile", fsObj.Get("readFileSync"))
	_ = vm.Set("writeFile", fsObj.Get("writeFileSync"))

	// fetch(url, [options])
	_ = vm.Set("fetch", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.ToValue("fetch: missing url argument"))
		}
		urlStr := call.Arguments[0].String()
		method := "GET"
		var bodyReader io.Reader
		headerMap := make(map[string]string)

		if len(call.Arguments) > 1 {
			if optObj := call.Arguments[1].ToObject(vm); optObj != nil {
				if m := optObj.Get("method"); m != nil && !goja.IsUndefined(m) && !goja.IsNull(m) {
					method = strings.ToUpper(m.String())
				}
				if b := optObj.Get("body"); b != nil && !goja.IsUndefined(b) && !goja.IsNull(b) {
					bodyReader = strings.NewReader(b.String())
				}
				if h := optObj.Get("headers"); h != nil && !goja.IsUndefined(h) && !goja.IsNull(h) {
					if hObj := h.ToObject(vm); hObj != nil {
						for _, k := range hObj.Keys() {
							headerMap[k] = hObj.Get(k).String()
						}
					}
				}
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, urlStr, bodyReader)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("fetch: %v", err)))
		}
		for k, v := range headerMap {
			req.Header.Set(k, v)
		}

		resp, err := caps.HTTPClient().Do(req)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("fetch error: %v", err)))
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("fetch read error: %v", err)))
		}

		respObj := vm.NewObject()
		_ = respObj.Set("status", resp.StatusCode)
		_ = respObj.Set("ok", resp.StatusCode >= 200 && resp.StatusCode < 300)
		_ = respObj.Set("text", func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(string(respBody))
		})
		_ = respObj.Set("json", func(call goja.FunctionCall) goja.Value {
			var parsed interface{}
			if err := json.Unmarshal(respBody, &parsed); err != nil {
				panic(vm.ToValue(fmt.Sprintf("JSON parse error: %v", err)))
			}
			return vm.ToValue(parsed)
		})

		headersObj := vm.NewObject()
		for k, v := range resp.Header {
			_ = headersObj.Set(k, strings.Join(v, ", "))
		}
		_ = respObj.Set("headers", headersObj)

		return respObj
	})
}

var _ command.Command = (*jsCommand)(nil)
