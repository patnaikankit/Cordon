package commands

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/cordon-dev/cordon/command"
)

type trCmd struct{}

func (trCmd) Name() string { return "tr" }

func (trCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "dscC", "")
	if err != nil {
		return ec.Fail(2, "tr: %v\n", err)
	}
	del, squeeze, complement := o.set['d'], o.set['s'], o.set['c'] || o.set['C']

	if len(o.args) == 0 || (!del && !squeeze && len(o.args) < 2) {
		return ec.Fail(1, "tr: missing operand\n")
	}
	set1 := expandTrSet(o.args[0])
	var set2 []rune
	if len(o.args) > 1 {
		set2 = expandTrSet(o.args[1])
	}

	data, rerr := readAll(ec)
	if rerr != nil {
		return ec.Fail(1, "tr: %v\n", rerr)
	}
	var b strings.Builder
	var last rune
	haveLast := false
	for _, r := range string(data) {
		out, keep := translateRune(r, set1, set2, del, complement)
		if !keep {
			continue
		}
		if squeeze && haveLast && out == last && inSqueezeSet(out, set1, set2, del, complement) {
			continue
		}
		b.WriteRune(out)
		last, haveLast = out, true
	}
	fmt.Fprint(ec.Stdout, b.String())
	return nil
}

func translateRune(r rune, set1, set2 []rune, del, complement bool) (rune, bool) {
	idx := indexOf(set1, r)
	matched := (idx >= 0) != complement
	if del {
		return r, !matched
	}
	if !matched || len(set2) == 0 {
		return r, true
	}
	if complement || idx >= len(set2) {
		return set2[len(set2)-1], true
	}
	return set2[idx], true
}

func inSqueezeSet(out rune, set1, set2 []rune, del, complement bool) bool {
	if del || len(set2) == 0 {
		return inSet(out, set1) != complement
	}
	return inSet(out, set2)
}

func expandTrSet(s string) []rune {
	rs := unescapeTr(s)
	var out []rune
	for i := 0; i < len(rs); i++ {
		if rs[i] == '[' && i+1 < len(rs) && rs[i+1] == ':' {
			if end := classClose(rs, i+2); end >= 0 {
				if members, ok := trClass(string(rs[i+2 : end])); ok {
					out = append(out, members...)
					i = end + 1
					continue
				}
			}
		}
		if i+2 < len(rs) && rs[i+1] == '-' && rs[i] <= rs[i+2] {
			for c := rs[i]; c <= rs[i+2]; c++ {
				out = append(out, c)
			}
			i += 2
			continue
		}
		out = append(out, rs[i])
	}
	return out
}

func unescapeTr(s string) []rune {
	rs := []rune(s)
	var out []rune
	for i := 0; i < len(rs); i++ {
		if rs[i] != '\\' || i+1 >= len(rs) {
			out = append(out, rs[i])
			continue
		}
		i++
		switch c := rs[i]; c {
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case 'r':
			out = append(out, '\r')
		case 'a':
			out = append(out, '\a')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'v':
			out = append(out, '\v')
		case '\\':
			out = append(out, '\\')
		case '0', '1', '2', '3', '4', '5', '6', '7':
			val, n := 0, 0
			for n < 3 && i < len(rs) && rs[i] >= '0' && rs[i] <= '7' {
				val = val*8 + int(rs[i]-'0')
				i++
				n++
			}
			i--
			out = append(out, rune(val))
		default:
			out = append(out, c)
		}
	}
	return out
}

func classClose(rs []rune, from int) int {
	for j := from; j+1 < len(rs); j++ {
		if rs[j] == ':' && rs[j+1] == ']' {
			return j
		}
	}
	return -1
}

func trClass(name string) ([]rune, bool) {
	var pred func(rune) bool
	switch name {
	case "alpha":
		pred = unicode.IsLetter
	case "digit":
		pred = unicode.IsDigit
	case "alnum":
		pred = func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	case "upper":
		pred = unicode.IsUpper
	case "lower":
		pred = unicode.IsLower
	case "space":
		pred = unicode.IsSpace
	case "blank":
		pred = func(r rune) bool { return r == ' ' || r == '\t' }
	case "punct":
		pred = unicode.IsPunct
	case "cntrl":
		pred = unicode.IsControl
	case "xdigit":
		pred = func(r rune) bool { return strings.ContainsRune("0123456789abcdefABCDEF", r) }
	default:
		return nil, false
	}
	var out []rune
	for r := range rune(128) {
		if pred(r) {
			out = append(out, r)
		}
	}
	return out, true
}

func indexOf(set []rune, r rune) int {
	for i, c := range set {
		if c == r {
			return i
		}
	}
	return -1
}

func inSet(r rune, set []rune) bool {
	return indexOf(set, r) >= 0
}

var Tr command.Command = trCmd{}
