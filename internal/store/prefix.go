package store

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wincent/tasks/internal/git"
)

const DefaultTargetBranch = "main"

const configSetup = "commit a root .tasks.yml to " + Branch + " with 'prefix: YOURPREFIX' (uppercase ASCII letters, optionally followed by digits or more letters) and optional 'default_target_branch: BRANCH'"

// Prefix is the configured repository-wide ticket key, without the dash.
type Prefix string

// Config holds repository-level settings from .tasks.yml on the tasks branch.
type Config struct {
	Prefix              Prefix
	DefaultTargetBranch string
}

var prefixValue = regexp.MustCompile(`^[A-Z][A-Z0-9]*$`)

// ValidTargetBranch accepts local implementation branch names allowed as
// default_target_branch (matching task topic rules, excluding reserved task/).
func ValidTargetBranch(name string) bool {
	return ValidTopic(name) && !strings.HasPrefix(strings.ToLower(name), "task/")
}

// This strict two-key YAML format supports plain and quoted keys/values, blank
// lines, and comments without needing a YAML dependency or accepting unknown
// settings.
func parseConfig(data []byte) (Config, error) {
	if !utf8.Valid(data) {
		return Config{}, fmt.Errorf("invalid .tasks.yml: file must be UTF-8; %s", configSetup)
	}
	cfg := Config{DefaultTargetBranch: DefaultTargetBranch}
	var seenPrefix, seenTarget bool
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, err := parseConfigLine(line)
		if err != nil {
			return Config{}, fmt.Errorf("invalid .tasks.yml at line %d: %v; %s", i+1, err, configSetup)
		}
		switch key {
		case "prefix":
			if seenPrefix {
				return Config{}, fmt.Errorf("invalid .tasks.yml at line %d: duplicate key %q; %s", i+1, key, configSetup)
			}
			if !prefixValue.MatchString(value) {
				return Config{}, fmt.Errorf("invalid .tasks.yml at line %d: invalid prefix %q; %s", i+1, value, configSetup)
			}
			cfg.Prefix = Prefix(value)
			seenPrefix = true
		case "default_target_branch":
			if seenTarget {
				return Config{}, fmt.Errorf("invalid .tasks.yml at line %d: duplicate key %q; %s", i+1, key, configSetup)
			}
			if !ValidTargetBranch(value) {
				return Config{}, fmt.Errorf("invalid .tasks.yml at line %d: invalid default_target_branch %q; %s", i+1, value, configSetup)
			}
			cfg.DefaultTargetBranch = value
			seenTarget = true
		default:
			return Config{}, fmt.Errorf("invalid .tasks.yml at line %d: unknown key %q; %s", i+1, key, configSetup)
		}
	}
	if !seenPrefix {
		return Config{}, fmt.Errorf("missing prefix in .tasks.yml; %s", configSetup)
	}
	return cfg, nil
}

func parseConfigLine(line string) (string, string, error) {
	if line[0] == ' ' || line[0] == '\t' {
		return "", "", errors.New("unexpected indentation")
	}
	var key, rest string
	var err error
	switch line[0] {
	case '\'':
		key, rest, err = parseConfigSingleQuoted(line)
		if err != nil {
			return "", "", err
		}
	case '"':
		key, rest, err = parseConfigDoubleQuoted(line)
		if err != nil {
			return "", "", err
		}
	default:
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return "", "", errors.New("expected 'key: value'")
		}
		key, rest = line[:colon], line[colon:]
		if strings.ContainsAny(key, " \t") {
			return "", "", errors.New("invalid unquoted key")
		}
	}
	if !strings.HasPrefix(rest, ":") {
		return "", "", errors.New("expected ':' after key")
	}
	rest = rest[1:]
	if len(rest) == 0 || (rest[0] != ' ' && rest[0] != '\t') {
		return "", "", errors.New("expected whitespace after ':'")
	}
	valPart := strings.TrimLeft(rest, " \t")
	if valPart == "" || valPart[0] == '#' {
		return "", "", errors.New("missing value")
	}
	value, err := parseConfigValue(valPart)
	if err != nil {
		return "", "", err
	}
	return key, value, nil
}

func parseConfigValue(s string) (string, error) {
	switch s[0] {
	case '\'':
		val, tail, err := parseConfigSingleQuoted(s)
		if err != nil {
			return "", err
		}
		if err := checkConfigTrailingComment(tail); err != nil {
			return "", err
		}
		return val, nil
	case '"':
		val, tail, err := parseConfigDoubleQuoted(s)
		if err != nil {
			return "", err
		}
		if err := checkConfigTrailingComment(tail); err != nil {
			return "", err
		}
		return val, nil
	case '[', '{', '|', '>', '&', '*', '!', '%', '@', '`', ',', ']', '}', '?', ':':
		return "", errors.New("unsupported YAML value syntax")
	default:
		end := len(s)
		for i := 0; i < len(s); i++ {
			if s[i] == '#' && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
				end = i
				break
			}
		}
		val := strings.TrimRight(s[:end], " \t")
		if val == "" {
			return "", errors.New("missing value")
		}
		for i := 0; i < len(val); i++ {
			if val[i] == ':' && (i+1 == len(val) || val[i+1] == ' ' || val[i+1] == '\t') {
				return "", errors.New("unexpected ':' in unquoted value")
			}
		}
		return val, nil
	}
}

