package store

import "strings"

// Recognize complete Git conflict groups, including diff3 and custom marker
// widths. Fenced Markdown examples are literal text, not unresolved metadata.
func hasMergeConflict(content string) bool {
	var fence byte
	fenceWidth, markerWidth, state := 0, 0, 0
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if state == 0 {
			trimmed := strings.TrimLeft(line, " ")
			if len(line)-len(trimmed) <= 3 && len(trimmed) > 0 && (trimmed[0] == '`' || trimmed[0] == '~') {
				width := markerRun(trimmed)
				if fence != 0 {
					if trimmed[0] == fence && width >= fenceWidth && strings.TrimSpace(trimmed[width:]) == "" {
						fence = 0
					}
				} else if width >= 3 && (trimmed[0] != '`' || !strings.Contains(trimmed[width:], "`")) {
					fence, fenceWidth = trimmed[0], width
				}
				continue
			}
			if fence != 0 {
				continue
			}
		}
		if len(line) < 7 {
			continue
		}
		width := markerRun(line)
		if width < 7 || (width < len(line) && line[width] != ' ') {
			continue
		}
		switch {
		case state == 0 && line[0] == '<':
			markerWidth, state = width, 1
		case state == 1 && line[0] == '=' && width == markerWidth && width == len(line):
			state = 2
		case state == 2 && line[0] == '>' && width == markerWidth:
			return true
		}
	}
	return false
}

func markerRun(line string) int {
	width := 1
	for width < len(line) && line[width] == line[0] {
		width++
	}
	return width
}
