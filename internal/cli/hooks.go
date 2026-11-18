package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/process"
)

func hook(ctx context.Context, repo *git.Repository, name string, args []string, streams IO) error {
	paths, err := repo.Paths()
	if err != nil {
		return err
	}
	directory, err := repo.Config("core.hookspath")
	if git.HasCode(err, git.NotFound) {
		directory = filepath.Join(paths.CommonDir, "hooks")
	} else if err != nil {
		return err
	}
	if strings.HasPrefix(directory, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		directory = filepath.Join(home, strings.TrimPrefix(directory, "~/"))
	}
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(paths.WorkDir, directory)
	}
	path := filepath.Join(directory, name)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return nil
	}
	runner := streams.Runner
	if runner == nil {
		runner = process.Native{}
	}
	arguments := append([]string{path}, args...)
	result := runner.Run(ctx, process.Command{Args: arguments, Directory: paths.WorkDir, Environment: os.Environ(), Out: streams.Err, Err: streams.Err})
	if errors.Is(result.Err, syscall.ENOEXEC) {
		arguments = append([]string{"/bin/sh"}, arguments...)
		result = runner.Run(ctx, process.Command{Args: arguments, Directory: paths.WorkDir, Environment: os.Environ(), Out: streams.Err, Err: streams.Err})
	}
	if result.Code != 0 || result.Err != nil {
		return process.Failure(arguments, result, "", "")
	}
	return nil
}
