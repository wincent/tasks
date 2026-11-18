package terminal

import (
	"strings"
	"unicode"
)

// Plain removes terminal controls, including OSC links/titles and incomplete
// escape sequences. Newlines and tabs remain usable in redirected diagnostics.
func Plain(text string) string {
	var out strings.Builder
	state := byte(0)
	for _, r := range strings.ToValidUTF8(text, "�") {
		switch state {
		case 1:
			switch r {
			case '[':
				state = 2
			case ']', 'P', '^', '_', 'X':
				state = 3
			default:
				state = 0
			}
			continue
		case 2:
			if r >= 0x40 && r <= 0x7e {
				state = 0
			}
			continue
		case 3:
			if r == 7 {
				state = 0
			} else if r == 27 {
				state = 4
			}
			continue
		case 4:
			if r == '\\' {
				state = 0
			} else {
				state = 3
			}
			continue
		}
		if r == 27 {
			state = 1
			continue
		}
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}
