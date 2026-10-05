package commands

import (
	"fmt"
	"strings"
)

// opts is the result of parsing short Unix-style options.
type opts struct {
	set  map[byte]bool
	vals map[byte]string
	args []string
}

// parseOpts parses short options. Letters in bools are boolean flags;
// letters in valFlags take an argument (attached like -n5 or next word -n 5).
// Unknown flags return an error: "invalid option -- '%c'".
func parseOpts(args []string, bools, valFlags string) (opts, error) {
	o := opts{set: make(map[byte]bool), vals: make(map[byte]string)}
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if len(a) < 2 || a[0] != '-' {
			break
		}
		j := 1
		for j < len(a) {
			c := a[j]
			switch {
			case strings.IndexByte(valFlags, c) >= 0:
				val := a[j+1:]
				if val == "" {
					i++
					if i >= len(args) {
						return o, fmt.Errorf("option requires an argument -- '%c'", c)
					}
					val = args[i]
				}
				o.vals[c] = val
				j = len(a)
			case strings.IndexByte(bools, c) >= 0:
				o.set[c] = true
				j++
			default:
				return o, fmt.Errorf("invalid option -- '%c'", c)
			}
		}
	}
	o.args = args[i:]
	return o, nil
}
