package commands

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/cordon-dev/cordon/command"
)

type catCmd struct{}

func (catCmd) Name() string { return "cat" }

func (catCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "n", "")
	if err != nil {
		return ec.Fail(2, "cat: %v\n", err)
	}
	number := o.set['n']
	line := 1

	write := func(r io.Reader) error {
		if !number {
			_, err := copyContext(ctx, ec.Stdout, r)
			return err
		}
		sc := newLineScanner(r)
		for sc.Scan() {
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprintf(ec.Stdout, "%6d\t%s\n", line, sc.Text())
			line++
		}
		return sc.Err()
	}

	if len(o.args) == 0 {
		if err := write(ec.StdinReader()); err != nil {
			return ec.Fail(1, "cat: %v\n", err)
		}
		return nil
	}

	exit := 0
	for _, name := range o.args {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if name == "-" {
			if err := write(ec.StdinReader()); err != nil {
				ec.Errorf("cat: %v\n", err)
				exit = 1
			}
			continue
		}
		data, err := ec.FS.ReadFile(ec.Resolve(name))
		if err != nil {
			ec.Errorf("cat: %s: %s\n", name, errMsg(err))
			exit = 1
			continue
		}
		if err := write(bytes.NewReader(data)); err != nil {
			ec.Errorf("cat: %v\n", err)
			exit = 1
		}
	}
	return command.Exit(exit)
}

var Cat command.Command = catCmd{}
