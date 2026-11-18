package git

import (
	"fmt"
	"regexp"
	"strings"
)

// Sparse patterns use gitignore syntax with positive matches meaning inclusion.
// Unlike an ignore traversal, an included parent does not prevent a later rule
// from excluding a child. Match each ancestor, carrying its last decision down.
type sparseRule struct {
	match      *regexp.Regexp
	include    bool
	directory  bool
	ignoreCase bool
}

type sparsePatterns []sparseRule

func sparseFoldASCII(value string) string {
	data := []byte(value)
	for i, b := range data {
		if b >= 'A' && b <= 'Z' {
			data[i] += 'a' - 'A'
		}
	}
	return string(data)
}

func parseSparsePatterns(data string, ignoreCase bool) (sparsePatterns, error) {
	var rules sparsePatterns
	for number, line := range strings.Split(data, "\n") {
		line = strings.TrimSuffix(line, "\r")
		for strings.HasSuffix(line, " ") {
			n := len(line) - 2
			for n >= 0 && line[n] == '\\' {
				n--
			}
			if (len(line)-2-n)%2 != 0 {
				break
			}
			line = strings.TrimSuffix(line, " ")
		}
		if line == "" || line[0] == '#' {
			continue
		}
		include := true
		if line[0] == '!' {
			include = false
			line = line[1:]
		}
		if line == "" {
			continue
		}
		directory := strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		anchored := strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		var pattern strings.Builder
		pattern.WriteString("(?s)")

		if anchored {
			pattern.WriteString("^")
		} else {
			pattern.WriteString("(^|/)")
		}
		for i := 0; i < len(line); i++ {
			switch line[i] {
			case '\\':
				i++
				if i == len(line) {
					return nil, fmt.Errorf("sparse-checkout line %d: trailing escape", number+1)
				}
				pattern.WriteString(sparseLiteral(line[i], ignoreCase))
			case '*':
				if i+1 < len(line) && line[i+1] == '*' && (i == 0 || line[i-1] == '/') && (i+2 == len(line) || line[i+2] == '/') {
					i++
					if i+1 < len(line) {
						pattern.WriteString("(.*/)?")
						i++
					} else {
						pattern.WriteString(".*")
					}
				} else {
					pattern.WriteString("[^/]*")
				}
			case '?':
				pattern.WriteString("[^/]")
			case '[':
				class, end, ok := sparseBracket(line, i, ignoreCase)
				if !ok {
					pattern.WriteString("[^\\x00-\\x{10ffff}]")
					i = len(line)
					break
				}
				pattern.WriteString(class)
				i = end
			default:
				pattern.WriteString(sparseLiteral(line[i], ignoreCase))
			}
		}
		pattern.WriteString("$")
		compiled, err := regexp.Compile(pattern.String())
		if err != nil {
			return nil, fmt.Errorf("sparse-checkout line %d: %w", number+1, err)
		}
		rules = append(rules, sparseRule{compiled, include, directory, ignoreCase})
	}
	return rules, nil
}

func (rules sparsePatterns) includes(path string) bool {
	included := false
	parts := strings.Split(path, "/")
	for i := range parts {
		prefix := strings.Join(parts[:i+1], "/")
		directory := i < len(parts)-1
		for _, rule := range rules {
			candidate := prefix
			if rule.ignoreCase {
				candidate = sparseFoldASCII(candidate)
			}
			// Git wildmatch operates on bytes, not Unicode code points.
			bytes := make([]rune, len(candidate))
			for j := range len(candidate) {
				bytes[j] = rune(candidate[j])
			}
			if (!rule.directory || directory) && rule.match.MatchString(string(bytes)) {
				included = rule.include
			}
		}
	}
	return included
}