func checkConfigTrailingComment(tail string) error {
	if tail == "" {
		return nil
	}
	trimmed := strings.TrimLeft(tail, " \t")
	if trimmed == "" {
		return nil
	}
	if len(trimmed) < len(tail) && trimmed[0] == '#' {
		return nil
	}
	return errors.New("unexpected trailing characters after quoted scalar")
}

func parseConfigSingleQuoted(s string) (string, string, error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] == '\'' {
			if i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			return b.String(), s[i+1:], nil
		}
		b.WriteByte(s[i])
	}
	return "", "", errors.New("unterminated single-quoted scalar")
}

func parseConfigDoubleQuoted(s string) (string, string, error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		ch := s[i]
		if ch == '"' {
			return b.String(), s[i+1:], nil
		}
		if ch != '\\' {
			b.WriteByte(ch)
			continue
		}
		i++
		if i >= len(s) {
			return "", "", errors.New("unterminated escape sequence in double-quoted scalar")
		}
		switch s[i] {
		case '"', '\\', '/', ' ':
			b.WriteByte(s[i])
		case '0':
			b.WriteByte(0)
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 't', '\t':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'v':
			b.WriteByte('\v')
		case 'f':
			b.WriteByte('\f')
		case 'r':
			b.WriteByte('\r')
		case 'e':
			b.WriteByte(0x1b)
		case 'x':
			r, err := parseConfigHexEscape(s[i+1:], 2)
			if err != nil {
				return "", "", err
			}
			b.WriteRune(r)
			i += 2
		case 'u':
			r, err := parseConfigHexEscape(s[i+1:], 4)
			if err != nil {
				return "", "", err
			}
			b.WriteRune(r)
			i += 4
		case 'U':
			r, err := parseConfigHexEscape(s[i+1:], 8)
			if err != nil {
				return "", "", err
			}
			b.WriteRune(r)
			i += 8
		default:
			return "", "", fmt.Errorf("unsupported escape sequence '\\%c' in double-quoted scalar", s[i])
		}
	}
	return "", "", errors.New("unterminated double-quoted scalar")
}

func parseConfigHexEscape(s string, width int) (rune, error) {
	if len(s) < width {
		return 0, errors.New("incomplete hex escape sequence in double-quoted scalar")
	}
	v, err := strconv.ParseUint(s[:width], 16, 32)
	if err != nil {
		return 0, errors.New("invalid hex escape sequence in double-quoted scalar")
	}
	r := rune(v)
	if !utf8.ValidRune(r) {
		return 0, errors.New("invalid Unicode code point in double-quoted scalar")
	}
	return r, nil
}

// readConfigAt reads and parses the committed .tasks.yml at commit.
// When the file is absent, found is false and err is nil.
func readConfigAt(repo *git.Repository, commit git.ID) (Config, bool, error) {
	entry, err := repo.Entry(commit, ".tasks.yml")
	if git.HasCode(err, git.NotFound) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("read .tasks.yml on %s: %w", Branch, err)
	}
	if entry.Mode != 0100644 && entry.Mode != 0100755 {
		return Config{}, false, fmt.Errorf(".tasks.yml on %s must be a regular file; %s", Branch, configSetup)
	}
	data, err := repo.Blob(entry.ID)
	if err != nil {
		return Config{}, false, fmt.Errorf("read .tasks.yml on %s: %w", Branch, err)
	}
	cfg, err := parseConfig(data)
	if err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

// LoadConfig reads a committed .tasks.yml from refs/heads/tasks, so all
// worktrees see the same repository settings.
func LoadConfig(repo *git.Repository) (Config, error) {
	ref, err := repo.Reference(Branch)
	if git.HasCode(err, git.NotFound) {
		return Config{}, ErrMissingLocalHistory
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s for task configuration: %w; %s", Branch, err, configSetup)
	}
	if ref.Symbolic != "" {
		return Config{}, fmt.Errorf("%s must be a direct branch for task configuration; %s", Branch, configSetup)
	}
	cfg, found, err := readConfigAt(repo, ref.ID)
	if err != nil {
		return Config{}, err
	}
	if !found {
		return Config{}, fmt.Errorf(".tasks.yml is missing from %s; %s", Branch, configSetup)
	}
	return cfg, nil
}

// targetBranchAt resolves the configured default_target_branch at commit,
// defaulting to main when .tasks.yml has not been committed yet (e.g. during
// initial bootstrap with `tasks init`).
func targetBranchAt(repo *git.Repository, commit git.ID) (string, error) {
	cfg, found, err := readConfigAt(repo, commit)
	if err != nil {
		return "", err
	}
	if !found {
		return DefaultTargetBranch, nil
	}
	return cfg.DefaultTargetBranch, nil
}

func (p Prefix) ID(number int) string { return string(p) + "-" + strconv.Itoa(number) }
