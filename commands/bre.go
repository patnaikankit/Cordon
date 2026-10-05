package commands

import (
	"errors"
	"strings"
)

// breToRE2 translates a POSIX/GNU basic regular expression into equivalent Go RE2 syntax.
func breToRE2(bre string) (string, error) {
	var b strings.Builder
	n := len(bre)
	exprStart, afterCaret := true, false
	for i := 0; i < n; {
		wasExprStart := exprStart
		starLiteral := exprStart || afterCaret
		exprStart, afterCaret = false, false
		switch c := bre[i]; c {
		case '\\':
			nextI, opensSub, err := breEscape(&b, bre, i, starLiteral)
			if err != nil {
				return "", err
			}
			i, exprStart = nextI, opensSub
		case '(', ')', '{', '}', '+', '?', '|':
			b.WriteByte('\\')
			b.WriteByte(c)
			i++
		case '[':
			j := bracketEnd(bre, i)
			writeBracketRE2(&b, bre[i:j])
			i = j
		case '^':
			if wasExprStart {
				b.WriteByte('^')
				afterCaret = true
			} else {
				b.WriteString(`\^`)
			}
			i++
		case '$':
			if breDollarIsAnchor(bre, i) {
				b.WriteByte('$')
			} else {
				b.WriteString(`\$`)
			}
			i++
		case '*':
			if starLiteral {
				b.WriteString(`\*`)
			} else {
				b.WriteByte('*')
			}
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), nil
}

func breEscape(b *strings.Builder, bre string, i int, starLiteral bool) (int, bool, error) {
	if i+1 >= len(bre) {
		b.WriteByte('\\')
		return i + 1, false, nil
	}
	next := bre[i+1]
	i += 2
	switch next {
	case '(', '|':
		b.WriteByte(next)
		return i, true, nil
	case ')', '}':
		b.WriteByte(next)
	case '{':
		body, end, kind := breIntervalScan(bre, i)
		switch kind {
		case intervalError:
			return i, false, errors.New(`unmatched \{`)
		case intervalLiteral:
			b.WriteString(`\{`)
		default:
			writeInterval(b, body, starLiteral)
			i = end
		}
	case '+', '?':
		if starLiteral {
			b.WriteByte('\\')
		}
		b.WriteByte(next)
	case '<', '>':
		b.WriteString(`\b`)
	default:
		b.WriteByte('\\')
		b.WriteByte(next)
	}
	return i, false, nil
}

const (
	intervalOK = iota
	intervalLiteral
	intervalError
)

func breIntervalScan(bre string, i int) (string, int, int) {
	j := i
	for j < len(bre) && (bre[j] == ',' || (bre[j] >= '0' && bre[j] <= '9')) {
		j++
	}
	if j+1 < len(bre) && bre[j] == '\\' && bre[j+1] == '}' {
		return bre[i:j], j + 2, intervalOK
	}
	if j == i && i < len(bre) {
		return "", i, intervalLiteral
	}
	return "", i, intervalError
}

func writeInterval(b *strings.Builder, body string, starLiteral bool) {
	if starLiteral {
		b.WriteString(`\{`)
		b.WriteString(body)
		b.WriteString(`\}`)
		return
	}
	if strings.HasPrefix(body, ",") {
		body = "0" + body
	}
	b.WriteByte('{')
	b.WriteString(body)
	b.WriteByte('}')
}

func writeBracketRE2(b *strings.Builder, expr string) {
	for i := range len(expr) {
		if expr[i] == '\\' {
			b.WriteString(`\\`)
		} else {
			b.WriteByte(expr[i])
		}
	}
}

func breDollarIsAnchor(bre string, i int) bool {
	rest := bre[i+1:]
	return rest == "" || strings.HasPrefix(rest, `\)`) || strings.HasPrefix(rest, `\|`)
}

func bracketEnd(bre string, i int) int {
	n := len(bre)
	j := i + 1
	if j < n && bre[j] == '^' {
		j++
	}
	if j < n && bre[j] == ']' {
		j++
	}
	for j < n {
		if bre[j] == '[' && j+1 < n && (bre[j+1] == ':' || bre[j+1] == '.' || bre[j+1] == '=') {
			closer := bre[j+1]
			j += 2
			for j+1 < n && (bre[j] != closer || bre[j+1] != ']') {
				j++
			}
			j += 2
			continue
		}
		if bre[j] == ']' {
			return j + 1
		}
		j++
	}
	return n
}
