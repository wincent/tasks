package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wincent/tasks/internal/git"
)

func TestBootstrapUnbornAndLayoutMarkerIsolation(t *testing.T) {
	for _, layout := range []string{"ordinary", "bare-container", "separate"} {
		t.Run(layout, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "odd\tquote'\nroot")
			path, workdir, bare := root, "", false
			if layout == "bare-container" {
				path = filepath.Join(root, ".git")
				bare = true
			}
			if layout == "separate" {
				path = filepath.Join(root, "private")
				workdir = filepath.Join(root, "checkout")
			}
			repo, err := git.Init(path, workdir, bare)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			seedLocalTasks(t, repo)
			withSession(t, repo, func(session *Session) {
				opened, err := session.Ensure(signature)
				if err != nil {
					t.Fatal(err)
				}
				defer opened.Close()
				paths, _ := repo.Paths()
				for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer", "BISECT_START"} {
					private := filepath.Join(opened.Paths.GitDir, marker)
					mustWrite(t, filepath.Join(paths.GitDir, marker), []byte("another checkout"))
					if err := opened.RequireClean("EXAMPLE-1 task.md"); err != nil {
						t.Fatalf("foreign %s contaminated tasks: %v", marker, err)
					}
					for range 2 {
						mustWrite(t, private, []byte("local operation"))
						if err := opened.RequireClean("EXAMPLE-1 task.md"); err == nil {
							t.Fatalf("ignored %s", marker)
						}
						if err := os.Remove(private); err != nil {
							t.Fatal(err)
						}
						if err := opened.RequireClean("EXAMPLE-1 task.md"); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Remove(filepath.Join(paths.GitDir, marker)); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := repo.Reference("refs/heads/main"); !git.HasCode(err, git.NotFound) {
					t.Fatalf("created implementation history: %v", err)
				}
			})
		})
	}
}

func TestNumberingAccountsForIndexAndWorktree(t *testing.T) {
	repo, store, _ := fixture(t)
	blob, err := repo.WriteBlob([]byte("Status: open\n"))
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(store.Paths.GitDir, "index")
	isolated := filepath.Join(store.Paths.GitDir, "staged-index")
	mustWrite(t, isolated, mustRead(t, index))
	if err := store.Repository.EditIndexFile(isolated, git.ID{}, []git.TreeEdit{{Path: "EXAMPLE-12 Staged.md", ID: blob, Mode: 0100644}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(isolated, index); err != nil {
		t.Fatal(err)
	}
	manual := filepath.Join(store.Path, "EXAMPLE-20 Manual.md")
	mustWrite(t, manual, []byte("Status: open\n"))
	withSession(t, repo, func(session *Session) {
		if n, err := session.NextTaskNumber("EXAMPLE"); err != nil || n != 21 {
			t.Fatalf("next number %d %v", n, err)
		}
		if err := os.Remove(manual); err != nil {
			t.Fatal(err)
		}
		if n, err := session.NextTaskNumber("EXAMPLE"); err != nil || n != 13 {
			t.Fatalf("next number %d %v", n, err)
		}
	})
}

func TestConcurrentMetadataCommitsDoNotLoseUpdates(t *testing.T) {
	repo, store, _ := fixture(t)
	var wg sync.WaitGroup
	failures := make(chan error, 6)
	for i := range 6 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "EXAMPLE-" + string(rune('1'+i)) + " concurrent.md"
			_, err := CommitFiles(context.Background(), repo, map[string][]byte{name: []byte(name)}, options())
			failures <- err
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	head, err := store.Repository.Head()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.Tree(head.ID)
	if err != nil || len(tree) != 6 {
		t.Fatalf("lost commits: %+v %v", tree, err)
	}
}

func TestBootstrapPreservesRemoteConfigurationAndLocalHistory(t *testing.T) {
	remote, _, _ := fixture(t)
	paths, _ := remote.Paths()
	repo := emptyRepository(t)
	local := seedLocalTasks(t, repo)
	for key, value := range map[string]string{"remote.origin.url": paths.WorkDir, "remote.origin.fetch": "+refs/heads/main:refs/remotes/origin/main"} {
		if err := repo.SetLocalConfig(key, value); err != nil {
			t.Fatal(err)
		}
	}
	configPaths, _ := repo.Paths()
	configPath := filepath.Join(configPaths.CommonDir, "config")
	config := mustRead(t, configPath)
	for i := range 2 {
		withSession(t, repo, func(session *Session) {
			opened, err := session.Ensure(signature)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			head, err := opened.Repository.Head()
			if err != nil || head.ID != local {
				t.Fatalf("advanced existing local tasks: %v %v", head, err)
			}
		})
		if !bytes.Equal(config, mustRead(t, configPath)) {
			t.Fatal("changed existing remote fetch configuration")
		}
		if i == 0 {
			if _, err := CommitFiles(context.Background(), remote, map[string][]byte{"EXAMPLE-40 Remote.md": []byte("new remote")}, options()); err != nil {
				t.Fatal(err)
			}
		}
	}
}
