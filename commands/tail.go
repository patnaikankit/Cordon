package commands

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/cordon-dev/cordon/command"
)

type tailCmd struct{}

func (tailCmd) Name() string { return "tail" }

func (tailCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(normalizeCountArgs(ec.Args[1:]), "", "nc")
	if err != nil {
		return ec.Fail(2, "tail: %v\n", err)
	}
	n, sign, byteMode, emsg := headTailCount(o)
	if emsg != "" {
		return ec.Fail(2, "tail: %s\n", emsg)
	}
	fromStart := sign == '+'

	emit := func(r io.Reader) error {
		if byteMode {
			return tailEmitBytes(ctx, ec.Stdout, r, n, fromStart)
		}
		sc := newLineScanner(r)
		var lines []string
		for sc.Scan() {
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			lines = append(lines, sc.Text())
		}
		if err := sc.Err(); err != nil {
			return err
		}
		start := 0
		if fromStart {
			start = min(max(n-1, 0), len(lines))
		} else if len(lines) > n {
			start = len(lines) - n
		}
		for _, l := range lines[start:] {
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprintln(ec.Stdout, l)
		}
		return nil
	}

	if len(o.args) == 0 {
		if err := emit(ec.StdinReader()); err != nil {
			return ec.Fail(1, "tail: %v\n", err)
		}
		return nil
	}

	exit := 0
	multi := len(o.args) > 1
	for i, name := range o.args {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		data, err := ec.FS.ReadFile(ec.Resolve(name))
		if err != nil {
			ec.Errorf("tail: cannot open '%s' for reading: %s\n", name, errMsg(err))
			exit = 1
			continue
		}
		if multi {
			if i > 0 {
				fmt.Fprintln(ec.Stdout)
			}
			fmt.Fprintf(ec.Stdout, "==> %s <==\n", name)
		}
		if err := emit(bytes.NewReader(data)); err != nil {
			ec.Errorf("tail: %v\n", err)
			exit = 1
		}
	}
	return command.Exit(exit)
}

func tailEmitBytes(ctx context.Context, w io.Writer, r io.Reader, n int, fromStart bool) error {
	data, err := readAllContext(ctx, r)
	if err != nil {
		return err
	}
	start := 0
	if fromStart {
		start = min(max(n-1, 0), len(data))
	} else if len(data) > n {
		start = len(data) - n
	}
	_, err = w.Write(data[start:])
	return err
}

var Tail command.Command = tailCmd{}
