package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/testutil"
)

func TestMain(m *testing.M) {
	state.DisableSyncForTesting()
	if os.Getenv("TASK_TEST_COMMAND_HELPER") != "task" {
		os.Exit(testutil.RunWithIsolatedHome(m.Run))
	}
	cwd, err := os.Getwd()
	if err == nil {
		err = Run(context.Background(), cwd, os.Args[1:], IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(ExitCode(err))
	}
	os.Exit(0)
}

func fixture(t *testing.T) (*git.Repository, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "repository")
	repo, err := git.Init(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repo.Close)
	for key, value := range map[string]string{"user.name": "Task Test", "user.email": "task@example.invalid", "commit.gpgsign": "false"} {
		if err := repo.SetLocalConfig(key, value); err != nil {
			t.Fatal(err)
		}
	}
	commitMainConfig(t, repo, "prefix: EXAMPLE\n")
	seedLocalTasks(t, repo)
	return repo, root
}

// An empty config string means no file (or removal from an existing tree).
func commitMainConfig(t *testing.T, repo *git.Repository, config string) git.ID {
	t.Helper()
	base, err := repo.ResolveCommit(mainRef)
	if err != nil && !git.HasCode(err, git.NotFound) {
		t.Fatal(err)
	}
	who := git.Signature{Name: "Task Test", Email: "task@example.invalid", When: time.Unix(1700000000, 0)}
	if base == (git.ID{}) {
		if err := repo.SetHead(mainRef); err != nil {
			t.Fatal(err)
		}
		empty, err := repo.WriteTree(nil)
		if err != nil {
			t.Fatal(err)
		}
		base, err = repo.WriteCommit(empty, nil, who, who, "initial\n")
		if err != nil {
			t.Fatal(err)
		}
		setRef(t, repo, mainRef, base, git.ID{})
	}
	edit := git.TreeEdit{Path: ".tasks.yml"}
	if config != "" {
		edit.ID, err = repo.WriteBlob([]byte(config))
		if err != nil {
			t.Fatal(err)
		}
		edit.Mode = 0100644
	}
	tree, err := repo.EditTree(base, []git.TreeEdit{edit})
	if err != nil {
		t.Fatal(err)
	}
	id, err := repo.WriteCommit(tree, []git.ID{base}, who, who, "main\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Checkout(base, id); err != nil {
		t.Fatal(err)
	}
	setRef(t, repo, mainRef, id, base)
	return id
}

func setRef(t *testing.T, repo *git.Repository, name string, id, old git.ID) {
	t.Helper()
	who := git.Signature{Name: "Task Test", Email: "task@example.invalid", When: time.Now()}
	tx, err := repo.Prepare(name, id, map[string]git.ID{name: old}, who, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func commitTaskText(t *testing.T, repo *git.Repository, path, content string) {
	t.Helper()
	who, err := repo.CommitterSignature()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitFiles(context.Background(), repo, map[string][]byte{filepath.Base(path): []byte(content)}, store.CommitOptions{Signature: who, Message: "fixture task state"}); err != nil {
		t.Fatal(err)
	}
}

func withEnv(base []string, overrides ...string) []string {
	if base == nil {
		base = os.Environ()
	}
	env := append([]string(nil), base...)
	for _, override := range overrides {
		key, _, hasValue := strings.Cut(override, "=")
		replaced := false
		for i, entry := range env {
			if k, _, ok := strings.Cut(entry, "="); ok && k == key {
				if hasValue {
					env[i] = override
				} else {
					env = append(env[:i], env[i+1:]...)
				}
				replaced = true
				break
			}
		}
		if hasValue && !replaced {
			env = append(env, override)
		}
	}
	return env
}

func writeSafetyFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, diagnostic, err := invoke(root, "", args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, diagnostic)
	}
	return out
}
func invoke(root, input string, args ...string) (string, string, error) {
	var out, diagnostic bytes.Buffer
	err := Run(context.Background(), root, args, IO{In: strings.NewReader(input), Out: &out, Err: &diagnostic})
	return out.String(), diagnostic.String(), err
}
func fails(t *testing.T, root, contains string, args ...string) {
	t.Helper()
	_, _, err := invoke(root, "", args...)
	if err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("%v: expected %q, got %v", args, contains, err)
	}
}
