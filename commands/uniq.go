package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cordon-dev/cordon/command"
)

type uniqCmd struct{}

func (uniqCmd) Name() string { return "uniq" }

func (uniqCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "cdui", "fsw")
	if err != nil {
		return ec.Fail(2, "uniq: %v\n", err)
	}
	count, onlyDup, onlyUniq, ignore := o.set['c'], o.set['d'], o.set['u'], o.set['i']
	skipFields, err := uniqIntOpt(o, 'f')
	if err != nil {
		return ec.Fail(2, "uniq: invalid number of fields to skip\n")
	}
	skipChars, err := uniqIntOpt(o, 's')
	if err != nil {
		return ec.Fail(2, "uniq: invalid number of bytes to skip\n")
	}
	_, hasW := o.vals['w']
	checkN, err := uniqIntOpt(o, 'w')
	if err != nil {
		return ec.Fail(2, "uniq: invalid number of bytes to compare\n")
	}

	data, exit := gatherInput(ec, "uniq", o.args)
	lines := stringLines(data)

	key := func(s string) string {
		return uniqKey(s, skipFields, skipChars, checkN, hasW, ignore)
	}

	emit := func(line string, n int) {
		if onlyDup && n < 2 {
			return
		}
		if onlyUniq && n > 1 {
			return
		}
		if count {
			fmt.Fprintf(ec.Stdout, "%7d %s\n", n, line)
			return
		}
		fmt.Fprintln(ec.Stdout, line)
	}

	var cur string
	n := 0
	for _, line := range lines {
		switch {
		case n == 0:
			cur, n = line, 1
		case key(line) == key(cur):
			n++
		default:
			emit(cur, n)
			cur, n = line, 1
		}
	}
	if n > 0 {
		emit(cur, n)
	}
	return command.Exit(exit)
}

func uniqIntOpt(o opts, flag byte) (int, error) {
	v, ok := o.vals[flag]
	if !ok {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, strconv.ErrSyntax
	}
	return n, nil
}

func uniqKey(s string, skipFields, skipChars, checkN int, hasW, ignore bool) string {
	k := s
	for range skipFields {
		k = strings.TrimLeft(k, " \t")
		i := strings.IndexAny(k, " \t")
		if i < 0 {
			k = ""
			break
		}
		k = k[i:]
	}
	if skipChars > 0 {
		if skipChars >= len(k) {
			k = ""
		} else {
			k = k[skipChars:]
		}
	}
	if hasW && checkN < len(k) {
		k = k[:checkN]
	}
	if ignore {
		k = strings.ToLower(k)
	}
	return k
}

var Uniq command.Command = uniqCmd{}
