package git

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func safetyTip(t *testing.T, repo *Repository, base ID, files map[string]string) ID {
	t.Helper()
	edits := []TreeEdit{}
	for path, content := range files {
		blob, err := repo.WriteBlob([]byte(content))
		if err != nil {
			t.Fatal(err)
		}
		edits = append(edits, TreeEdit{Path: path, ID: blob, Mode: 0100644})
	}
	tree, err := repo.EditTree(base, edits)
	if err != nil {
		t.Fatal(err)
	}
	var parents []ID
	if base != (ID{}) {
		parents = []ID{base}
	}
	id, err := repo.WriteCommit(tree, parents, testSignature, testSignature, "safety fixture")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func safetyFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			result[strings.TrimPrefix(path, root)] = string(readFile(t, path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNativeMergeNeverInterferesWithRerere(t *testing.T) {
	for _, mode := range []string{"enabled", "autoupdate", "injected", "custom-markers", "disabled", "implicit-cache"} {
		t.Run(mode, func(t *testing.T) {
			repo, root := fixture(t, false)
			attributes := ""
			if mode == "custom-markers" {
				attributes = "content.txt conflict-marker-size=13\n"
			}
			base := safetyTip(t, repo, ID{}, map[string]string{"content.txt": "base\n", ".gitattributes": attributes})
			left := safetyTip(t, repo, base, map[string]string{"content.txt": "left\n"})
			right := safetyTip(t, repo, base, map[string]string{"content.txt": "right\n"})
			publish(t, repo, "refs/heads/main", left, ID{})
			checkoutInitial(t, repo, left)
			paths, err := repo.Paths()
			if err != nil {
				t.Fatal(err)
			}
			if mode != "implicit-cache" {
				if err := repo.SetLocalConfig("rerere.enabled", fmt.Sprint(mode != "disabled")); err != nil {
					t.Fatal(err)
				}
				if err := repo.SetLocalConfig("rerere.autoupdate", fmt.Sprint(mode == "autoupdate")); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "injected" {
				t.Setenv("GIT_CONFIG_PARAMETERS", "'rerere.enabled'= 'rerere.autoupdate'='true'")
				opened, err := Open(root)
				if err != nil {
					t.Fatal(err)
				}
				defer opened.Close()
				repo = opened
			}
			cache := filepath.Join(paths.CommonDir, "rr-cache")
			id := sha1.Sum([]byte("left\n\x00right\n\x00"))
			entry := filepath.Join(cache, fmt.Sprintf("%x", id))
			if err := os.MkdirAll(entry, 0700); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(entry, "preimage"), []byte("<<<<<<<\nleft\n=======\nright\n>>>>>>>\n"))
			writeFile(t, filepath.Join(entry, "postimage"), []byte("tempting remembered resolution\n"))
			writeFile(t, filepath.Join(entry, "thisimage"), []byte("do not update\n"))
			writeFile(t, filepath.Join(paths.GitDir, "MERGE_RR"), []byte("existing manual rerere state\x00"))
			before, config := safetyFiles(t, cache), readFile(t, filepath.Join(paths.CommonDir, "config"))
			beforeIndex := readFile(t, filepath.Join(paths.GitDir, "index"))
			_, err = repo.MergeCleanIntoIndex(right)
			if !errors.Is(err, ErrMergeConflict) || strings.Contains(err.Error(), "preimage") {
				t.Fatalf("%v", err)
			}
			if !reflect.DeepEqual(before, safetyFiles(t, cache)) {
				t.Fatal("modified rerere cache")
			}
			if string(readFile(t, filepath.Join(paths.GitDir, "MERGE_RR"))) != "existing manual rerere state\x00" {
				t.Fatal("modified manual rerere state")
			}
			if !bytes.Equal(config, readFile(t, filepath.Join(paths.CommonDir, "config"))) {
				t.Fatal("modified user configuration")
			}
			if content := string(readFile(t, filepath.Join(root, "content.txt"))); content != "left\n" {
				t.Fatalf("modified worktree: %q", content)
			}
			if !bytes.Equal(beforeIndex, readFile(t, filepath.Join(paths.GitDir, "index"))) {
				t.Fatal("modified index")
			}
			expectRef(t, repo, "refs/heads/main", left)
		})
	}
}

func TestNativeConflictDoesNotCreateRerereCache(t *testing.T) {
	t.Parallel()
	repo, _ := fixture(t, false)
	base := commit(t, repo, "base\n")
	left, right := commit(t, repo, "left\n", base), commit(t, repo, "right\n", base)
	publish(t, repo, "refs/heads/main", left, ID{})
	checkoutInitial(t, repo, left)
	if err := repo.SetLocalConfig("rerere.enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.MergeCleanIntoIndex(right); !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	paths, _ := repo.Paths()
	for _, path := range []string{filepath.Join(paths.CommonDir, "rr-cache"), filepath.Join(paths.GitDir, "MERGE_RR")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("created rerere state: %s: %v", path, err)
		}
	}
}
