package commands

import (
	"context"
	"fmt"
	iofs "io/fs"
	"path"
	"sort"
	"strings"

	"github.com/cordon-dev/cordon/command"
)

type lsCmd struct{}

func (lsCmd) Name() string { return "ls" }

func (lsCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "alR1", "")
	if err != nil {
		return ec.Fail(2, "ls: %v\n", err)
	}
	all, long, recursive := o.set['a'], o.set['l'], o.set['R']

	operands := o.args
	if len(operands) == 0 {
		operands = []string{"."}
	}

	var files, dirs []string
	exit := 0
	for _, p := range operands {
		info, err := ec.FS.Stat(ec.Resolve(p))
		if err != nil {
			ec.Errorf("ls: cannot access '%s': %s\n", p, errMsg(err))
			exit = 2
			continue
		}
		if info.IsDir() {
			dirs = append(dirs, p)
		} else {
			files = append(files, p)
		}
	}

	sort.Strings(files)
	for _, f := range files {
		info, _ := ec.FS.Stat(ec.Resolve(f))
		lsPrint(ec, long, f, info)
	}

	header := recursive || len(dirs) > 1 || (len(files) > 0 && len(dirs) > 0)
	for idx, d := range dirs {
		if len(files) > 0 || idx > 0 {
			fmt.Fprintln(ec.Stdout)
		}
		if err := lsDir(ec, ec.Resolve(d), d, all, long, recursive, header); err != nil {
			exit = 2
		}
	}
	return command.Exit(exit)
}

func lsDir(ec *command.Context, abs, disp string, all, long, recursive, header bool) error {
	entries, err := ec.FS.ReadDir(abs)
	if err != nil {
		ec.Errorf("ls: cannot open directory '%s': %s\n", disp, errMsg(err))
		return err
	}
	if header {
		fmt.Fprintf(ec.Stdout, "%s:\n", disp)
	}
	if all {
		fmt.Fprintln(ec.Stdout, ".")
		fmt.Fprintln(ec.Stdout, "..")
	}
	for _, e := range entries {
		if !all && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, _ := e.Info()
		lsPrint(ec, long, e.Name(), info)
	}
	if recursive {
		for _, e := range entries {
			if !e.IsDir() || (!all && strings.HasPrefix(e.Name(), ".")) {
				continue
			}
			fmt.Fprintln(ec.Stdout)
			_ = lsDir(ec, path.Join(abs, e.Name()), path.Join(disp, e.Name()), all, long, recursive, true)
		}
	}
	return nil
}

func lsPrint(ec *command.Context, long bool, name string, info iofs.FileInfo) {
	if !long {
		fmt.Fprintln(ec.Stdout, name)
		return
	}
	size := int64(0)
	mode := "----------"
	if info != nil {
		size = info.Size()
		mode = info.Mode().String()
	}
	fmt.Fprintf(ec.Stdout, "%s %d %s\n", mode, size, name)
}

var Ls command.Command = lsCmd{}
