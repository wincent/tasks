package store

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wincent/tasks/internal/git"
)

const prefixSetup = "commit a root .tasks.yml to local main with a single 'prefix: YOURPREFIX' setting (uppercase ASCII letters, optionally followed by digits or more letters)"

// Prefix is the configured repository-wide ticket key, without the dash.
type Prefix string

// This one-key YAML format supports plain and quoted keys/values, blank lines,
// and comments without needing a YAML dependency or accepting other settings.
var prefixSetting = regexp.MustCompile(`^(?:prefix|'prefix'|"prefix"):[ \t]+(?:([A-Z][A-Z0-9]*)|'([A-Z][A-Z0-9]*)'|"([A-Z][A-Z0-9]*)")(?:[ \t]+#.*)?$`)

func parsePrefix(data []byte) (Prefix, error) {
	var prefix Prefix
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		match := prefixSetting.FindStringSubmatch(line)
		if match == nil || prefix != "" {
			return "", fmt.Errorf("invalid .tasks.yml at line %d; %s", i+1, prefixSetup)
		}
		prefix = Prefix(match[1] + match[2] + match[3])
	}
	if prefix == "" {
		return "", fmt.Errorf("missing prefix in .tasks.yml; %s", prefixSetup)
	}
	return prefix, nil
}

// LoadPrefix reads a committed blob, so all worktrees see the same setting.
func LoadPrefix(repo *git.Repository) (Prefix, error) {
	main, err := repo.Reference("refs/heads/main")
	if err != nil {
		return "", fmt.Errorf("read local main for task configuration: %w; %s", err, prefixSetup)
	}
	if main.Symbolic != "" {
		return "", fmt.Errorf("local main must be a direct branch for task configuration; %s", prefixSetup)
	}
	entry, err := repo.Entry(main.ID, ".tasks.yml")
	if git.HasCode(err, git.NotFound) {
		return "", fmt.Errorf(".tasks.yml is missing from local main; %s", prefixSetup)
	}
	if err != nil {
		return "", fmt.Errorf("read .tasks.yml on local main: %w", err)
	}
	if entry.Mode != 0100644 && entry.Mode != 0100755 {
		return "", fmt.Errorf(".tasks.yml on local main must be a regular file; %s", prefixSetup)
	}
	data, err := repo.Blob(entry.ID)
	if err != nil {
		return "", fmt.Errorf("read .tasks.yml on local main: %w", err)
	}
	return parsePrefix(data)
}

func (p Prefix) ID(number int) string { return string(p) + "-" + strconv.Itoa(number) }
