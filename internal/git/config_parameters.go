package git

import (
	"errors"
	"strings"
)

type configurationParameter struct {
	key   string
	value *string // nil is Git's implicit true, not an explicit empty value.
}

func configurationSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// Git's propagated parameter format is not a general shell program. Only
// single-quoted words and the two escapes emitted by Git's quoting are valid.
func configurationWord(input string) (string, string, bool) {
	var word strings.Builder
	for {
		if len(input) == 0 || input[0] != '\'' {
			return "", "", false
		}
		end := strings.IndexByte(input[1:], '\'')
		if end < 0 {
			return "", "", false
		}
		word.WriteString(input[1 : end+1])
		rest := input[end+2:]
		if len(rest) >= 3 && rest[0] == '\\' && (rest[1] == '\'' || rest[1] == '!') && rest[2] == '\'' {
			word.WriteByte(rest[1])
			input = rest[2:]
			continue
		}
		return word.String(), rest, true
	}
}

func configurationParameters(input string) ([]configurationParameter, error) {
	invalid := errors.New("invalid quoted configuration in GIT_CONFIG_PARAMETERS")
	if strings.ContainsRune(input, 0) {
		return nil, invalid
	}
	var pairs []configurationParameter
	for input != "" {
		key, rest, ok := configurationWord(input)
		if !ok {
			return nil, invalid
		}
		var value *string
		switch {
		case rest == "" || configurationSpace(rest[0]):
			// Older Git versions quoted the entire key=value pair together.
			if name, text, found := strings.Cut(key, "="); found {
				key, value = name, &text
			}
		case rest[0] == '=':
			rest = rest[1:]
			if rest != "" && rest[0] == '\'' {
				text, remaining, ok := configurationWord(rest)
				if !ok || remaining != "" && !configurationSpace(remaining[0]) {
					return nil, invalid
				}
				value, rest = &text, remaining
			} else if rest != "" && !configurationSpace(rest[0]) {
				return nil, invalid
			}
		default:
			return nil, invalid
		}
		pairs = append(pairs, configurationParameter{key, value})
		for rest != "" && configurationSpace(rest[0]) {
			rest = rest[1:]
		}
		input = rest
	}
	return pairs, nil
}
