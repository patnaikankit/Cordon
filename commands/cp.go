package commands

import (
	"context"

	"github.com/cordon-dev/cordon/command"
)

type cpCmd struct{}

func (cpCmd) Name() string { return "cp" }

func (cpCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "rR", "")
	if err != nil {
		return ec.Fail(2, "cp: %v\n", err)
	}
	recursive := o.set['r'] || o.set['R']

	moves, ferr := resolveMoves(ec, "cp", o.args)
	if ferr != nil {
		return ferr
	}
	exit := 0
	for _, m := range moves {
		if err := copyPath(ec, m.srcAbs, m.targetAbs, recursive); err != nil {
			ec.Errorf("cp: %v\n", err)
			exit = 1
		}
	}
	return command.Exit(exit)
}

var Cp command.Command = cpCmd{}
