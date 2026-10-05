package commands

import (
	"context"
	"errors"
	"fmt"
	stdSort "sort"
	"strconv"
	"strings"

	"github.com/cordon-dev/cordon/command"
)

type sortCmd struct{}

func (sortCmd) Name() string { return "sort" }

type sortKey struct {
	start, end                    int
	numeric, reverse, fold, blank bool
}

func (sortCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "rnufb", "tk")
	if err != nil {
		return ec.Fail(2, "sort: %v\n", err)
	}
	gReverse, gNumeric, gUnique, gFold, gBlank := o.set['r'], o.set['n'], o.set['u'], o.set['f'], o.set['b']
	sep := o.vals['t']

	var key *sortKey
	if kd, ok := o.vals['k']; ok {
		k, kerr := parseKeyDef(kd)
		if kerr != nil {
			return ec.Fail(2, "sort: %v\n", kerr)
		}
		key = &k
	}

	data, exit := gatherInput(ec, "sort", o.args)
	lines := stringLines(data)

	blank := gBlank
	if key != nil {
		blank = blank || key.blank
	}

	keyOf := func(s string) string {
		k := s
		if key != nil {
			k = extractKey(s, sep, key.start, key.end)
		}
		if blank {
			k = strings.TrimLeft(k, " \t")
		}
		return k
	}

	numeric, fold := gNumeric, gFold
	keyReverse := gReverse
	if key != nil {
		numeric = numeric || key.numeric
		fold = fold || key.fold
		keyReverse = gReverse != key.reverse
	}

	cmp := func(a, b string) int {
		c := compareKey(keyOf(a), keyOf(b), numeric, fold)
		if keyReverse {
			c = -c
		}
		if c == 0 {
			c = strings.Compare(a, b)
			if gReverse {
				c = -c
			}
		}
		return c
	}

	stdSort.SliceStable(lines, func(i, j int) bool {
		return cmp(lines[i], lines[j]) < 0
	})

	var out []string
	prevKey := ""
	havePrev := false
	for _, line := range lines {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		k := keyOf(line)
		if gUnique && havePrev && compareKey(prevKey, k, numeric, fold) == 0 {
			continue
		}
		out = append(out, line)
		prevKey, havePrev = k, true
	}
	for _, line := range out {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		fmt.Fprintln(ec.Stdout, line)
	}
	return command.Exit(exit)
}

func extractKey(line, sep string, start, end int) string {
	var fields []string
	joiner := sep
	if sep == "" {
		fields = strings.Fields(line)
		joiner = " "
	} else {
		fields = strings.Split(line, sep)
	}
	if start < 1 {
		start = 1
	}
	lo := start - 1
	if lo >= len(fields) {
		return ""
	}
	hi := len(fields)
	if end > 0 && end < hi {
		hi = end
	}
	if lo >= hi {
		return ""
	}
	return strings.Join(fields[lo:hi], joiner)
}

func compareKey(a, b string, numeric, fold bool) int {
	if numeric {
		na, nb := numericPrefix(a), numericPrefix(b)
		switch {
		case na < nb:
			return -1
		case na > nb:
			return 1
		default:
			return 0
		}
	}
	if fold {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	return strings.Compare(a, b)
}

func parseKeyDef(s string) (sortKey, error) {
	if s == "" {
		return sortKey{}, errors.New("invalid key definition: empty")
	}
	startPart, endPart, hasEnd := strings.Cut(s, ",")
	start, startFlags, err := parseKeyField(startPart)
	if err != nil {
		return sortKey{}, err
	}
	k := sortKey{start: start}
	flags := startFlags
	if hasEnd {
		end, endFlags, eerr := parseKeyField(endPart)
		if eerr != nil {
			return sortKey{}, eerr
		}
		k.end = end
		flags += endFlags
	}
	for i := range len(flags) {
		switch flags[i] {
		case 'n':
			k.numeric = true
		case 'r':
			k.reverse = true
		case 'f':
			k.fold = true
		case 'b':
			k.blank = true
		default:
			return sortKey{}, fmt.Errorf("invalid key option -- '%c'", flags[i])
		}
	}
	return k, nil
}

func parseKeyField(p string) (int, string, error) {
	i := 0
	for i < len(p) && p[i] >= '0' && p[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, "", fmt.Errorf("invalid field number in key: %q", p)
	}
	field, _ := strconv.Atoi(p[:i])
	if field < 1 {
		return 0, "", fmt.Errorf("field number must be at least 1: %q", p)
	}
	rest := p[i:]
	if strings.HasPrefix(rest, ".") {
		j := 1
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		rest = rest[j:]
	}
	return field, rest, nil
}

func numericPrefix(s string) float64 {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) {
		c := s[end]
		if (c == '-' && end == 0) || c == '.' || (c >= '0' && c <= '9') {
			end++
			continue
		}
		break
	}
	v, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
		return 0
	}
	return v
}

var Sort command.Command = sortCmd{}
