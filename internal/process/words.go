package process

import (
	"errors"
	"strings"
)

// Split implements shell-like quoting, not shell evaluation. Expansion,
// pipelines, command substitution and redirection are never executed.
func Split(value string) ([]string, error) {
	result := []string{}
	var word strings.Builder
	var quote rune
	started := false
	chars := []rune(value)
	for i := 0; i < len(chars); i++ {
		c := chars[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			continue
		}
		if c == '\\' {
			if i+1 == len(chars) {
				return nil, errors.New("editor command ends with an escape")
			}
			i++
			next := chars[i]
			if quote == '"' && next != '"' && next != '\\' {
				word.WriteRune('\\')
			}
			word.WriteRune(next)
			started = true
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			started = true
			continue
		}
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			if started {
				result = append(result, word.String())
				word.Reset()
				started = false
			}
			continue
		}
		word.WriteRune(c)
		started = true
	}
	if quote != 0 {
		return nil, errors.New("editor command has an unterminated quote")
	}
	if started {
		result = append(result, word.String())
	}
	if len(result) == 0 || result[0] == "" {
		return nil, errors.New("editor command must not be empty")
	}
	return result, nil
}
