package cli

import "fmt"

type commandCorrection struct {
	from, to string
}

func (c commandCorrection) suggestion() error {
	return fmt.Errorf("subcommand `%s` does not exist; did you mean `%s`?", c.from, c.to)
}

func allowCorrection(arguments) bool { return true }

// Only consider a unique match explainable by one typing mistake. This
// includes shifts such as "lsti" and short transpositions such as "og".
func (r commandRegistry) closest(name string) (commandSpec, bool) {
	if name == "" {
		return commandSpec{}, false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return commandSpec{}, false
		}
	}
	var match commandSpec
	for _, spec := range r {
		if spec.hidden || spec.alias || !oneTypoApart(name, spec.name) {
			continue
		}
		if match.name != "" {
			return commandSpec{}, false
		}
		match = spec
	}
	return match, match.name != ""
}

// Command names are ASCII. Allow one insertion, deletion, replacement, or
// a letter typed in the wrong position (including adjacent transpositions).
func oneTypoApart(a, b string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(b)-len(a) > 1 {
		return false
	}
	i := 0
	for i < len(a) && a[i] == b[i] {
		i++
	}
	if len(a) != len(b) {
		return a[i:] == b[i+1:]
	}
	if i == len(a) {
		return false
	}
	j := len(a) - 1
	for a[j] == b[j] {
		j--
	}
	return i == j ||
		a[i] == b[j] && a[i+1:j+1] == b[i:j] ||
		b[i] == a[j] && b[i+1:j+1] == a[i:j]
}
