package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShowNumberValidation(t *testing.T) {
	for _, value := range []string{"all", "0", "-1", "EXAMPLE-0"} {
		if _, err := parse([]string{"show", value}); err == nil || !strings.Contains(err.Error(), "positive ticket number") {
			t.Fatalf("show %q: %v", value, err)
		}
	}
}

func TestShowStagedRename(t *testing.T) {
	repo, root := fixture(t)
	run(t, root, "init")
	metadata := filepath.Join(root, "tasks")
	const oldName = "EXAMPLE-1 Old title.md"
	const newName = "EXAMPLE-1 New title.md"
	const content = "# Example task\n\nStatus: open\n"
	commitTaskText(t, repo, filepath.Join(metadata, oldName), content)
	systemGit(t, metadata, "mv", "--", oldName, newName)

	for _, args := range [][]string{{"show", "1"}, {"show"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if got := run(t, root, args...); got != content {
				t.Fatalf("got %q, want %q", got, content)
			}
		})
	}
	const ambiguous = "ambiguous EXAMPLE-1: " + oldName + " is committed but missing"
	if _, err := cleanupDestination(repo, "EXAMPLE", 1); err == nil || !strings.Contains(err.Error(), ambiguous) {
		t.Fatalf("cleanup destination: expected %q, got %v", ambiguous, err)
	}
	fails(t, root, ambiguous, "comment", "1", "--body", "Must not edit an ambiguous ticket")
}

func TestShowLatestTask(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, root := fixture(t)
		run(t, root, "init")
		for _, args := range [][]string{{"show"}, {"show", "--raw"}} {
			out, diagnostic, err := invoke(root, "", args...)
			if err == nil || !strings.Contains(err.Error(), "no tasks in the shared store") || out != "" || diagnostic != "" {
				t.Fatalf("%v: %q %q %v", args, out, diagnostic, err)
			}
		}
	})
	t.Run("highest-number-in-shared-store", func(t *testing.T) {
		repo, root := fixture(t)
		run(t, root, "init")
		for _, n := range []int{10, 2, 9} {
			commitTaskText(t, repo, filepath.Join(root, "tasks", fmt.Sprintf("EXAMPLE-%d Task.md", n)), fmt.Sprintf("# EXAMPLE-%d\n\nStatus: open\n", n))
		}
		head, err := repo.Head()
		if err != nil {
			t.Fatal(err)
		}
		linked, err := createCheckout(repo, "show-latest-", head.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "tasks", "EXAMPLE-10 Task.md")
		for _, status := range []string{"open", "active", "blocked", "done", "merged", "closed", "unknown"} {
			content := "# Highest task\n\nStatus: " + status + "\nManual edit without final newline"
			writeSafetyFile(t, path, content)
			for _, cwd := range []string{root, linked.Paths.WorkDir} {
				for _, args := range [][]string{{"show"}, {"show", "--raw"}, {"show", "EXAMPLE-10"}, {"show", "10", "--raw"}} {
					if got := run(t, cwd, args...); got != content {
						t.Fatalf("%v: %q != %q", args, got, content)
					}
				}
				if got := run(t, cwd, "show", "example-2"); got != "# EXAMPLE-2\n\nStatus: open\n" {
					t.Fatalf("explicit selection: %q", got)
				}
			}
			if got := string(uiBytes(t, path)); got != content {
				t.Fatalf("show modified task: %q", got)
			}
		}
		// Untracked tasks count, and malformed unrelated records do not prevent
		// reading the latest task for diagnosis.
		writeSafetyFile(t, filepath.Join(root, "tasks", "EXAMPLE-20 Untracked.md"), "Latest untracked task")
		writeSafetyFile(t, filepath.Join(root, "tasks", "EXAMPLE-2 Task.md"), "Broken status")
		writeSafetyFile(t, filepath.Join(root, "tasks", "notes.md"), "Not a task")
		if got := run(t, linked.Paths.WorkDir, "show"); got != "Latest untracked task" {
			t.Fatal(got)
		}
	})
	t.Run("argument-validation", func(t *testing.T) {
		_, root := fixture(t)
		fails(t, root, "expects 1 positional", "show", "1", "2")
		fails(t, root, "positive ticket number", "show", "0")
		fails(t, root, "positive ticket number", "show", "-1")
		fails(t, root, "positive ticket number", "show", "invalid")
		fails(t, root, "found 0", "show", "99")
		help := run(t, root, "show", "--help")
		if !strings.Contains(help, "show [NUMBER] [--raw]") || !strings.Contains(help, "highest-numbered task") {
			t.Fatal(help)
		}
	})
	for _, kind := range []string{"duplicate", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			_, root := fixture(t)
			path := strings.TrimSpace(run(t, root, "create", "First", "--body", "Requirements"))
			latest := filepath.Join(filepath.Dir(path), "EXAMPLE-10 Latest.md")
			want := "regular file"
			switch kind {
			case "duplicate":
				writeSafetyFile(t, latest, "First copy")
				writeSafetyFile(t, filepath.Join(filepath.Dir(path), "EXAMPLE-10 Duplicate.md"), "Second copy")
				want = "found 2"
			case "symlink":
				if err := os.Symlink(path, latest); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(latest, 0700); err != nil {
					t.Fatal(err)
				}
			}
			fails(t, root, want, "show")
			fails(t, root, want, "show", "--raw")
			fails(t, root, want, "show", "10")
		})
	}
}
