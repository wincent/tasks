package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/wincent/tasks/internal/git"
)

func TestNumberingReadsFetchedMergedAwayHistoryWithoutAdvancingLocal(t *testing.T) {
	t.Parallel()
	for _, checkout := range []bool{false, true} {
		t.Run(map[bool]string{false: "without-checkout", true: "with-checkout"}[checkout], func(t *testing.T) {
			t.Parallel()
			remote, remoteStore, _ := fixture(t)
			original, err := remoteStore.Repository.Head()
			if err != nil {
				t.Fatal(err)
			}
			blob, err := remote.WriteBlob([]byte("historical"))
			if err != nil {
				t.Fatal(err)
			}
			tree, err := remote.EditTree(original.ID, []git.TreeEdit{{Path: "EXAMPLE-71 Side.md", ID: blob, Mode: 0100644}, {Path: "nested/EXAMPLE-99 Ignore.md", ID: blob, Mode: 0100644}})
			if err != nil {
				t.Fatal(err)
			}
			side, err := remote.WriteCommit(tree, []git.ID{original.ID}, signature, signature, "side\n")
			if err != nil {
				t.Fatal(err)
			}
			empty, err := remote.WriteTree(nil)
			if err != nil {
				t.Fatal(err)
			}
			merged, err := remote.WriteCommit(empty, []git.ID{original.ID, side}, signature, signature, "merge away task\n")
			if err != nil {
				t.Fatal(err)
			}
			publish(t, remote, Branch, merged, original.ID)
			local := emptyRepository(t)
			remotePaths, _ := remote.Paths()
			if err := local.SetLocalConfig("remote.origin.url", remotePaths.WorkDir); err != nil {
				t.Fatal(err)
			}
			copyFixtureTasks(t, remote, local)
			var localTip git.ID
			if checkout {
				publish(t, local, Branch, merged, git.ID{})
				withSession(t, local, func(s *Session) {
					store, err := s.Ensure()
					if err != nil {
						t.Fatal(err)
					}
					defer store.Close()
					head, _ := store.Repository.Head()
					localTip = head.ID
				})
			}
			// Fetch later deletion-only history; ledger must visit all parents, not
			// just paths still visible in either local or remote tip trees.
			tree, err = remote.EditTree(merged, []git.TreeEdit{{Path: "EXAMPLE-82 Later.md", ID: blob, Mode: 0100644}})
			if err != nil {
				t.Fatal(err)
			}
			later, err := remote.WriteCommit(tree, []git.ID{merged}, signature, signature, "later\n")
			if err != nil {
				t.Fatal(err)
			}
			deleted, err := remote.WriteCommit(empty, []git.ID{later}, signature, signature, "delete\n")
			if err != nil {
				t.Fatal(err)
			}
			publish(t, remote, Branch, deleted, merged)
			copyFixtureTasks(t, remote, local)
			withSession(t, local, func(s *Session) {
				n, err := s.NextTaskNumber("EXAMPLE")
				if err != nil || n != 83 {
					t.Fatalf("%d %v", n, err)
				}
			})
			ref, err := local.Reference(Branch)
			if checkout {
				if err != nil || ref.ID != localTip {
					t.Fatalf("local branch advanced: %+v %v", ref, err)
				}
			} else if !git.HasCode(err, git.NotFound) {
				t.Fatalf("created local branch: %v", err)
			}
		})
	}
}

func TestEnsureObstructionAndFreshRevalidation(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"file", "directory", "symlink", "foreign", "elsewhere"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			repo := emptyRepository(t)
			location := testLocation(t, repo)
			switch kind {
			case "file":
				mustWrite(t, location, []byte("precious"))
			case "directory":
				if err := os.Mkdir(location, 0700); err != nil {
					t.Fatal(err)
				}
				mustWrite(t, filepath.Join(location, "precious"), []byte("precious"))
			case "symlink":
				target := t.TempDir()
				mustWrite(t, filepath.Join(target, "precious"), []byte("precious"))
				if err := os.Symlink(target, location); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				foreign, err := git.Init(location, "", false)
				if err != nil {
					t.Fatal(err)
				}
				foreign.Close()
				mustWrite(t, filepath.Join(location, "precious"), []byte("precious"))
			case "elsewhere":
				tree, err := repo.WriteTree(nil)
				if err != nil {
					t.Fatal(err)
				}
				id, err := repo.WriteCommit(tree, nil, signature, signature, "orphan\n")
				if err != nil {
					t.Fatal(err)
				}
				publish(t, repo, Branch, id, git.ID{})
				if err := repo.AddWorktree("other-tasks", filepath.Join(t.TempDir(), "elsewhere"), Branch); err != nil {
					t.Fatal(err)
				}
			}
			withSession(t, repo, func(s *Session) {
				opened, err := s.Ensure()
				if opened != nil {
					opened.Close()
				}
				if err == nil {
					t.Fatal("accepted obstruction")
				}
			})
			if kind == "file" {
				if string(mustRead(t, location)) != "precious" {
					t.Fatal("changed obstruction")
				}
			} else if kind != "elsewhere" {
				if string(mustRead(t, filepath.Join(location, "precious"))) != "precious" {
					t.Fatal("changed obstruction")
				}
			}
		})
	}
	repo, store, _ := fixture(t)
	paths, _ := repo.Paths()
	exclude := filepath.Join(paths.CommonDir, "info", "exclude")
	mustWrite(t, exclude, []byte("user-rule"))
	withSession(t, repo, func(s *Session) {
		opened, err := s.Ensure()
		if err != nil {
			t.Fatal(err)
		}
		opened.Close()
		if !bytes.Equal(mustRead(t, exclude), []byte("user-rule\n/tasks/\n")) {
			t.Fatal("lost excludes")
		}
		main, err := repo.ResolveCommit("refs/heads/main")
		if err != nil {
			t.Fatal(err)
		}
		head, err := store.Repository.Head()
		if err != nil {
			t.Fatal(err)
		}
		publish(t, repo, Branch, main, head.ID)
		opened, err = s.Ensure()
		if opened != nil {
			opened.Close()
		}
		if err == nil {
			t.Fatal("cached history validity")
		}
	})
}
