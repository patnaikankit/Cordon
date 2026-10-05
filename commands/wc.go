package commands

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cordon-dev/cordon/command"
)

type wcCmd struct{}

func (wcCmd) Name() string { return "wc" }

type wcCounts struct {
	lines, words, chars, bytes int
}

func (c *wcCounts) add(o wcCounts) {
	c.lines += o.lines
	c.words += o.words
	c.chars += o.chars
	c.bytes += o.bytes
}

func (wcCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "lwcm", "")
	if err != nil {
		return ec.Fail(2, "wc: %v\n", err)
	}
	showL, showW, showM, showC := o.set['l'], o.set['w'], o.set['m'], o.set['c']
	if !showL && !showW && !showM && !showC {
		showL, showW, showC = true, true, true
	}

	format := func(name string, n wcCounts) string {
		var parts []string
		for _, sel := range []struct {
			show bool
			val  int
		}{
			{showL, n.lines},
			{showW, n.words},
			{showM, n.chars},
			{showC, n.bytes},
		} {
			if sel.show {
				parts = append(parts, strconv.Itoa(sel.val))
			}
		}
		s := strings.Join(parts, " ")
		if name != "" {
			s += " " + name
		}
		return s
	}

	count := func(data []byte) wcCounts {
		return wcCounts{
			lines: bytes.Count(data, []byte{'\n'}),
			words: len(bytes.Fields(data)),
			chars: utf8.RuneCount(data),
			bytes: len(data),
		}
	}

	if len(o.args) == 0 {
		data, err := readAll(ec)
		if err != nil {
			return ec.Fail(1, "wc: %v\n", err)
		}
		fmt.Fprintln(ec.Stdout, format("", count(data)))
		return nil
	}

	var total wcCounts
	exit := 0
	for _, name := range o.args {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		var (
			data []byte
			err  error
		)
		if name == "-" {
			data, err = readAll(ec)
		} else {
			data, err = ec.FS.ReadFile(ec.Resolve(name))
		}
		if err != nil {
			ec.Errorf("wc: %s: %s\n", name, errMsg(err))
			exit = 1
			continue
		}
		c := count(data)
		total.add(c)
		fmt.Fprintln(ec.Stdout, format(name, c))
	}
	if len(o.args) > 1 {
		fmt.Fprintln(ec.Stdout, format("total", total))
	}
	return command.Exit(exit)
}

var Wc command.Command = wcCmd{}
