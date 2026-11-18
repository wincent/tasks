package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/git"
)

var signature = git.Signature{Name: "Task Test", Email: "task@example.invalid", When: time.Unix(1700000000, 0).UTC()}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func publish(t *testing.T, repo *git.Repository, name string, next, previous git.ID) {
	t.Helper()
	tx, err := repo.Prepare(name, next, map[string]git.ID{name: previous}, signature, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (*git.Repository, *Store, git.ID) {
	t.Helper()
	repo, err := git.Init(filepath.Join(t.TempDir(), "repository"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repo.Close)
	tree, err := repo.WriteTree(nil)
	if err != nil {
		t.Fatal(err)
	}
	main, err := repo.WriteCommit(tree, nil, signature, signature, "implementation\n")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, repo, "refs/heads/main", main, git.ID{})
	initial, err := repo.WriteCommit(tree, nil, signature, signature, "orphan tasks\n")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, repo, Branch, initial, git.ID{})
	path, err := Location(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AddWorktree("tasks", path, Branch); err != nil {
		t.Fatal(err)
	}
	store, err := Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return repo, store, initial
}

func options() CommitOptions {
	return CommitOptions{Signature: signature, Message: "chore: update task"}
}

func TestCommitSelectedFilesPreservesOtherContentAndStaging(t *testing.T) {
	repo, store, initial := fixture(t)
	ctx := context.Background()
	name := "EXAMPLE-1 literal[abc]\nname.md"
	first, err := CommitFiles(ctx, repo, map[string][]byte{name: []byte("first\n")}, options())
	if err != nil {
		t.Fatal(err)
	}
	if first == initial {
		t.Fatal("did not commit")
	}
	blob, err := repo.WriteBlob([]byte("staged bytes"))
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(store.Paths.GitDir, "index")
	isolated := filepath.Join(store.Paths.GitDir, "test-index")
	mustWrite(t, isolated, mustRead(t, index))
	if err := store.Repository.EditIndexFile(isolated, git.ID{}, []git.TreeEdit{{Path: "unrelated", ID: blob, Mode: 0100755}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(isolated, index); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(store.Path, "unrelated"), []byte("unstaged bytes"))
	mustWrite(t, filepath.Join(store.Path, "untracked"), []byte("untracked bytes"))
	mustWrite(t, filepath.Join(store.Path, ".gitignore"), []byte("ignored\n"))
	mustWrite(t, filepath.Join(store.Path, "ignored"), []byte("ignored bytes"))
	revision, err := CommitFiles(ctx, repo, map[string][]byte{name: []byte("updated\n")}, options())
	if err != nil {
		t.Fatal(err)
	}
	entries, err := store.Repository.IndexEntries()
	if err != nil || len(entries) != 2 {
		t.Fatalf("index: %+v %v", entries, err)
	}
	if entries[1].Path != "unrelated" || entries[1].ID != blob || entries[1].Mode != 0100755 {
		t.Fatalf("lost unrelated staging: %+v", entries)
	}
	if string(mustRead(t, filepath.Join(store.Path, "unrelated"))) != "unstaged bytes" {
		t.Fatal("lost unstaged bytes")
	}
	for _, path := range []string{"untracked", "ignored"} {
		if string(mustRead(t, filepath.Join(store.Path, path))) != path+" bytes" {
			t.Fatalf("lost %s", path)
		}
	}
	committed, err := repo.Tree(revision)
	if err != nil || len(committed) != 1 || committed[0].Name != name {
		t.Fatalf("committed unrelated files: %+v %v", committed, err)
	}
	content, err := repo.Blob(committed[0].ID)
	if err != nil || string(content) != "updated\n" {
		t.Fatalf("committed content: %q %v", content, err)
	}
	if ancestor, err := repo.IsAncestor(first, revision); err != nil || !ancestor {
		t.Fatalf("lost history: %v %v", ancestor, err)
	}
	if _, err := os.Lstat(index + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("index lock leaked: %v", err)
	}
}

func TestMetadataRefVerificationAndRacePreservation(t *testing.T) {
	for _, race := range []string{"stale-ref", "callback-error", "file", "index", "index-lock", "head", "operation"} {
		t.Run(race, func(t *testing.T) {
			repo, store, _ := fixture(t)
			name := "EXAMPLE-1 task.md"
			first, err := CommitFiles(context.Background(), repo, map[string][]byte{name: []byte("first")}, options())
			if err != nil {
				t.Fatal(err)
			}
			index := filepath.Join(store.Paths.GitDir, "index")
			beforeIndex := mustRead(t, index)
			opts := options()
			main, _ := repo.Reference("refs/heads/main")
			opts.ExpectedRefs = map[string]git.ID{"refs/heads/main": main.ID}
			if race == "stale-ref" {
				opts.ExpectedRefs["refs/heads/main"] = git.ID{}
			}
			callbackErr := errors.New("refused completion")
			opts.BeforePublish = func() error {
				// Ref verification locks must still be held while callback runs.
				tx, lockErr := repo.Prepare("refs/heads/main", first, map[string]git.ID{"refs/heads/main": main.ID}, signature, "interloper")
				if tx != nil {
					tx.Close()
				}
				if !git.HasCode(lockErr, git.Locked) {
					t.Fatalf("verification ref unlocked: %v", lockErr)
				}
				switch race {
				case "callback-error":
					return callbackErr
				case "file":
					mustWrite(t, filepath.Join(store.Path, name), []byte("other writer"))
				case "index":
					mustWrite(t, index, []byte("other index"))
				case "index-lock":
					if err := os.Remove(index + ".lock"); err != nil {
						t.Fatal(err)
					}
					mustWrite(t, index+".lock", []byte("new owner"))
				case "head":
					mustWrite(t, filepath.Join(store.Paths.GitDir, "HEAD"), []byte(first.String()+"\n"))
				case "operation":
					mustWrite(t, filepath.Join(store.Paths.GitDir, "MERGE_HEAD"), []byte(main.ID.String()+"\n"))
				}
				return nil
			}
			_, err = CommitFiles(context.Background(), repo, map[string][]byte{name: []byte("attempted")}, opts)
			if err == nil {
				t.Fatal("race accepted")
			}
			if race == "callback-error" && !errors.Is(err, callbackErr) {
				t.Fatalf("lost cause: %v", err)
			}
			ref, err := repo.Reference(Branch)
			if err != nil || ref.ID != first {
				t.Fatalf("published on race: %+v %v", ref, err)
			}
			want := "first"
			if race == "file" {
				want = "other writer"
			}
			if string(mustRead(t, filepath.Join(store.Path, name))) != want {
				t.Fatal("overwrote task content")
			}
			if race != "index" && !bytes.Equal(beforeIndex, mustRead(t, index)) {
				t.Fatal("changed index on failed commit")
			}
			if race == "index" && string(mustRead(t, index)) != "other index" {
				t.Fatal("discarded concurrent index")
			}
			if race == "index-lock" {
				if string(mustRead(t, index+".lock")) != "new owner" {
					t.Fatal("removed another owner's replacement lock")
				}
			} else if _, err := os.Lstat(index + ".lock"); !os.IsNotExist(err) {
				t.Fatalf("leaked own index lock: %v", err)
			}
		})
	}
}

func TestMetadataRefusesManualChangesAndOperationMarkers(t *testing.T) {
	for _, obstruction := range []string{"untracked", "modified", "symlink", "directory", "operation", "index-lock", "traversal"} {
		t.Run(obstruction, func(t *testing.T) {
			repo, store, initial := fixture(t)
			name := "EXAMPLE-1 task.md"
			path := filepath.Join(store.Path, name)
			if obstruction == "modified" {
				var err error
				initial, err = CommitFiles(context.Background(), repo, map[string][]byte{name: []byte("committed")}, options())
				if err != nil {
					t.Fatal(err)
				}
			}
			switch obstruction {
			case "untracked", "modified":
				mustWrite(t, path, []byte("precious"))
			case "symlink":
				if err := os.Symlink("missing-target", path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "operation":
				mustWrite(t, filepath.Join(store.Paths.GitDir, "CHERRY_PICK_HEAD"), []byte(initial.String()+"\n"))
			case "index-lock":
				mustWrite(t, filepath.Join(store.Paths.GitDir, "index.lock"), []byte("other owner"))
			case "traversal":
				name = "../escape"
			}
			_, err := CommitFiles(context.Background(), repo, map[string][]byte{name: []byte("attempted")}, options())
			if err == nil {
				t.Fatal("accepted obstruction")
			}
			ref, err := repo.Reference(Branch)
			if err != nil || ref.ID != initial {
				t.Fatalf("ref changed: %+v %v", ref, err)
			}
			if obstruction == "index-lock" && string(mustRead(t, filepath.Join(store.Paths.GitDir, "index.lock"))) != "other owner" {
				t.Fatal("removed another operation's index lock")
			}
		})
	}
}

func TestPublicationFailureRetainsRecoveryEvidence(t *testing.T) {
	repo, store, initial := fixture(t)
	index := filepath.Join(store.Paths.GitDir, "index")
	before := mustRead(t, index)
	opts := options()
	opts.BeforePublish = func() error {
		// Fault injection affects only this disposable fixture's own ref lock.
		return os.Remove(filepath.Join(store.Paths.CommonDir, "refs", "heads", "tasks.lock"))
	}
	revision, err := CommitFiles(context.Background(), repo, map[string][]byte{"EXAMPLE-1 task.md": []byte("proposed")}, opts)
	var recovery *RecoveryError
	if !errors.As(err, &recovery) || recovery.Revision != revision {
		t.Fatalf("missing uncertainty: %v", err)
	}
	if len(mustRead(t, filepath.Join(recovery.Directory, "recovery.json"))) == 0 || len(mustRead(t, filepath.Join(recovery.Directory, "user-index"))) == 0 {
		t.Fatal("missing recovery artifacts")
	}
	if string(mustRead(t, filepath.Join(store.Path, "EXAMPLE-1 task.md"))) != "proposed" {
		t.Fatal("silently rolled back after publication attempt")
	}
	if !bytes.Equal(before, mustRead(t, index)) {
		t.Fatal("installed index after failed ref publication")
	}
	ref, err := repo.Reference(Branch)
	if err != nil || ref.ID != initial {
		t.Fatalf("fault injection did not prevent publication: %+v %v", ref, err)
	}
}

func TestMetadataRejectsSymlinkStateDirectory(t *testing.T) {
	repo, _, initial := fixture(t)
	paths, _ := repo.Paths()
	foreign := t.TempDir()
	if err := os.Symlink(foreign, filepath.Join(paths.CommonDir, "tasks")); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitFiles(context.Background(), repo, map[string][]byte{"EXAMPLE-1 task.md": []byte("attempted")}, options()); err == nil {
		t.Fatal("followed state directory symlink")
	}
	entries, err := os.ReadDir(foreign)
	if err != nil || len(entries) != 0 {
		t.Fatalf("modified foreign directory: %v %v", entries, err)
	}
	ref, err := repo.Reference(Branch)
	if err != nil || ref.ID != initial {
		t.Fatalf("changed tasks: %+v %v", ref, err)
	}
}

func TestMetadataDoesNotRecreateMovedRepository(t *testing.T) {
	repo, _, _ := fixture(t)
	paths, _ := repo.Paths()
	if err := os.Rename(paths.WorkDir, paths.WorkDir+"-moved"); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitFiles(context.Background(), repo, map[string][]byte{"EXAMPLE-1 task.md": []byte("attempted")}, options()); err == nil {
		t.Fatal("accepted stale repository paths")
	}
	if _, err := os.Lstat(paths.WorkDir); !os.IsNotExist(err) {
		t.Fatalf("recreated old repository path: %v", err)
	}
}

func TestStoreRejectsRelatedHistoryAndWrongBranch(t *testing.T) {
	repo, store, initial := fixture(t)
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	main, _ := repo.Reference("refs/heads/main")
	publish(t, repo, "refs/heads/main", initial, main.ID)
	if opened, err := Open(repo); err == nil {
		opened.Close()
		t.Fatal("accepted related tasks history")
	}
	publish(t, repo, "refs/heads/main", main.ID, initial)
	mustWrite(t, filepath.Join(store.Paths.GitDir, "HEAD"), []byte("ref: refs/heads/main\n"))
	if opened, err := Open(repo); err == nil {
		opened.Close()
		t.Fatal("accepted wrong branch")
	}
	if strings.Contains(store.Paths.GitDir, paths.GitDir+"/tasks") {
		t.Fatal("task store confused with local state")
	}
}
