package git

import (
	"fmt"
	"regexp"
	"strings"
)

func sparseLiteral(b byte, fold bool) string {
	if fold && b >= 'A' && b <= 'Z' {
		b += 'a' - 'A'
	}
	return regexp.QuoteMeta(string(rune(b)))
}

// Enumerate Git wildmatch's byte alphabet and ASCII POSIX classes instead of
// inheriting RE2's Unicode folding or negated-class slash behavior.
func sparseBracket(pattern string, start int, fold bool) (string, int, bool) {
	i := start + 1
	negate := i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^')
	if negate {
		i++
	}
	var set [256]bool
	previous := byte(0)
	first := true
	for i < len(pattern) {
		b := pattern[i]
		if b == ']' && !first {
			var out strings.Builder
			out.WriteByte('[')
			for n := 1; n < 256; n++ {
				if byte(n) == '/' {
					continue
				}
				matched := set[n]
				if fold && n >= 'a' && n <= 'z' {
					matched = matched || set[n-'a'+'A']
				}
				if matched != negate {
					fmt.Fprintf(&out, "\\x{%x}", n)
				}
			}
			if out.Len() == 1 {
				return "[^\\x00-\\x{10ffff}]", i, true
			}
			out.WriteByte(']')
			return out.String(), i, true
		}
		first = false
		switch {
		case b == '\\':
			i++
			if i == len(pattern) {
				return "", i, false
			}
			b = pattern[i]
			set[b] = true
			previous = b
		case b == '-' && previous != 0 && i+1 < len(pattern) && pattern[i+1] != ']':
			i++
			end := pattern[i]
			if end == '\\' {
				i++
				if i == len(pattern) {
					return "", i, false
				}
				end = pattern[i]
			}
			for n := int(previous); n <= int(end); n++ {
				set[n] = true
			}
			previous = 0
		case b == '[' && i+1 < len(pattern) && pattern[i+1] == ':':
			end := strings.Index(pattern[i+2:], ":]")
			if end < 0 {
				set[b] = true
				previous = b
				break
			}
			name := pattern[i+2 : i+2+end]
			for n := 1; n < 256; n++ {
				v := byte(n)
				alpha := v >= 'a' && v <= 'z' || v >= 'A' && v <= 'Z'
				digit := v >= '0' && v <= '9'
				var match bool
				switch name {
				case "alnum":
					match = alpha || digit
				case "alpha":
					match = alpha
				case "blank":
					match = v == ' ' || v == '\t'
				case "cntrl":
					match = v < 32 || v == 127
				case "digit":
					match = digit
				case "graph":
					match = v > 32 && v < 127
				case "lower":
					match = v >= 'a' && v <= 'z'
				case "print":
					match = v >= 32 && v < 127
				case "punct":
					match = v > 32 && v < 127 && !alpha && !digit
				case "space":
					match = v == ' ' || v >= '\t' && v <= '\r'
				case "upper":
					match = v >= 'A' && v <= 'Z'
				case "xdigit":
					match = digit || v >= 'a' && v <= 'f' || v >= 'A' && v <= 'F'
				default:
					return "", i, false
				}
				set[n] = set[n] || match
			}
			i += end + 3
			previous = 0
		default:
			set[b] = true
			previous = b
		}
		i++
	}
	return "", i, false
}
