package git

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type configurationContext struct{ gitdir, branch string }

// Read HEAD without constructing a repository handle (which admits ownership).
// Native discovery resolves gitfiles, linked worktrees and bare repositories;
// only symbolic HEAD supplies onbranch context, including an unborn branch.
func configurationAdmissionContext(gitdir, initialBranch string) (configurationContext, error) {
	resolved, err := configurationIncludeIdentity(filepath.Clean(gitdir))
	if err != nil {
		return configurationContext{}, errors.New("cannot resolve Git ownership include context")
	}
	context := configurationContext{gitdir: resolved, branch: initialBranch}
	head, err := os.ReadFile(filepath.Join(resolved, "HEAD"))
	if os.IsNotExist(err) {
		return context, nil
	}
	if err != nil {
		return configurationContext{}, errors.New("cannot read Git ownership include branch context")
	}
	context.branch = ""
	if strings.HasPrefix(string(head), "ref:") {
		target := strings.TrimSpace(strings.TrimPrefix(string(head), "ref:"))
		if strings.HasPrefix(target, "refs/heads/") {
			context.branch = target
		}
	}
	return context, nil
}

// Native repository paths are directory paths and can carry a trailing slash.
// Admission must recognize both that representation and our cleaned context;
// otherwise a directory-only gitdir pattern could hide a hasconfig closure.
func (context configurationContext) matchesOwnership(condition, origin string) (bool, error) {
	matched, err := context.matches(condition, origin)
	if err != nil || matched || !(strings.HasPrefix(condition, "gitdir:") || strings.HasPrefix(condition, "gitdir/i:")) {
		return matched, err
	}
	context.gitdir = strings.TrimRight(context.gitdir, string(filepath.Separator)) + string(filepath.Separator)
	return context.matches(condition, origin)
}

func (context configurationContext) matches(condition, origin string) (bool, error) {
	var pattern, subject string
	fold := false
	switch {
	case strings.HasPrefix(condition, "onbranch:"):
		pattern, subject = strings.TrimPrefix(condition, "onbranch:"), strings.TrimPrefix(context.branch, "refs/heads/")
		if pattern == "" || !strings.HasPrefix(context.branch, "refs/heads/") {
			return false, nil
		}
	case strings.HasPrefix(condition, "gitdir:") || strings.HasPrefix(condition, "gitdir/i:"):
		fold = strings.HasPrefix(condition, "gitdir/i:")
		pattern = condition[strings.IndexByte(condition, ':')+1:]
		subject = filepath.ToSlash(context.gitdir)
		if strings.HasPrefix(pattern, "./") {
			if origin == "" {
				return false, errors.New("relative command Git include condition has no originating configuration file")
			}
			pattern = filepath.ToSlash(filepath.Dir(origin)) + "/" + pattern[2:]
		} else if strings.HasPrefix(pattern, "~/") {
			var err error
			pattern, err = configurationExpandPath(pattern)
			if err != nil {
				return false, err
			}
		} else if !filepath.IsAbs(pattern) {
			pattern = "**/" + pattern
		}
	default:
		return false, nil
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	return configurationWildmatch(pattern, subject, fold)
}

// Full-string WM_PATHNAME matching, not gitignore/sparse selection: no comment,
// negation, basename, ancestor, trailing-space or leading-slash transformations.
// Only the byte alphabet/literal/bracket primitives are shared with sparse rules.
func configurationWildmatch(pattern, subject string, fold bool) (bool, error) {
	var expression strings.Builder
	expression.WriteString("(?s)^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
			if i == len(pattern) {
				return false, nil
			}
			expression.WriteString(sparseLiteral(pattern[i], fold))
		case '*':
			start := i
			for i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
			}
			next := i + 1
			slash := next < len(pattern) && pattern[next] == '/'
			escapedSlash := next+1 < len(pattern) && pattern[next] == '\\' && pattern[next+1] == '/'
			if i > start && (start == 0 || pattern[start-1] == '/') && (next == len(pattern) || slash || escapedSlash) {
				if next == len(pattern) {
					expression.WriteString(".*")
				} else {
					expression.WriteString("(.*/)?")
					i++
					if escapedSlash {
						i++
					}
				}
			} else {
				expression.WriteString("[^/]*")
			}
		case '?':
			expression.WriteString("[^/]")
		case '[':
			class, end, ok := sparseBracket(pattern, i, fold)
			if !ok {
				return false, nil
			}
			expression.WriteString(class)
			i = end
		default:
			expression.WriteString(sparseLiteral(pattern[i], fold))
		}
	}
	expression.WriteString("$")
	compiled, err := regexp.Compile(expression.String())
	if err != nil {
		return false, err
	}
	if fold {
		subject = sparseFoldASCII(subject)
	}
	bytes := make([]rune, len(subject))
	for i := range len(subject) {
		bytes[i] = rune(subject[i])
	}
	return compiled.MatchString(string(bytes)), nil
}
