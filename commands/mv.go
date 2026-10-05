package commands

import (
	"context"

	"github.com/cordon-dev/cordon/command"
)

type mvCmd struct{}

func (mvCmd) Name() string { return "mv" }

func (mvCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "f", "")
	if err != nil {
		return ec.Fail(2, "mv: %v\n", err)
	}
	moves, ferr := resolveMoves(ec, "mv", o.args)
	if ferr != nil {
		return ferr
	}
	exit := 0
	for _, m := range moves {
		if err := ec.FS.Rename(m.srcAbs, m.targetAbs); err != nil {
			ec.Errorf("mv: cannot move '%s' to '%s': %s\n", m.srcDisp, m.dstDisp, errMsg(err))
			exit = 1
		}
	}
	return command.Exit(exit)
}

var Mv command.Command = mvCmd{}
