package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/cordon-dev/cordon/command"
)

type headCmd struct{}

func (headCmd) Name() string { return "head" }

func (headCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(normalizeCountArgs(ec.Args[1:]), "", "nc")
	if err != nil {
		return ec.Fail(2, "head: %v\n", err)
	}
	n, sign, byteMode, emsg := headTailCount(o)
	if emsg != "" {
		return ec.Fail(2, "head: %s\n", emsg)
	}
	allButLast := sign == '-'

	emit := func(r io.Reader) error {
		if byteMode {
			return headEmitBytes(ec.Stdout, r, n, allButLast)
		}
		return headEmit(ec.Stdout, r, n, allButLast)
	}

	if len(o.args) == 0 {
		if err := emit(ec.StdinReader()); err != nil {
			return ec.Fail(1, "head: %v\n", err)
		}
		return nil
	}

	exit := 0
	multi := len(o.args) > 1
	for i, name := range o.args {
		f, err := ec.FS.OpenFile(ec.Resolve(name), os.O_RDONLY, 0)
		if err != nil {
			ec.Errorf("head: cannot open '%s' for reading: %s\n", name, errMsg(err))
			exit = 1
			continue
		}
		if multi {
			if i > 0 {
				fmt.Fprintln(ec.Stdout)
			}
			fmt.Fprintf(ec.Stdout, "==> %s <==\n", name)
		}
		emitErr := emit(f)
		_ = f.Close()
		if emitErr != nil {
			ec.Errorf("head: %v\n", emitErr)
			exit = 1
		}
	}
	return command.Exit(exit)
}

func headEmit(w io.Writer, r io.Reader, n int, allButLast bool) error {
	sc := newLineScanner(r)
	if allButLast {
		var lines []string
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		if err := sc.Err(); err != nil {
			return err
		}
		limit := max(len(lines)-n, 0)
		for _, l := range lines[:limit] {
			fmt.Fprintln(w, l)
		}
		return nil
	}
	for count := 0; count < n && sc.Scan(); count++ {
		fmt.Fprintln(w, sc.Text())
	}
	return sc.Err()
}

func headEmitBytes(w io.Writer, r io.Reader, n int, allButLast bool) error {
	if allButLast {
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		_, err = w.Write(data[:max(len(data)-n, 0)])
		return err
	}
	_, err := io.CopyN(w, r, int64(n))
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func headTailCount(o opts) (int, byte, bool, string) {
	if v, ok := o.vals['c']; ok {
		num, sign, err := parseLineCount(v)
		if err != nil {
			return 0, 0, true, "invalid number of bytes: '" + v + "'"
		}
		return num, sign, true, ""
	}
	if v, ok := o.vals['n']; ok {
		num, sign, err := parseLineCount(v)
		if err != nil {
			return 0, 0, false, "invalid number of lines: '" + v + "'"
		}
		return num, sign, false, ""
	}
	return 10, 0, false, ""
}

func normalizeCountArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if (a == "-n" || a == "-c") && i+1 < len(args) {
			out = append(out, a, args[i+1])
			i++
			continue
		}
		if len(a) > 1 && a[0] == '-' && isAllDigits(a[1:]) {
			out = append(out, "-n", a[1:])
			continue
		}
		out = append(out, a)
	}
	return out
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func parseLineCount(v string) (int, byte, error) {
	sign := byte(0)
	if len(v) > 0 && (v[0] == '+' || v[0] == '-') {
		sign = v[0]
		v = v[1:]
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, 0, strconv.ErrSyntax
	}
	return n, sign, nil
}

var Head command.Command = headCmd{}
