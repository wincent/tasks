package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/store"
)

func TestArchiveRoundTrip(t *testing.T) {
	for _, tt := range []struct {
		number, bucket int
		prospective    bool
	}{
		{999, 0, false}, {1000, 1000, true}, {42017, 42000, true},
	} {
		t.Run(fmt.Sprint(tt.number), func(t *testing.T) {
			_, root := fixture(t)
			metadata := filepath.Join(root, "tasks")
			name := fmt.Sprintf("EXAMPLE-%d Task [literal].md", tt.number)
			id := store.Prefix("EXAMPLE").ID(tt.number)
			archived := filepath.Join("archive", fmt.Sprint(tt.bucket), name)
			artifact := filepath.Join("storage", id, "report.txt")
			original := "# Task\n\nStatus: closed\n\nExact bytes, no final newline"
			run(t, root, "create", "Working task", "--body", "Visible")
			writeSafetyFile(t, filepath.Join(metadata, name), original)
			if err := os.Chmod(filepath.Join(metadata, name), 0755); err != nil {
				t.Fatal(err)
			}
			systemGit(t, metadata, "add", "--", name)
			systemGit(t, metadata, "update-index", "--chmod=+x", "--", name)
			systemGit(t, metadata, "commit", "-m", "seed ticket")
			writeSafetyFile(t, filepath.Join(root, "report.txt"), "artifact bytes\n")
			run(t, root, "store", id, "report.txt")
			before := systemGit(t, root, "rev-parse", "tasks")
			if tt.prospective {
				systemGit(t, root, "worktree", "remove", metadata)
			}

			run(t, root, "archive", strings.ToLower(id))
			archivedHead := systemGit(t, root, "rev-parse", "tasks")
			assertTicketMove(t, root, before, name, archived)
			if got := string(uiBytes(t, filepath.Join(metadata, archived))); got != original {
				t.Fatalf("changed ticket bytes: %q", got)
			}
			fails(t, root, "already archived", "archive", id)
			if tt.prospective {
				systemGit(t, root, "worktree", "remove", metadata)
			}

			for _, args := range [][]string{{"list"}, {"list", "all"}, nil, {"search", "literal"}, {"show"}} {
				if got := run(t, root, args...); strings.Contains(got, id) || strings.Contains(got, "Exact bytes") {
					t.Fatalf("archived task leaked into %v: %s", args, got)
				}
			}
			if got := run(t, root, "show"); !strings.Contains(got, "Working task") {
				t.Fatal(got)
			}
			if got := run(t, root, "show", id); !strings.Contains(got, id+" is archived") || !strings.HasSuffix(got, original) {
				t.Fatal(got)
			}
			out, diagnostic, err := invoke(root, "", "show", fmt.Sprint(tt.number), "--raw")
			if err != nil || out != original || !strings.Contains(diagnostic, id+" is archived") {
				t.Fatalf("raw show: %q %q %v", out, diagnostic, err)
			}
			if got := run(t, root, "list", "closed", "--include-archived"); !strings.Contains(got, id+"\tclosed\tTask [literal] (archived)") {
				t.Fatal(got)
			}
			if got := run(t, root, "list", "--include-archived"); strings.Contains(got, id) {
				t.Fatalf("include-archived bypassed the default status filter: %s", got)
			}
			var rows []struct {
				Number   int
				Archived *bool // Require the field on both kinds of record.
			}
			out = run(t, root, "list", "all", "--include-archived", "--json")
			if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 2 || rows[0].Number != 1 || rows[0].Archived == nil || *rows[0].Archived || rows[1].Number != tt.number || rows[1].Archived == nil || !*rows[1].Archived {
				t.Fatalf("unexpected listing: %s (%v)", out, err)
			}
			if got := run(t, root, "store", id); strings.TrimSpace(got) != filepath.Join("tasks", artifact) {
				t.Fatal(got)
			}
			for _, args := range [][]string{{"run", id}, {"edit", id}, {"merge", id}, {"close", id, "--body", "no"}, {"comment", id, "--body", "no"}, {"store", id, "report.txt"}} {
				fails(t, root, id+" is archived; run 'tasks unarchive "+id+"' first", args...)
			}
			if tt.prospective {
				if _, err := os.Lstat(metadata); !os.IsNotExist(err) {
					t.Fatalf("queries or rejected mutations created checkout: %v", err)
				}
			}

			run(t, root, "unarchive", fmt.Sprint(tt.number))
			assertTicketMove(t, root, archivedHead, archived, name)
			fails(t, root, "is not archived", "unarchive", id)
			if got := run(t, root, "show", id); got != original {
				t.Fatalf("changed ticket: %q", got)
			}
			if got := string(uiBytes(t, filepath.Join(metadata, artifact))); got != "artifact bytes\n" {
				t.Fatalf("changed artifact: %q", got)
			}
			if got := systemGit(t, metadata, "status", "--porcelain"); got != "" {
				t.Fatalf("round trip left changes: %s", got)
			}
		})
	}
}

