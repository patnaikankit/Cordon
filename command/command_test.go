package command_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/cordon-dev/cordon/command"
)

func TestRegistry(t *testing.T) {
	reg := command.NewRegistry()
	if reg.Len() != 0 {
		t.Errorf("expected 0 commands, got %d", reg.Len())
	}

	cmd1 := command.New("foo", func(ctx context.Context, ec *command.Context) error {
		return nil
	})
	cmd2 := command.New("bar", func(ctx context.Context, ec *command.Context) error {
		return nil
	})

	reg.Register(cmd1, cmd2)
	if reg.Len() != 2 {
		t.Errorf("expected 2 commands, got %d", reg.Len())
	}

	lookupCmd, ok := reg.Lookup("foo")
	if !ok || lookupCmd.Name() != "foo" {
		t.Errorf("failed to lookup foo")
	}

	_, ok = reg.Lookup("nonexistent")
	if ok {
		t.Errorf("expected nonexistent command to not be found")
	}

	names := reg.Names()
	if len(names) != 2 {
		t.Errorf("expected 2 names, got %d", len(names))
	}
}

func TestCommandContextHelpers(t *testing.T) {
	var stderr bytes.Buffer
	ec := &command.Context{
		Stderr: &stderr,
	}

	ec.Errorf("hello %s", "world")
	if stderr.String() != "hello world" {
		t.Errorf("unexpected Stderr output: %q", stderr.String())
	}

	err := ec.Fail(42, "failure %d", 1)
	if err == nil {
		t.Fatal("expected non-nil error from Fail")
	}
	if err.Error() != "exit status 42" {
		t.Errorf("expected 'exit status 42', got %q", err.Error())
	}
}
