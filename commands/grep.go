package commands

import (
	"bytes"
	"context"
	"fmt"
	iofs "io/fs"
	"path"
	"regexp"
	"strings"

	"github.com/cordon-dev/cordon/command"
)

type grepCmd struct{}

func (grepCmd) Name() string { return "grep" }

type grepOpts struct {
	re        *regexp.Regexp
	invert    bool
	showNum   bool
	countOnly bool
	onlyMatch bool
	filesOnly bool
	quiet     bool
	withName  bool
}

func (grepCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "invcrRFEwolq", "")
	if err != nil {
		return ec.Fail(2, "grep: %v\n", err)
	}
	if len(o.args) == 0 {
		return ec.Fail(2, "grep: missing pattern\n")
	}
	pattern := o.args[0]
	files := o.args[1:]
	recursive := o.set['r'] || o.set['R']

	expr := pattern
	switch {
	case o.set['F']:
		expr = regexp.QuoteMeta(expr)
	case !o.set['E']:
		converted, berr := breToRE2(expr)
		if berr != nil {
			return ec.Fail(2, "grep: %v\n", berr)
		}
		expr = converted
	}
	if o.set['w'] {
		expr = `\b(?:` + expr + `)\b`
	}
	if o.set['i'] {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return ec.Fail(2, "grep: %v\n", err)
	}
	g := grepOpts{
		re:        re,
		invert:    o.set['v'],
		showNum:   o.set['n'],
		countOnly: o.set['c'],
		onlyMatch: o.set['o'],
		filesOnly: o.set['l'],
		quiet:     o.set['q'],
	}

	type target struct{ disp, abs string }
	var targets []target
	exit := 1

	if len(files) == 0 && !recursive {
		data, _ := readAll(ec)
		if grepData(ec, g, "", data) {
			exit = 0
		}
		return command.Exit(exit)
	}

	if len(files) == 0 {
		files = []string{"."}
	}

	for _, f := range files {
		abs := ec.Resolve(f)
		info, serr := ec.FS.Stat(abs)
		if serr != nil {
			ec.Errorf("grep: %s: %s\n", f, errMsg(serr))
			exit = 2
			continue
		}
		if info.IsDir() {
			if !recursive {
				ec.Errorf("grep: %s: Is a directory\n", f)
				exit = 2
				continue
			}
			collectFiles(ec, abs, f, func(disp, abs string) {
				targets = append(targets, target{disp, abs})
			})
			continue
		}
		targets = append(targets, target{f, abs})
	}

	g.withName = len(targets) > 1 || recursive
	for _, t := range targets {
		if ec.Ctx != nil && ec.Ctx.Err() != nil {
			return ec.Ctx.Err()
		}
		data, rerr := ec.FS.ReadFile(t.abs)
		if rerr != nil {
			ec.Errorf("grep: %s: %s\n", t.disp, errMsg(rerr))
			exit = 2
			continue
		}
		if grepData(ec, g, t.disp, data) && exit != 2 {
			exit = 0
		}
	}
	return command.Exit(exit)
}

func grepData(ec *command.Context, g grepOpts, name string, data []byte) bool {
	matched := false
	cnt := 0
	lineNo := 0
	for _, raw := range splitLines(data) {
		if ec.Ctx != nil && ec.Ctx.Err() != nil {
			return false
		}
		lineNo++
		if g.re.Match(raw) == g.invert {
			continue
		}
		matched = true
		cnt++
		if g.quiet || g.countOnly {
			continue
		}
		if g.filesOnly {
			fmt.Fprintln(ec.Stdout, name)
			return true
		}
		if g.onlyMatch {
			grepPrintMatches(ec, g, name, lineNo, raw)
			continue
		}
		var b strings.Builder
		grepPrefix(&b, g, name, lineNo)
		b.Write(raw)
		fmt.Fprintln(ec.Stdout, b.String())
	}
	if g.countOnly {
		if g.withName {
			fmt.Fprintf(ec.Stdout, "%s:%d\n", name, cnt)
		} else {
			fmt.Fprintf(ec.Stdout, "%d\n", cnt)
		}
	}
	return matched
}

func grepPrefix(b *strings.Builder, g grepOpts, name string, lineNo int) {
	if g.withName {
		b.WriteString(name)
		b.WriteByte(':')
	}
	if g.showNum {
		fmt.Fprintf(b, "%d:", lineNo)
	}
}

func grepPrintMatches(ec *command.Context, g grepOpts, name string, lineNo int, raw []byte) {
	for _, m := range g.re.FindAll(raw, -1) {
		if len(m) == 0 {
			continue
		}
		var b strings.Builder
		grepPrefix(&b, g, name, lineNo)
		b.Write(m)
		fmt.Fprintln(ec.Stdout, b.String())
	}
}

func splitLines(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	trimmed := bytes.TrimSuffix(data, []byte{'\n'})
	return bytes.Split(trimmed, []byte{'\n'})
}

func collectFiles(ec *command.Context, abs, disp string, fn func(disp, abs string)) {
	entries, err := ec.FS.ReadDir(abs)
	if err != nil {
		return
	}
	for _, e := range entries {
		childAbs := path.Join(abs, e.Name())
		childDisp := joinDisp(disp, e.Name())
		if e.IsDir() {
			collectFiles(ec, childAbs, childDisp, fn)
			continue
		}
		if e.Type().IsRegular() || e.Type() == iofs.FileMode(0) {
			fn(childDisp, childAbs)
		}
	}
}

var Grep command.Command = grepCmd{}
