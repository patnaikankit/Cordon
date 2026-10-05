package shell_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/cordon-dev/cordon/command"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/shell"
)

func TestEngine_Pipes(t *testing.T) {
	upperCmd := command.New("uppercase", func(ctx context.Context, ec *command.Context) error {
		data, err := io.ReadAll(ec.StdinReader())
		if err != nil {
			return err
		}
		_, err = io.WriteString(ec.Stdout, strings.ToUpper(string(data)))
		return err
	})

	reg := command.NewRegistry()
	reg.Register(upperCmd)

	eng := shell.New(shell.Config{
		FS:  fs.Mem(),
		Reg: reg,
	})

	var stdout, stderr bytes.Buffer
	code, err := eng.Run(context.Background(), `echo "hello world" | uppercase`, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d, stderr=%q", code, stderr.String())
	}
	if stdout.String() != "HELLO WORLD\n" {
		t.Errorf("got %q, want 'HELLO WORLD\\n'", stdout.String())
	}
}

func TestEngine_Redirects(t *testing.T) {
	catCmd := command.New("cat", func(ctx context.Context, ec *command.Context) error {
		if len(ec.Args) > 1 {
			for _, name := range ec.Args[1:] {
				data, err := ec.FS.ReadFile(ec.Resolve(name))
				if err != nil {
					return ec.Fail(1, "cat: %v\n", err)
				}
				_, _ = ec.Stdout.Write(data)
			}
			return nil
		}
		data, err := io.ReadAll(ec.StdinReader())
		if err != nil {
			return err
		}
		_, err = ec.Stdout.Write(data)
		return err
	})

	mem := fs.Mem()
	reg := command.NewRegistry()
	reg.Register(catCmd)

	eng := shell.New(shell.Config{
		FS:  mem,
		Reg: reg,
	})

	// 1. Output redirection >
	var stdout, stderr bytes.Buffer
	code, err := eng.Run(context.Background(), `echo "first line" > /out.txt`, strings.NewReader(""), &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("redirect > failed: code=%d, err=%v, stderr=%q", code, err, stderr.String())
	}

	// 2. Append redirection >>
	code, err = eng.Run(context.Background(), `echo "second line" >> /out.txt`, strings.NewReader(""), &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("redirect >> failed: code=%d, err=%v, stderr=%q", code, err, stderr.String())
	}

	data, err := mem.ReadFile("/out.txt")
	if err != nil {
		t.Fatalf("ReadFile /out.txt failed: %v", err)
	}
	expected := "first line\nsecond line\n"
	if string(data) != expected {
		t.Errorf("got %q, want %q", string(data), expected)
	}

	// 3. Input redirection <
	stdout.Reset()
	code, err = eng.Run(context.Background(), `cat < /out.txt`, strings.NewReader(""), &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("redirect < failed: code=%d, err=%v", code, err)
	}
	if stdout.String() != expected {
		t.Errorf("cat < /out.txt got %q, want %q", stdout.String(), expected)
	}

	// 4. Virtual /dev/null redirection
	stdout.Reset()
	code, err = eng.Run(context.Background(), `echo "silence" > /dev/null`, strings.NewReader(""), &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("redirect > /dev/null failed: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected no stdout from /dev/null redirect, got %q", stdout.String())
	}

	// 5. Virtual /dev/stdout redirection
	stdout.Reset()
	code, err = eng.Run(context.Background(), `echo "to-stdout" > /dev/stdout`, strings.NewReader(""), &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("redirect > /dev/stdout failed: %v", err)
	}
	if stdout.String() != "to-stdout\n" {
		t.Errorf("got %q, want 'to-stdout\\n'", stdout.String())
	}
}

func TestEngine_Subshells(t *testing.T) {
	eng := shell.New(shell.Config{
		FS:  fs.Mem(),
		Reg: command.NewRegistry(),
	})

	var stdout, stderr bytes.Buffer
	script := `x=outer; (x=inner; echo "subshell: $x"); echo "parent: $x"`
	code, err := eng.Run(context.Background(), script, strings.NewReader(""), &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("subshell run failed: code=%d, err=%v, stderr=%q", code, err, stderr.String())
	}

	expected := "subshell: inner\nparent: outer\n"
	if stdout.String() != expected {
		t.Errorf("got %q, want %q", stdout.String(), expected)
	}
}

func TestEngine_Globbing(t *testing.T) {
	mem := fs.Mem().Seed(map[string]string{
		"/app/a.go":    "package a",
		"/app/b.go":    "package b",
		"/app/c.txt":   "text",
		"/app/d.go":    "package d",
	})

	eng := shell.New(shell.Config{
		FS:      mem,
		Reg:     command.NewRegistry(),
		WorkDir: "/app",
	})

	var stdout, stderr bytes.Buffer
	script := `for f in *.go; do echo $f; done`
	code, err := eng.Run(context.Background(), script, strings.NewReader(""), &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("globbing failed: code=%d, err=%v, stderr=%q", code, err, stderr.String())
	}

	expected := "a.go\nb.go\nd.go\n"
	if stdout.String() != expected {
		t.Errorf("got %q, want %q", stdout.String(), expected)
	}
}