func TestArchiveMultipleTasks(t *testing.T) {
	_, root := fixture(t)
	var paths, archived, originals []string
	for _, title := range []string{"First", "Second", "Third"} {
		path := strings.TrimSpace(run(t, root, "create", title, "--body", "Original"))
		paths = append(paths, path)
		archived = append(archived, filepath.Join(root, "tasks", "archive", "0", filepath.Base(path)))
		originals = append(originals, string(uiBytes(t, path)))
	}
	metadata := filepath.Join(root, "tasks")
	before := systemGit(t, root, "rev-parse", "tasks")
	want := strings.Join([]string{archived[2], archived[1], archived[0]}, "\n") + "\n"
	if got := run(t, root, "archive", "3", "example-2", "1"); got != want {
		t.Fatalf("archive output = %q, want %q", got, want)
	}
	archivedHead := systemGit(t, root, "rev-parse", "tasks")
	if got := systemGit(t, root, "rev-parse", "tasks^"); got != before {
		t.Fatalf("archive should write one commit on %s, got parent %s", before, got)
	}
	if got := systemGit(t, metadata, "status", "--porcelain"); got != "" {
		t.Fatalf("archive left changes: %s", got)
	}
	for i, path := range archived {
		if got, want := string(uiBytes(t, path)), originals[i]; got != want {
			t.Fatalf("archive changed ticket contents: %q, want %q", got, want)
		}
	}

	want = strings.Join([]string{paths[1], paths[0], paths[2]}, "\n") + "\n"
	if got := run(t, root, "unarchive", "2", "EXAMPLE-1", "3"); got != want {
		t.Fatalf("unarchive output = %q, want %q", got, want)
	}
	if got := systemGit(t, root, "rev-parse", "tasks^"); got != archivedHead {
		t.Fatalf("unarchive should write one commit on %s, got parent %s", archivedHead, got)
	}
	if got := systemGit(t, root, "diff", "--name-status", before, "tasks", "--"); got != "" {
		t.Fatalf("round trip changed the tasks tree: %s", got)
	}
	if got := systemGit(t, metadata, "status", "--porcelain"); got != "" {
		t.Fatalf("round trip left changes: %s", got)
	}
}

