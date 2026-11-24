package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type userConfig struct {
	modelAliases map[string]string
}

func userConfigPath(streams IO) (string, bool) {
	if xdg := streams.getenv("XDG_CONFIG_HOME"); xdg != "" && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "tasks", "config.yml"), true
	}
	home, err := streams.userHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".config", "tasks", "config.yml"), true
}

func loadUserConfig(streams IO) (userConfig, error) {
	path, ok := userConfigPath(streams)
	if !ok {
		return userConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return userConfig{}, nil
		}
		return userConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg, err := parseUserConfig(data)
	if err != nil {
		return userConfig{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

func formatUserConfig(cfg userConfig) string {
	if len(cfg.modelAliases) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("models:\n  aliases:\n")
	for _, name := range slices.Sorted(maps.Keys(cfg.modelAliases)) {
		b.WriteString("    ")
		b.WriteString(formatYAMLScalar(name))
		b.WriteString(": ")
		b.WriteString(formatYAMLScalar(cfg.modelAliases[name]))
		b.WriteByte('\n')
	}
	return b.String()
}

func formatYAMLScalar(s string) string {
	if !needsYAMLQuoting(s) {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case 0:
			b.WriteString(`\0`)
		case '\a':
			b.WriteString(`\a`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\v':
			b.WriteString(`\v`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		case 0x1b:
			b.WriteString(`\e`)
		default:
			if ch < 0x20 || ch == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, ch)
			} else {
				b.WriteByte(ch)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func needsYAMLQuoting(s string) bool {
	if s == "" {
		return true
	}
	if s[0] == ' ' || s[0] == '\t' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t' {
		return true
	}
	switch s[0] {
	case '#', '\'', '"', '|', '>', '{', '[', '&', '*', '!', '@', '`', '%':
		return true
	}
	if s == "-" || strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "-\t") ||
		s == "?" || strings.HasPrefix(s, "? ") || strings.HasPrefix(s, "?\t") ||
		s == ":" || strings.HasPrefix(s, ": ") || strings.HasPrefix(s, ":\t") ||
		s == "---" || strings.HasPrefix(s, "--- ") || strings.HasPrefix(s, "---\t") ||
		s == "..." || strings.HasPrefix(s, "... ") || strings.HasPrefix(s, "...\t") {
		return true
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch < 0x20 || ch == 0x7f {
			return true
		}
		if ch == '#' && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
			return true
		}
		if ch == ':' && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t') {
			return true
		}
	}
	return false
}

func resolveModelOption(a arguments, streams IO) (string, bool, error) {
	if len(a.values["--model"]) == 0 {
		return "", false, nil
	}
	cfg, err := loadUserConfig(streams)
	if err != nil {
		return "", false, err
	}
	return cfg.resolveModel(a.value("--model")), true, nil
}

func isThinkingLevel(s string) bool {
	switch s {
	case "off", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func (c userConfig) resolveModel(model string) string {
	if len(c.modelAliases) == 0 {
		return model
	}
	if target, ok := c.modelAliases[model]; ok {
		return target
	}
	if i := strings.LastIndexByte(model, ':'); i > 0 && i < len(model)-1 {
		base, thinking := model[:i], model[i+1:]
		if target, ok := c.modelAliases[base]; ok {
			if j := strings.LastIndexByte(target, ':'); j > 0 && isThinkingLevel(target[j+1:]) {
				return target[:j+1] + thinking
			}
			return target + ":" + thinking
		}
	}
	return model
}

type configSection int

const (
	configSectionRoot configSection = iota
	configSectionModels
	configSectionAliases
)

// parseUserConfig parses a strict YAML subset supporting the nested
// models.aliases mapping without adding a third-party YAML dependency.
func parseUserConfig(data []byte) (userConfig, error) {
	if !utf8.Valid(data) {
		return userConfig{}, fmt.Errorf("line %d: invalid UTF-8", invalidUTF8Line(data))
	}

	section := configSectionRoot
	seenModels := false
	seenAliases := false
	modelsIndent := -1
	aliasesIndent := -1
	var aliases map[string]string

	lines := strings.Split(string(data), "\n")
	for idx, rawLine := range lines {
		lineNo := idx + 1
		line := strings.TrimSuffix(rawLine, "\r")
		for i := 0; i < len(line); i++ {
			b := line[i]
			if b == '\r' || b == 0x7f || (b < 0x20 && b != '\t') {
				return userConfig{}, fmt.Errorf("line %d: unsupported control character 0x%02x", lineNo, b)
			}
		}

		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || trimmed[0] == '#' {
			continue
		}

		indent := len(line) - len(trimmed)
		if strings.ContainsRune(line[:indent], '\t') {
			return userConfig{}, fmt.Errorf("line %d: tabs are not allowed for indentation", lineNo)
		}

		key, value, hasValue, err := parseMappingLine(trimmed)
		if err != nil {
			return userConfig{}, fmt.Errorf("line %d: %w", lineNo, err)
		}

		switch section {
		case configSectionRoot:
			if indent != 0 {
				return userConfig{}, fmt.Errorf("line %d: unexpected indentation", lineNo)
			}
		case configSectionModels:
			switch {
			case indent == 0:
				section = configSectionRoot
			case modelsIndent == -1:
				modelsIndent = indent
			case indent != modelsIndent:
				return userConfig{}, fmt.Errorf("line %d: inconsistent indentation", lineNo)
			}
		case configSectionAliases:
			switch {
			case indent == 0:
				section = configSectionRoot
			case indent == modelsIndent:
				section = configSectionModels
			case indent < modelsIndent:
				return userConfig{}, fmt.Errorf("line %d: inconsistent indentation", lineNo)
			case aliasesIndent == -1:
				aliasesIndent = indent
			case indent != aliasesIndent:
				return userConfig{}, fmt.Errorf("line %d: inconsistent indentation", lineNo)
			}
		}

		switch section {
		case configSectionRoot:
			if key != "models" {
				return userConfig{}, fmt.Errorf("line %d: unknown top-level key %q", lineNo, key)
			}
			if seenModels {
				return userConfig{}, fmt.Errorf("line %d: duplicate key %q", lineNo, key)
			}
			if hasValue {
				return userConfig{}, fmt.Errorf("line %d: %q must be a mapping", lineNo, key)
			}
			seenModels = true
			section = configSectionModels
		case configSectionModels:
			if key != "aliases" {
				return userConfig{}, fmt.Errorf("line %d: unknown key %q under models", lineNo, key)
			}
			if seenAliases {
				return userConfig{}, fmt.Errorf("line %d: duplicate key %q under models", lineNo, key)
			}
			if hasValue {
				return userConfig{}, fmt.Errorf("line %d: %q must be a mapping", lineNo, key)
			}
			seenAliases = true
			section = configSectionAliases
		case configSectionAliases:
			if err := validateAliasName(key); err != nil {
				return userConfig{}, fmt.Errorf("line %d: %w", lineNo, err)
			}
			if !hasValue || strings.TrimSpace(value) == "" {
				return userConfig{}, fmt.Errorf("line %d: model alias %q requires a nonempty value", lineNo, key)
			}
			if strings.ContainsAny(value, "\x00\n\r") {
				return userConfig{}, fmt.Errorf("line %d: model alias %q value must be a single line", lineNo, key)
			}
			if aliases == nil {
				aliases = make(map[string]string)
			}
			if _, exists := aliases[key]; exists {
				return userConfig{}, fmt.Errorf("line %d: duplicate model alias %q", lineNo, key)
			}
			aliases[key] = value
		}
	}

	return userConfig{modelAliases: aliases}, nil
}

func invalidUTF8Line(data []byte) int {
	line := 1
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			return line
		}
		if r == '\n' {
			line++
		}
		data = data[size:]
	}
	return line
}

func validateAliasName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("model alias name must not be empty or whitespace")
	}
	for _, r := range name {
		if unicode.IsSpace(r) {
			return fmt.Errorf("invalid model alias %q: must not contain whitespace", name)
		}
		if r == ':' {
			return fmt.Errorf("invalid model alias %q: must not contain ':'", name)
		}
		if r == 0 {
			return fmt.Errorf("invalid model alias %q: must not contain NUL", name)
		}
	}
	return nil
}

