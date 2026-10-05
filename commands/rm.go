package commands

import (
	"context"

	"github.com/cordon-dev/cordon/command"
)

type rmCmd struct{}

func (rmCmd) Name() string { return "rm" }

func (rmCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "rRf", "")
	if err != nil {
		return ec.Fail(2, "rm: %v\n", err)
	}
	recursive := o.set['r'] || o.set['R']
	force := o.set['f']

	if len(o.args) == 0 {
		if force {
			return nil
		}
		return ec.Fail(1, "rm: missing operand\n")
	}

	exit := 0
	for _, name := range o.args {
		abs := ec.Resolve(name)
		info, serr := ec.FS.Stat(abs)
		if serr != nil {
			if !force {
				ec.Errorf("rm: cannot remove '%s': %s\n", name, errMsg(serr))
				exit = 1
			}
			continue
		}
		if info.IsDir() && !recursive {
			ec.Errorf("rm: cannot remove '%s': Is a directory\n", name)
			exit = 1
			continue
		}
		var derr error
		if recursive {
			derr = ec.FS.RemoveAll(abs)
		} else {
			derr = ec.FS.Remove(abs)
		}
		if derr != nil && !force {
			ec.Errorf("rm: cannot remove '%s': %s\n", name, errMsg(derr))
			exit = 1
		}
	}
	return command.Exit(exit)
}

var Rm command.Command = rmCmd{}