func TestEngine_VariablesAndEnvironmentIsolation(t *testing.T) {
	eng := shell.New(shell.Config{
		FS:      fs.Mem(),
		Reg:     command.NewRegistry(),
		WorkDir: "/workspace",
	})

	var stdout, stderr bytes.Buffer
	script := `echo USER=$USER HOME=$HOME PWD=$PWD PATH=$PATH`
	code, err := eng.Run(context.Background(), script, strings.NewReader(""), &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("failed: code=%d, err=%v", code, err)
	}

	out := strings.TrimSpace(stdout.String())
	if !strings.Contains(out, "USER=sandbox") {
		t.Errorf("expected USER=sandbox, got %q", out)
	}
	if !strings.Contains(out, "HOME=/workspace") {
		t.Errorf("expected HOME=/workspace, got %q", out)
	}
	if !strings.Contains(out, "PWD=/workspace") {
		t.Errorf("expected PWD=/workspace, got %q", out)
	}
	if !strings.Contains(out, "PATH=") || strings.Contains(out, "PATH=/usr") {
		t.Errorf("host PATH leaked: %q", out)
	}
	// Verify host username is not leaked
	hostUser := os.Getenv("USER")
	if hostUser != "" && hostUser != "sandbox" && strings.Contains(out, "USER="+hostUser) {
		t.Errorf("host username %q leaked into sandbox: %q", hostUser, out)
	}
}

func TestEngine_ControlFlow(t *testing.T) {
	eng := shell.New(shell.Config{
		FS:  fs.Mem(),
		Reg: command.NewRegistry(),
	})

	var stdout, stderr bytes.Buffer
	script := `
if true; then
	echo "true branch"
else
	echo "false branch"
fi

count=0
for i in 1 2 3; do
	count=$((count + i))
done
echo "sum: $count"
`
	code, err := eng.Run(context.Background(), script, strings.NewReader(""), &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("control flow failed: code=%d, err=%v, stderr=%q", code, err, stderr.String())
	}

	expected := "true branch\nsum: 6\n"
	if stdout.String() != expected {
		t.Errorf("got %q, want %q", stdout.String(), expected)
	}
}

func TestEngine_ExitCodes(t *testing.T) {
	eng := shell.New(shell.Config{
		FS:  fs.Mem(),
		Reg: command.NewRegistry(),
	})

	var stdout, stderr bytes.Buffer
	code, _ := eng.Run(context.Background(), "exit 42", strings.NewReader(""), &stdout, &stderr)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d", code)
	}

	code, _ = eng.Run(context.Background(), "true", strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Errorf("expected exit code 0 for true, got %d", code)
	}

	code, _ = eng.Run(context.Background(), "false", strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected exit code 1 for false, got %d", code)
	}
}

func TestEngine_NoHostFallback(t *testing.T) {
	eng := shell.New(shell.Config{
		FS:  fs.Mem(),
		Reg: command.NewRegistry(), // no commands registered
	})

	// Common host commands that might exist on developer machines
	hostCommands := []string{"whoami", "id", "uname", "ps", "sh", "bash"}

	for _, cmd := range hostCommands {
		var stdout, stderr bytes.Buffer
		code, err := eng.Run(context.Background(), cmd, strings.NewReader(""), &stdout, &stderr)
		if err != nil {
			t.Fatalf("run error for %q: %v", cmd, err)
		}
		if code != shell.ExitNotFound {
			t.Errorf("expected exit code 127 for unregistered host command %q, got %d", cmd, code)
		}
		if !strings.Contains(stderr.String(), "command not found") {
			t.Errorf("expected stderr to report 'command not found' for %q, got %q", cmd, stderr.String())
		}
		if stdout.Len() > 0 {
			t.Errorf("expected no stdout for unregistered command %q, got %q", cmd, stdout.String())
		}
	}
}

func TestEngine_HostProbingBuiltinsRefused(t *testing.T) {
	eng := shell.New(shell.Config{
		FS:  fs.Mem(),
		Reg: command.NewRegistry(),
	})

	probingCommands := []string{
		"type ls",
		"command -v ls",
		"command -V whoami",
	}

	for _, cmd := range probingCommands {
		var stdout, stderr bytes.Buffer
		code, err := eng.Run(context.Background(), cmd, strings.NewReader(""), &stdout, &stderr)
		if err != nil {
			t.Fatalf("run error for %q: %v", cmd, err)
		}
		if code != shell.ExitNotFound {
			t.Errorf("expected exit code 127 for probe %q, got %d", cmd, code)
		}
		if !strings.Contains(stderr.String(), "not available in the sandbox") {
			t.Errorf("expected stderr to report 'not available in the sandbox' for %q, got %q", cmd, stderr.String())
		}
	}
}

func TestEngine_HostFilesystemInaccessibleByDefault(t *testing.T) {
	eng := shell.New(shell.Config{
		FS:  fs.Mem(), // empty virtual filesystem
		Reg: command.NewRegistry(),
	})

	// Test if /etc/passwd or /Users exist inside the sandbox
	var stdout, stderr bytes.Buffer
	code, err := eng.Run(context.Background(), "test -e /etc/passwd", strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("test error: %v", err)
	}
	if code == 0 {
		t.Errorf("host /etc/passwd appeared to exist in empty sandbox!")
	}

	code, err = eng.Run(context.Background(), "test -e /proc/cpuinfo", strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("test error: %v", err)
	}
	if code == 0 {
		t.Errorf("host /proc/cpuinfo appeared to exist in empty sandbox!")
	}
}

func TestEngine_ProcessSubstitutionRejected(t *testing.T) {
	eng := shell.New(shell.Config{
		FS:  fs.Mem(),
		Reg: command.NewRegistry(),
	})

	var stdout, stderr bytes.Buffer
	code, _ := eng.Run(context.Background(), "cat <(echo foo)", strings.NewReader(""), &stdout, &stderr)
	if code != shell.ExitUsage {
		t.Errorf("expected exit code %d for process substitution, got %d", shell.ExitUsage, code)
	}
	if !strings.Contains(stderr.String(), "process substitution: not supported") {
		t.Errorf("expected stderr to report unsupported feature, got %q", stderr.String())
	}
}
