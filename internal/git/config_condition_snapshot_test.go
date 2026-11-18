package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrozenFileGitdirConditionRetainsOrigin(t *testing.T) {
	repo, _ := fixture(t, false)
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(paths.GitDir)
	top := filepath.Join(dir, "command-include")
	target := filepath.Join(dir, "conditional-values")
	writeCommandInclude(t, target, "[tasktool]\ncondition = matched\n")
	for _, prefix := range []string{"gitdir:", "gitdir/i:"} {
		t.Run(prefix, func(t *testing.T) {
			writeCommandInclude(t, top, "[includeIf \""+prefix+"./"+filepath.Base(paths.GitDir)+"\"]\npath = conditional-values\n")
			injectConfiguration(t, [2]string{"include.path", top})
			t.Setenv("GIT_CONFIG_PARAMETERS", "")
			prepared, err := prepareEnvironmentConfiguration()
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.close()
			writeCommandInclude(t, top, "[malformed")
			if err := prepared.apply(repo.ptr); err != nil {
				t.Fatal(err)
			}
			view := preparedConfigurationView(repo, prepared)
			if got, err := view.Config("tasktool.condition"); err != nil || got != "matched" {
				t.Fatal("relative condition or target lost real origin", got, err)
			}
		})
	}
}

func TestFrozenFileBranchConditionsAvoidUnsafeNativeInputs(t *testing.T) {
	for _, mode := range []string{"unborn", "detached", "non-head-symbolic", "empty"} {
		t.Run(mode, func(t *testing.T) {
			repo, _ := fixture(t, false)
			paths, err := repo.Paths()
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "detached":
				writeCommandInclude(t, filepath.Join(paths.GitDir, "HEAD"), strings.Repeat("1", 40)+"\n")
			case "non-head-symbolic":
				writeCommandInclude(t, filepath.Join(paths.GitDir, "HEAD"), "ref: refs/tags/not-a-branch\n")
			}
			dir := t.TempDir()
			top := filepath.Join(dir, "top")
			condition := "onbranch:main"
			if mode == "empty" {
				condition = "onbranch:"
			}
			writeCommandInclude(t, top, "[includeIf \""+condition+"\"]\npath = target\n")
			target := "[malformed"
			if mode == "unborn" {
				target = "[tasktool]\ncondition = matched\n"
			}
			writeCommandInclude(t, filepath.Join(dir, "target"), target)
			injectConfiguration(t, [2]string{"include.path", top})
			t.Setenv("GIT_CONFIG_PARAMETERS", "")
			prepared, err := prepareEnvironmentConfiguration()
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.close()
			temp := t.TempDir()
			t.Setenv("TMPDIR", temp)
			err = prepared.apply(repo.ptr)
			view := preparedConfigurationView(repo, prepared)
			if err != nil {
				t.Fatal(err)
			} else if mode == "unborn" {
				if got, err := view.Config("tasktool.condition"); err != nil || got != "matched" {
					t.Fatal("unborn branch condition did not match", got, err)
				}
			} else if _, err := view.Config("tasktool.condition"); !HasCode(err, NotFound) {
				t.Fatal("inactive condition read its target", err)
			}
			if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
				t.Fatal("condition parser left temporary inputs", entries, err)
			}
		})
	}
}
