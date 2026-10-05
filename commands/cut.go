package commands

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cordon-dev/cordon/command"
)

type cutCmd struct{}

func (cutCmd) Name() string { return "cut" }

func (cutCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "s", "dfc")
	if err != nil {
		return ec.Fail(2, "cut: %v\n", err)
	}
	fieldSpec, byField := o.vals['f']
	charSpec, byChar := o.vals['c']
	if byField == byChar {
		return ec.Fail(2, "cut: you must specify exactly one of -f or -c\n")
	}
	onlyDelim := o.set['s']
	delim := "\t"
	if d, ok := o.vals['d']; ok {
		if utf8.RuneCountInString(d) != 1 {
			return ec.Fail(2, "cut: the delimiter must be a single character\n")
		}
		if byChar {
			return ec.Fail(2, "cut: an input delimiter may be specified only when operating on fields\n")
		}
		delim = d
	}
	if onlyDelim && byChar {
		return ec.Fail(2, "cut: suppressing non-delimited lines makes sense only when operating on fields\n")
	}

	spec := fieldSpec
	if byChar {
		spec = charSpec
	}
	ranges, perr := parseRanges(spec)
	if perr != nil {
		return ec.Fail(2, "cut: invalid list: '%s'\n", spec)
	}

	data, exit := gatherInput(ec, "cut", o.args)
	for _, line := range stringLines(data) {
		if byChar {
			ec.Stdout.Write([]byte(selectItems(strings.Split(line, ""), ranges, "") + "\n"))
			continue
		}
		if !strings.Contains(line, delim) {
			if !onlyDelim {
				ec.Stdout.Write([]byte(line + "\n"))
			}
			continue
		}
		ec.Stdout.Write([]byte(selectItems(strings.Split(line, delim), ranges, delim) + "\n"))
	}
	return command.Exit(exit)
}

type itemRange struct {
	lo, hi int
}

func parseRanges(spec string) ([]itemRange, error) {
	var ranges []itemRange
	for part := range strings.SplitSeq(spec, ",") {
		lo, hi, err := parseOneRange(part)
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, itemRange{lo, hi})
	}
	return ranges, nil
}

func parseOneRange(part string) (int, int, error) {
	before, after, hasDash := strings.Cut(part, "-")
	if !hasDash {
		n, err := strconv.Atoi(part)
		return n, n, err
	}
	lo := 1
	if before != "" {
		v, err := strconv.Atoi(before)
		if err != nil {
			return 0, 0, err
		}
		lo = v
	}
	hi := 0
	if after != "" {
		v, err := strconv.Atoi(after)
		if err != nil {
			return 0, 0, err
		}
		hi = v
	}
	return lo, hi, nil
}

func selectItems(items []string, ranges []itemRange, sep string) string {
	var out []string
	for i, item := range items {
		idx := i + 1
		for _, r := range ranges {
			if idx >= r.lo && (r.hi == 0 || idx <= r.hi) {
				out = append(out, item)
				break
			}
		}
	}
	return strings.Join(out, sep)
}

var Cut command.Command = cutCmd{}
