package store

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wincent/tasks/internal/git"
)

func emptyRepository(t *testing.T) *git.Repository {
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
	return repo
}

func withSession(t *testing.T, repo *git.Repository, fn func(*Session)) {
	t.Helper()
	session, err := Begin(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	fn(session)
}

func TestBootstrapExplicitLocalHistory(t *testing.T) {
	remote, tasks, _ := fixture(t)
	id, err := CommitFiles(context.Background(), remote, map[string][]byte{"EXAMPLE-42 Remote.md": []byte("Status: open\n")}, options())
	if err != nil {
		t.Fatal(err)
	}
	repo := emptyRepository(t)
	copyFixtureTasks(t, remote, repo)
	publish(t, repo, Branch, id, git.ID{})
	for key, value := range map[string]string{"remote.origin.url": "git@private-alias:project.git", "remote.second.url": "/missing/repository.git"} {
		if err := repo.SetLocalConfig(key, value); err != nil {
			t.Fatal(err)
		}
	}
	paths, _ := repo.Paths()
	config := mustRead(t, filepath.Join(paths.CommonDir, "config"))
	t.Setenv("PATH", "/nonexistent")
	withSession(t, repo, func(session *Session) {
		for range 2 {
			opened, err := session.Ensure(signature)
			if err != nil {
				t.Fatal(err)
			}
			head, err := opened.Repository.Head()
			opened.Close()
			if err != nil || head.ID != id {
				t.Fatalf("head %v %v", head, err)
			}
		}
		n, err := session.NextTaskNumber("EXAMPLE")
		if err != nil || n != 43 {
			t.Fatalf("next number %d %v", n, err)
		}
	})
	if !reflect.DeepEqual(config, mustRead(t, filepath.Join(paths.CommonDir, "config"))) {
		t.Fatal("changed remote/tracking configuration")
	}
	if _, err := os.Stat(filepath.Join(tasks.Path, "EXAMPLE-42 Remote.md")); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapMissingLocalBranchNeverContactsRemotes(t *testing.T) {
	var contacts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contacts.Add(1)
		http.Error(w, "remote must not be contacted", http.StatusForbidden)
	}))
	defer server.Close()
	for _, remotes := range [][]string{nil, {"/missing/repository.git"}, {"git@private-ssh-alias:project.git"}, {server.URL}, {server.URL, "git@another-alias:project.git"}} {
		t.Run(strings.Join(remotes, ","), func(t *testing.T) {
			repo := emptyRepository(t)
			for i, url := range remotes {
				if err := repo.SetLocalConfig("remote.remote"+string(rune('a'+i))+".url", url); err != nil {
					t.Fatal(err)
				}
			}
			withSession(t, repo, func(session *Session) {
				// Begin's persistent lock directory is allowed; Ensure itself must not
				// create objects, refs, tracking config, a ledger or worktree metadata.
				paths, _ := repo.Paths()
				before := bootstrapSnapshot(t, paths.CommonDir)
				opened, err := session.Ensure(signature)
				if opened != nil {
					opened.Close()
					t.Fatal("opened missing local branch")
				}
				if err == nil {
					t.Fatal("silently initialized history")
				}
				for _, message := range []string{"local refs/heads/tasks is missing", "never probes or fetches", "git fetch REMOTE refs/heads/tasks:refs/heads/tasks", "Git/OpenSSH", "intentionally new independent", "git commit-tree", "git update-ref refs/heads/tasks", "tasks init"} {
					if !strings.Contains(err.Error(), message) {
						t.Fatalf("missing %q: %v", message, err)
					}
				}
				if !reflect.DeepEqual(before, bootstrapSnapshot(t, paths.CommonDir)) {
					t.Fatal("mutated uninitialized repository")
				}
				if _, err := repo.Reference(Branch); !git.HasCode(err, git.NotFound) {
					t.Fatalf("created branch: %v", err)
				}
				location, err := Location(repo)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(location); !os.IsNotExist(err) {
					t.Fatalf("created checkout: %v", err)
				}
			})
		})
	}
	if contacts.Load() != 0 {
		t.Fatalf("contacted remotes %d times", contacts.Load())
	}
}

func TestBootstrapDoesNotAdoptLocalTrackingRef(t *testing.T) {
	remote, _, _ := fixture(t)
	repo := emptyRepository(t)
	copyFixtureTasks(t, remote, repo)
	withSession(t, repo, func(session *Session) {
		paths, _ := repo.Paths()
		before := bootstrapSnapshot(t, paths.CommonDir)
		opened, err := session.Ensure(signature)
		if opened != nil {
			opened.Close()
			t.Fatal("adopted tracking history")
		}
		if err == nil || !strings.Contains(err.Error(), "local refs/heads/tasks is missing") {
			t.Fatalf("%v", err)
		}
		if !reflect.DeepEqual(before, bootstrapSnapshot(t, paths.CommonDir)) {
			t.Fatal("mutated tracking-only repository")
		}
	})
}

func bootstrapSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		snapshot[relative] = info.Mode().String()
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot[relative] += string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestBootstrapRejectsNonOrphanBeforeCreatingCheckout(t *testing.T) {
	repo := emptyRepository(t)
	main, err := repo.Reference("refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, repo, Branch, main.ID, git.ID{})
	withSession(t, repo, func(session *Session) {
		paths, _ := repo.Paths()
		before := bootstrapSnapshot(t, paths.CommonDir)
		opened, err := session.Ensure(signature)
		if opened != nil {
			opened.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "shares implementation history") {
			t.Fatalf("%v", err)
		}
		if !reflect.DeepEqual(before, bootstrapSnapshot(t, paths.CommonDir)) {
			t.Fatal("modified rejected history")
		}
	})
}