func parseMappingLine(s string) (key, value string, hasValue bool, err error) {
	switch {
	case s == "---" || strings.HasPrefix(s, "--- ") || strings.HasPrefix(s, "---\t"):
		return "", "", false, errors.New("document markers are not supported")
	case s == "..." || strings.HasPrefix(s, "... ") || strings.HasPrefix(s, "...\t"):
		return "", "", false, errors.New("document markers are not supported")
	case s[0] == '%':
		return "", "", false, errors.New("YAML directives are not supported")
	case s == "-" || strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "-\t"):
		return "", "", false, errors.New("sequences are not supported")
	case s[0] == '{' || s[0] == '[':
		return "", "", false, errors.New("flow collections are not supported")
	case s == "?" || strings.HasPrefix(s, "? ") || strings.HasPrefix(s, "?\t"):
		return "", "", false, errors.New("explicit mapping keys are not supported")
	case s[0] == '&' || s[0] == '*':
		return "", "", false, errors.New("anchors and aliases are not supported")
	case s[0] == '!':
		return "", "", false, errors.New("tags are not supported")
	case s[0] == '|' || s[0] == '>':
		return "", "", false, errors.New("multiline scalars are not supported")
	case s[0] == '@' || s[0] == '`':
		return "", "", false, errors.New("unsupported YAML syntax")
	}

	var rest string
	switch s[0] {
	case '\'':
		key, rest, err = parseSingleQuoted(s)
		if err != nil {
			return "", "", false, err
		}
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" || rest[0] != ':' || (len(rest) > 1 && rest[1] != ' ' && rest[1] != '\t') {
			return "", "", false, errors.New("expected ':' mapping separator after key")
		}
		rest = rest[1:]
	case '"':
		key, rest, err = parseDoubleQuoted(s)
		if err != nil {
			return "", "", false, err
		}
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" || rest[0] != ':' || (len(rest) > 1 && rest[1] != ' ' && rest[1] != '\t') {
			return "", "", false, errors.New("expected ':' mapping separator after key")
		}
		rest = rest[1:]
	default:
		sep := findMappingSeparator(s)
		if sep < 0 {
			return "", "", false, errors.New("expected mapping entry (key: value)")
		}
		key = strings.TrimRight(s[:sep], " \t")
		rest = s[sep+1:]
	}

	valRest := strings.TrimLeft(rest, " \t")
	if valRest == "" || valRest[0] == '#' {
		return key, "", false, nil
	}

	switch valRest[0] {
	case '\'':
		val, tail, err := parseSingleQuoted(valRest)
		if err != nil {
			return "", "", false, err
		}
		if err := checkTrailingComment(tail); err != nil {
			return "", "", false, err
		}
		return key, val, true, nil
	case '"':
		val, tail, err := parseDoubleQuoted(valRest)
		if err != nil {
			return "", "", false, err
		}
		if err := checkTrailingComment(tail); err != nil {
			return "", "", false, err
		}
		return key, val, true, nil
	case '|', '>':
		return "", "", false, errors.New("multiline scalars are not supported")
	case '{', '[':
		return "", "", false, errors.New("flow collections are not supported")
	case '&', '*':
		return "", "", false, errors.New("anchors and aliases are not supported")
	case '!':
		return "", "", false, errors.New("tags are not supported")
	case '@', '`':
		return "", "", false, errors.New("unsupported YAML syntax")
	}
	if valRest == "-" || strings.HasPrefix(valRest, "- ") || strings.HasPrefix(valRest, "-\t") {
		return "", "", false, errors.New("sequences are not supported")
	}
	if valRest == "?" || strings.HasPrefix(valRest, "? ") || strings.HasPrefix(valRest, "?\t") ||
		valRest == ":" || strings.HasPrefix(valRest, ": ") || strings.HasPrefix(valRest, ":\t") {
		return "", "", false, errors.New("unsupported YAML syntax")
	}

	val, err := parsePlainValue(valRest)
	if err != nil {
		return "", "", false, err
	}
	return key, val, true, nil
}

func findMappingSeparator(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
			return -1
		}
		if s[i] == ':' && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t') {
			return i
		}
	}
	return -1
}

func checkTrailingComment(tail string) error {
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

func parsePlainValue(s string) (string, error) {
	end := len(s)
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
			end = i
			break
		}
	}
	val := strings.TrimRight(s[:end], " \t")
	for i := 0; i < len(val); i++ {
		if val[i] == ':' && (i+1 == len(val) || val[i+1] == ' ' || val[i+1] == '\t') {
			return "", errors.New("unexpected ':' in unquoted value; quote the value if literal")
		}
	}
	return val, nil
}

func parseSingleQuoted(s string) (string, string, error) {
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

func parseDoubleQuoted(s string) (string, string, error) {
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
			r, err := parseHexEscape(s[i+1:], 2)
			if err != nil {
				return "", "", err
			}
			b.WriteRune(r)
			i += 2
		case 'u':
			r, err := parseHexEscape(s[i+1:], 4)
			if err != nil {
				return "", "", err
			}
			b.WriteRune(r)
			i += 4
		case 'U':
			r, err := parseHexEscape(s[i+1:], 8)
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

func parseHexEscape(s string, width int) (rune, error) {
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
