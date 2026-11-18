package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/testutil"
)

func uiBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func uiAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected absent %s: %v", path, err)
	}
}

func uiTerminal(t *testing.T, root string, args ...string) (string, string, error) {
	t.Helper()
	master, slave, err := testutil.OpenPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err := master.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(master); done <- data }()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = Run(ctx, root, args, IO{In: slave, Out: &out, Err: slave})
	slave.Close()
	return out.String(), strings.ReplaceAll(string(<-done), "\r\n", "\n"), err
}

func uiStage(t *testing.T, tasks *store.Store, path, content string) {
	t.Helper()
	head, err := tasks.Repository.Head()
	if err != nil {
		t.Fatal(err)
	}
	blob, err := tasks.Repository.WriteBlob([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(tasks.Paths.GitDir, "ui-fixture-index")
	if err := tasks.Repository.EditIndexFile(temp, head.ID, []git.TreeEdit{{Path: filepath.Base(path), ID: blob, Mode: 0100644}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temp, filepath.Join(tasks.Paths.GitDir, "index")); err != nil {
		t.Fatal(err)
	}
}

func TestEditorOracleSelectedFileAndLinkedCheckout(t *testing.T) {
	for _, ticket := range []string{"1", "EXAMPLE-1", "example-1", "linked"} {
		t.Run(ticket, func(t *testing.T) {
			repo, root, capture := editorFixture(t)
			path := strings.TrimSpace(run(t, root, "create", "Edited", "--body", "Original description"))
			original := string(uiBytes(t, path))
			updated := strings.Replace(original, "Original description", "Edited description", 1)
			other := strings.TrimSpace(run(t, root, "create", "Other", "--body", "Other description"))
			tasks, err := store.Open(repo)
			if err != nil {
				t.Fatal(err)
			}
			defer tasks.Close()
			otherCommitted := string(uiBytes(t, other))
			staged := otherCommitted + "\nStaged change\n"
			unstaged := staged + "\nUnstaged change\n"
			uiStage(t, tasks, other, staged)
			writeSafetyFile(t, other, unstaged)
			beforeIndex, err := tasks.Repository.IndexEntries()
			if err != nil {
				t.Fatal(err)
			}
			beforeHead, err := tasks.Repository.Head()
			if err != nil {
				t.Fatal(err)
			}
			beforeTree, err := tasks.Repository.Tree(beforeHead.ID)
			if err != nil {
				t.Fatal(err)
			}
			implementation, err := repo.Head()
			if err != nil {
				t.Fatal(err)
			}
			cwd, number := root, ticket
			if ticket == "linked" {
				linked, err := createCheckout(repo, "ui-editor-", implementation.ID, "")
				if err != nil {
					t.Fatal(err)
				}
				cwd, number = linked.Paths.WorkDir, "EXAMPLE-1"
			}
			t.Setenv("TASK_TEST_CONTENT", updated)
			out, diagnostic, err := uiTerminal(t, cwd, "edit", number)
			if err != nil || out != path+"\n" || !strings.Contains(diagnostic, "editor output") {
				t.Fatalf("%q %q %v", out, diagnostic, err)
			}
			c := captured(t, capture)
			if c["initial"] != original || c["stdin_tty"] != true || c["stdout_tty"] != true {
				t.Fatal(c)
			}
			args := c["args"].([]any)
			if !reflect.DeepEqual(args[len(args)-3:len(args)-1], []any{"--flag", "argument with spaces"}) {
				t.Fatal(args)
			}
			uiAbsent(t, c["draft"].(string))
			if string(uiBytes(t, path)) != updated || string(uiBytes(t, other)) != unstaged {
				t.Fatal("editor changed wrong bytes")
			}
			afterHead, err := tasks.Repository.Head()
			if err != nil || afterHead.ID == beforeHead.ID {
				t.Fatalf("no edit commit: %v", err)
			}
			afterTree, err := tasks.Repository.Tree(afterHead.ID)
			if err != nil {
				t.Fatal(err)
			}
			changed := []string{}
			for i, entry := range beforeTree {
				if i >= len(afterTree) {
					t.Fatal("tree lost entries")
				}
				if !reflect.DeepEqual(entry, afterTree[i]) {
					changed = append(changed, entry.Name)
				}
			}
			if len(afterTree) != len(beforeTree) || !reflect.DeepEqual(changed, []string{filepath.Base(path)}) {
				t.Fatalf("changed files: %v", changed)
			}
			entry, err := tasks.Repository.Entry(afterHead.ID, filepath.Base(path))
			if err != nil {
				t.Fatal(err)
			}
			blob, err := tasks.Repository.Blob(entry.ID)
			if err != nil || string(blob) != updated {
				t.Fatalf("committed content %q: %v", blob, err)
			}
			afterIndex, err := tasks.Repository.IndexEntries()
			if err != nil {
				t.Fatal(err)
			}
			for _, before := range beforeIndex {
				if before.Path == filepath.Base(other) {
					found := false
					for _, after := range afterIndex {
						if reflect.DeepEqual(before, after) {
							found = true
						}
					}
					if !found {
						t.Fatal("unrelated staging changed")
					}
				}
			}
			afterImplementation, err := repo.Head()
			if err != nil || implementation != afterImplementation {
				t.Fatal("implementation moved", err)
			}
		})
	}
}

func TestEditorOracleActualFallbacks(t *testing.T) {
	for _, action := range []string{"create", "edit"} {
		for _, choice := range []string{"EDITOR", "vi"} {
			t.Run(action+"/"+choice, func(t *testing.T) {
				_, root, capture := editorFixture(t)
				args := []string{"create", "Edited"}
				updated := "Description from editor.\n\n- Acceptance criterion\n"
				if action == "edit" {
					run(t, root, "create", "Edited", "--body", "Original description")
					updated = strings.Replace(run(t, root, "show", "1"), "Original description", "Edited description", 1)
					args = []string{"edit", "1"}
				}
				command := os.Getenv("VISUAL")
				t.Setenv("VISUAL", "  ")
				t.Setenv("EDITOR", command)
				if choice == "vi" {
					executable, err := os.Executable()
					if err != nil {
						t.Fatal(err)
					}
					dir := t.TempDir()
					script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run=^TestEditorProcess$ -- \"$@\"\n"
					if err := os.WriteFile(filepath.Join(dir, "vi"), []byte(script), 0700); err != nil {
						t.Fatal(err)
					}
					t.Setenv("PATH", dir)
					t.Setenv("EDITOR", "")
				}
				t.Setenv("TASK_TEST_CONTENT", updated)
				out, diagnostic, err := uiTerminal(t, root, args...)
				if err != nil || !strings.Contains(diagnostic, "editor output") {
					t.Fatalf("%q %v", diagnostic, err)
				}
				if string(uiBytes(t, strings.TrimSpace(out))) != updated && action == "edit" {
					t.Fatal("fallback lost edited text")
				}
				if !strings.Contains(run(t, root, "show", "1"), strings.TrimSpace(updated)) {
					t.Fatal("fallback lost body")
				}
				c := captured(t, capture)
				if c["stdin_tty"] != true || c["stdout_tty"] != true {
					t.Fatal(c)
				}
				uiAbsent(t, c["draft"].(string))
			})
		}
	}
}