func TestArchiveBatchRejectsInvalidSelections(t *testing.T) {
	repo, root := fixture(t)
	run(t, root, "create", "First", "--body", "Original")
	run(t, root, "create", "Second", "--body", "Original")
	metadata := filepath.Join(root, "tasks")
	assertUnchanged := func(before string) {
		t.Helper()
		if got := systemGit(t, root, "rev-parse", "tasks"); got != before {
			t.Fatalf("rejected batch changed tasks ref: %s, want %s", got, before)
		}
		if got := systemGit(t, metadata, "status", "--porcelain"); got != "" {
			t.Fatalf("rejected batch left changes: %s", got)
		}
	}
	before := systemGit(t, root, "rev-parse", "tasks")
	fails(t, root, "unexpected task prefix WRONG", "archive", "1", "WRONG-2")
	fails(t, root, "positive ticket number", "archive", "1", "0")
	fails(t, root, "duplicate task number: EXAMPLE-1", "archive", "1", "example-1")
	fails(t, root, "EXAMPLE-3 task in the shared store; found 0", "archive", "1", "3")
	fails(t, root, "mutually exclusive", "archive", "1", "2", "--query", "status:open")

	directory, err := operationState(repo)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := taskLock(directory, "EXAMPLE", 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	fails(t, root, "EXAMPLE-2 already has a running agent or task operation", "archive", "1", "2")
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	assertUnchanged(before)

	// This also checks that the failed batch released the first task's lock.
	run(t, root, "archive", "1")
	before = systemGit(t, root, "rev-parse", "tasks")
	fails(t, root, "EXAMPLE-1 is already archived", "archive", "2", "1")
	fails(t, root, "EXAMPLE-2 is not archived", "unarchive", "1", "2")
	fails(t, root, "unexpected task prefix WRONG", "unarchive", "1", "WRONG-2")
	fails(t, root, "duplicate task number: EXAMPLE-1", "unarchive", "1", "example-1")
	assertUnchanged(before)
}

func assertTicketMove(t *testing.T, root, previous, from, to string) {
	t.Helper()
	if got := systemGit(t, root, "show", "-s", "--format=%P", store.Branch); got != previous {
		t.Fatalf("expected one metadata commit on %s, got parents %s", previous, got)
	}
	want := "R100\t" + from + "\t" + to
	if got := systemGit(t, root, "diff", "--name-status", "-M100%", previous, store.Branch); got != want {
		t.Fatalf("commit changed more than the ticket path: %q, want %q", got, want)
	}
	if got := systemGit(t, root, "ls-tree", "tasks", "--", to); !strings.HasPrefix(got, "100755 blob ") {
		t.Fatalf("ticket mode changed: %q", got)
	}
}

func TestArchiveRejectsInvalidMovesAndPreservesLocalChanges(t *testing.T) {
	repo, root := fixture(t)
	path := strings.TrimSpace(run(t, root, "create", "Target", "--body", "Original"))
	metadata := filepath.Dir(path)
	original := uiBytes(t, path)
	before := systemGit(t, root, "rev-parse", "tasks")
	for _, action := range []string{"archive", "unarchive"} {
		fails(t, root, "expects at least 1 positional", action)
		fails(t, root, "positive ticket number", action, "0")
		fails(t, root, "positive ticket number", action, "-1")
		fails(t, root, "prefix", action, "WRONG-1")
	}
	directory, err := operationState(repo)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := taskLock(directory, "EXAMPLE", 1)
	if err != nil {
		t.Fatal(err)
	}
	fails(t, root, "running agent or task operation", "archive", "1")
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	writeSafetyFile(t, path, "unsaved edit")
	fails(t, root, "manual changes", "archive", "1")
	systemGit(t, metadata, "add", "--", filepath.Base(path))
	fails(t, root, "manual changes", "archive", "1")
	systemGit(t, metadata, "restore", "--source=HEAD", "--staged", "--worktree", "--", filepath.Base(path))
	bucket := filepath.Join(metadata, "archive", "0")
	if err := os.MkdirAll(bucket, 0755); err != nil {
		t.Fatal(err)
	}
	remove := func(path string) {
		t.Helper()
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	collision := filepath.Join(bucket, filepath.Base(path))
	writeSafetyFile(t, collision, "collision")
	fails(t, root, "found 2", "archive", "1")
	if got := string(uiBytes(t, collision)); got != "collision" {
		t.Fatal(got)
	}
	remove(collision)
	duplicate := filepath.Join(bucket, "EXAMPLE-1 Different title.md")
	writeSafetyFile(t, duplicate, "duplicate")
	fails(t, root, "found 2", "archive", "1")
	systemGit(t, metadata, "add", "--", filepath.Join("archive", "0", filepath.Base(duplicate)))
	remove(duplicate)
	fails(t, root, "duplicate path in the task index", "archive", "1")
	systemGit(t, metadata, "reset", "--", filepath.Join("archive", "0", filepath.Base(duplicate)))
	remove(bucket)
	outside := t.TempDir()
	if err := os.Symlink(outside, bucket); err != nil {
		t.Fatal(err)
	}
	fails(t, root, "archive directory is obstructed", "archive", "1")
	remove(bucket)
	if got := systemGit(t, root, "rev-parse", "tasks"); got != before || string(uiBytes(t, path)) != string(original) {
		t.Fatal("rejected archive changed the ticket or metadata ref")
	}

	unrelated := filepath.Join(metadata, "unrelated")
	writeSafetyFile(t, unrelated, "staged")
	systemGit(t, metadata, "add", "unrelated")
	writeSafetyFile(t, unrelated, "unstaged")
	staged := systemGit(t, metadata, "ls-files", "--stage", "--", "unrelated")
	assertUnrelated := func() {
		t.Helper()
		if systemGit(t, metadata, "ls-files", "--stage", "--", "unrelated") != staged || string(uiBytes(t, unrelated)) != "unstaged" || systemGit(t, metadata, "ls-tree", "HEAD", "--", "unrelated") != "" {
			t.Fatal("move disturbed unrelated staged or unstaged changes")
		}
	}
	run(t, root, "archive", "1")
	assertUnrelated()
	writeSafetyFile(t, path, "restore collision")
	fails(t, root, "found 2", "unarchive", "1")
	if got := string(uiBytes(t, path)); got != "restore collision" {
		t.Fatal(got)
	}
	remove(path)
	run(t, root, "unarchive", "1")
	assertUnrelated()
}

func TestArchivedNumbersAreNotReused(t *testing.T) {
	repo, root := fixture(t)
	initial, err := repo.Reference(store.Branch)
	if err != nil {
		t.Fatal(err)
	}
	const path = "archive/1000/EXAMPLE-1000 Archived.md"
	blob, err := repo.WriteBlob([]byte("Status: done\n"))
	if err != nil {
		t.Fatal(err)
	}
	who, err := repo.CommitterSignature()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.EditTree(initial.ID, []git.TreeEdit{{Path: path, ID: blob, Mode: 0100644}})
	if err != nil {
		t.Fatal(err)
	}
	onlyArchived, err := repo.WriteCommit(tree, []git.ID{initial.ID}, who, who, "import archived record\n")
	if err != nil {
		t.Fatal(err)
	}
	setRef(t, repo, store.Branch, onlyArchived, initial.ID)
	t.Run("deleted archival record still reserves its number", func(t *testing.T) {
		empty, err := repo.EditTree(onlyArchived, []git.TreeEdit{{Path: path}})
		if err != nil {
			t.Fatal(err)
		}
		id, err := repo.WriteCommit(empty, []git.ID{onlyArchived}, who, who, "delete archived record\n")
		if err != nil {
			t.Fatal(err)
		}
		setRef(t, repo, store.Branch, id, onlyArchived)
		if got := run(t, root, "create", "Next", "--body", "body"); !strings.Contains(got, "EXAMPLE-1001 Next.md") {
			t.Fatal(got)
		}
	})
}

func TestCleanupArchivedTaskUsesRecordedTopic(t *testing.T) {
	repo, root := fixture(t)
	run(t, root, "create", "Topic task", "--topic", "delivery", "--body", "Only on the topic")
	worktree := filepath.Join(filepath.Dir(root), "topic-task")
	systemGit(t, root, "worktree", "add", "-b", "task/example-1", worktree, "main")
	tip := commitImplementation(t, worktree, "only-on-topic.txt", "not in main\n")
	systemGit(t, root, "branch", "delivery", tip)
	directory, err := operationState(repo)
	if err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(directory, "saved.jsonl")
	writeSafetyFile(t, session, "saved session\n")
	if err := writeTaskSession(directory, 1, taskSession{Worktree: worktree, Branch: "refs/heads/task/example-1", SessionFile: session}); err != nil {
		t.Fatal(err)
	}
	claim := uiBytes(t, taskSessionPath(directory, 1))
	run(t, root, "archive", "1")
	if got := systemGit(t, worktree, "rev-parse", "HEAD"); got != tip || string(uiBytes(t, taskSessionPath(directory, 1))) != string(claim) || string(uiBytes(t, session)) != "saved session\n" {
		t.Fatal("archive disturbed implementation worktree, branch, or saved session")
	}
	metadata := filepath.Join(root, "tasks")
	systemGit(t, root, "worktree", "remove", metadata)
	if got := run(t, root, "cleanup", "EXAMPLE-1"); !strings.Contains(got, "Would remove branch task/example-1") {
		t.Fatal(got)
	}
	if got := run(t, root, "cleanup", "--apply"); !strings.Contains(got, "Removed branch task/example-1") {
		t.Fatal(got)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("cleanup left checkout: %v", err)
	}
	if _, err := os.Stat(metadata); !os.IsNotExist(err) {
		t.Fatalf("cleanup created metadata worktree: %v", err)
	}
	if got := run(t, root, "show", "1"); !strings.Contains(got, "Topic: delivery") {
		t.Fatal(got)
	}
}
