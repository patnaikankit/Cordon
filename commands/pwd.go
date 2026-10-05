package commands

import (
	"context"
	"fmt"

	"github.com/cordon-dev/cordon/command"
)

type pwdCmd struct{}

func (pwdCmd) Name() string { return "pwd" }

func (pwdCmd) Run(ctx context.Context, ec *command.Context) error {
	dir := ec.Dir
	if dir == "" {
		dir = ec.WorkDir
	}
	if dir == "" {
		dir = "/"
	}
	fmt.Fprintln(ec.Stdout, dir)
	return nil
}

var Pwd command.Command = pwdCmd{}
