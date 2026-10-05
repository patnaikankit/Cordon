package commands

import (
	"context"

	"github.com/cordon-dev/cordon/command"
)

type mkdirCmd struct{}

func (mkdirCmd) Name() string { return "mkdir" }

func (mkdirCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "p", "")
	if err != nil {
		return ec.Fail(2, "mkdir: %v\n", err)
	}
	if len(o.args) == 0 {
		return ec.Fail(1, "mkdir: missing operand\n")
	}
	parents := o.set['p']
	exit := 0
	for _, name := range o.args {
		abs := ec.Resolve(name)
		var err error
		if parents {
			err = ec.FS.MkdirAll(abs, 0o755)
		} else {
			err = ec.FS.Mkdir(abs, 0o755)
		}
		if err != nil {
			ec.Errorf("mkdir: cannot create directory '%s': %s\n", name, errMsg(err))
			exit = 1
		}
	}
	return command.Exit(exit)
}

var Mkdir command.Command = mkdirCmd{}
