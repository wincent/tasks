package cli

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/wincent/tasks/internal/terminal"
)

func (streams IO) environ() []string {
	if streams.Env != nil {
		return streams.Env
	}
	return os.Environ()
}

func (streams IO) lookupEnv(key string) (string, bool) {
	if streams.Env == nil {
		return os.LookupEnv(key)
	}
	for i := len(streams.Env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(streams.Env[i], "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

func (streams IO) getenv(key string) string {
	value, _ := streams.lookupEnv(key)
	return value
}

func (streams IO) userHomeDir() (string, error) {
	if streams.Env == nil {
		return os.UserHomeDir()
	}
	home := streams.getenv("HOME")
	if home == "" {
		return "", errors.New("$HOME is not defined")
	}
	return home, nil
}

func (streams IO) lookPath(file string) (string, error) {
	if streams.Env == nil {
		return exec.LookPath(file)
	}
	if file == "" {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}
	if strings.Contains(file, "/") {
		if err := findExecutable(file); err != nil {
			return "", &exec.Error{Name: file, Err: err}
		}
		return file, nil
	}
	for _, dir := range filepath.SplitList(streams.getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, file)
		if findExecutable(candidate) == nil {
			if !filepath.IsAbs(candidate) {
				return candidate, &exec.Error{Name: file, Err: exec.ErrDot}
			}
			return candidate, nil
		}
	}
	return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
}

func findExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return syscall.EISDIR
	}
	if info.Mode().Perm()&0111 == 0 {
		return fs.ErrPermission
	}
	return nil
}

func (streams IO) presentation(out io.Writer) presentation {
	interactive := terminal.IsTerminal(out)
	// Preserve list's historical nonempty NO_COLOR behavior.
	return presentation{out, interactive, interactive && streams.getenv("NO_COLOR") == "" && streams.getenv("TERM") != "dumb"}
}

func (streams IO) color(out io.Writer) bool {
	if streams.Env == nil {
		return terminal.Color(out)
	}
	_, disabled := streams.lookupEnv("NO_COLOR")
	return terminal.IsTerminal(out) && !disabled && streams.getenv("TERM") != "dumb"
}
