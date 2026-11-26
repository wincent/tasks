package git

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWorktreeConfigMaterialization(t *testing.T) {
	for _, mode := range []string{"counted", "parameters", "onbranch", "gitdir", "local-onbranch", "frozen-include"} {
		t.Run(mode, func(t *testing.T) {
			original, root := fixture(t, false)
			if err := original.SetLocalConfig("core.autocrlf", "false"); err != nil {
				t.Fatal(err)
			}
			tip := safetyTip(t, original, ID{}, map[string]string{
				"fixture.txt":    "line\n",
				"encoded.txt":    "A\n",
				".gitattributes": "encoded.txt text eol=crlf working-tree-encoding=UTF-16LE\n",
			})
			publish(t, original, "refs/heads/tasks", tip, ID{})
			paths, err := original.Paths()
			if err != nil {
				t.Fatal(err)
			}
			// Parent-only attributes must not influence the child's content.
			if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("* -text filter=parent\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := original.SetLocalConfig("filter.parent.smudge", "must-not-run"); err != nil {
				t.Fatal(err)
			}
			included := filepath.Join(t.TempDir(), "included")
			if err := os.WriteFile(included, []byte("[core]\nautocrlf = true\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_CONFIG_PARAMETERS", "")
			switch mode {
			case "counted":
				injectConfiguration(t, [2]string{"core.autocrlf", "true"})
			case "parameters":
				injectConfiguration(t, [2]string{"core.autocrlf", "false"})
				t.Setenv("GIT_CONFIG_PARAMETERS", "'core.autocrlf=true'")
			case "onbranch":
				injectConfiguration(t, [2]string{"includeIf.onbranch:tasks.path", included})
			case "gitdir":
				injectConfiguration(t, [2]string{"includeIf.gitdir:" + filepath.Join(paths.CommonDir, "worktrees", "child") + ".path", included})
			case "local-onbranch":
				injectConfiguration(t)
				if err := original.SetLocalConfig("includeIf.onbranch:tasks.path", included); err != nil {
					t.Fatal(err)
				}
			case "frozen-include":
				injectConfiguration(t, [2]string{"include.path", included})
			}
			repo, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if mode == "frozen-include" {
				if err := os.WriteFile(included, []byte("[core]\nautocrlf = false\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// No subsequent environment read may change this handle's command layer.
			injectConfiguration(t)
			t.Setenv("GIT_CONFIG_PARAMETERS", "")
			destination := filepath.Join(t.TempDir(), "child")
			if err := repo.AddWorktree("child", destination, "refs/heads/tasks"); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, filepath.Join(destination, "fixture.txt")); !bytes.Equal(got, []byte("line\r\n")) {
				t.Fatalf("line endings: %q", got)
			}
			if got := readFile(t, filepath.Join(destination, "encoded.txt")); !bytes.Equal(got, []byte{'A', 0, '\r', 0, '\n', 0}) {
				t.Fatalf("encoding: %x", got)
			}
		})
	}
}

func TestWorktreeConfigRejectsBeforeRegistration(t *testing.T) {
	for _, mode := range []string{"smudge", "process", "selected-clean", "selected-encoding"} {
		t.Run(mode, func(t *testing.T) {
			original, root := fixture(t, false)
			attrs := "*.txt filter=future\n"
			if mode == "selected-encoding" {
				attrs = "*.txt working-tree-encoding=UTF-16LE\n"
			}
			tip := safetyTip(t, original, ID{}, map[string]string{".gitattributes": attrs})
			if mode == "smudge" || mode == "process" {
				tip = safetyTip(t, original, ID{}, map[string]string{".gitattributes": attrs, "fixture.txt": "line\n"})
			}
			publish(t, original, "refs/heads/tasks", tip, ID{})
			include := filepath.Join(t.TempDir(), "future")
			command := mode
			if mode == "selected-clean" {
				command = "clean"
			}
			data := "[filter \"future\"]\n" + command + " = must-not-run\n"
			if mode == "selected-encoding" {
				data = "[core]\nautocrlf = false\n"
			}
			if err := os.WriteFile(include, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			injectConfiguration(t, [2]string{"includeIf.onbranch:tasks.path", include})
			repo, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			destination := filepath.Join(t.TempDir(), "child")
			before := mergePreflightSnapshot(t, root)
			if strings.HasPrefix(mode, "selected-") {
				err = repo.ValidateProspectiveWorktreeContents("child", "refs/heads/tasks", map[string][]byte{"new.txt": []byte("x")})
			} else {
				err = repo.AddWorktree("child", destination, "refs/heads/tasks")
			}
			want := "external filter"
			if mode == "selected-encoding" {
				want = "working-tree-encoding"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("expected %s: %v", want, err)
			}
			if after := mergePreflightSnapshot(t, root); !reflect.DeepEqual(before, after) {
				t.Fatal("rejection changed registration, index, workdir or ODB")
			}
			if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatalf("destination exists: %v", err)
			}
		})
	}
}

func TestWorktreeConfigProspectiveUnbornAndFrozenConditional(t *testing.T) {
	_, root := fixture(t, false)
	include := filepath.Join(t.TempDir(), "future")
	if err := os.WriteFile(include, []byte("[tasktool]\ncontext = first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	injectConfiguration(t, [2]string{"includeIf.onbranch:tasks.path", include})
	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	for i := 0; i < 2; i++ {
		err := repo.WithProspectiveWorktree("child", "refs/heads/tasks", func(view *Repository) error {
			got, err := view.Config("tasktool.context")
			if err != nil || got != "first" {
				t.Fatalf("prospective configuration: %q %v", got, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(include, []byte("[tasktool]\ncontext = changed\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := mergePreflightSnapshot(t, root)
	if err := repo.ValidateProspectiveWorktreeContents("child", "refs/heads/tasks", map[string][]byte{"new.md": []byte("new\n")}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, mergePreflightSnapshot(t, root)) {
		t.Fatal("unborn preflight mutated source")
	}
}
