package git

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNativeExternalFilterPreflightPreservesAffectedState(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"clean", "smudge", "process"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			repo, root := fixture(t, false)
			base := safetyTip(t, repo, ID{}, map[string]string{"content.txt": "base\n", "unrelated": "untouched\n", ".gitattributes": "content.txt filter=custom\n"})
			next := safetyTip(t, repo, base, map[string]string{".gitattributes": "content.txt filter=custom\n", "content.txt": "next\n"})
			publish(t, repo, "refs/heads/main", base, ID{})
			publish(t, repo, "refs/heads/next", next, ID{})
			if err := repo.CheckoutMetadata(ID{}, base); err != nil {
				t.Fatal(err)
			}
			if err := repo.SetLocalConfig("filter.custom."+command, "never-execute-this"); err != nil {
				t.Fatal(err)
			}
			paths, _ := repo.Paths()
			before := safetyFiles(t, root)
			err := repo.Checkout(base, next)
			if err == nil || !strings.Contains(err.Error(), "external filter") || !strings.Contains(err.Error(), "content.txt") {
				t.Fatalf("%v", err)
			}
			if !reflect.DeepEqual(before, safetyFiles(t, root)) {
				t.Fatal("rejected checkout modified state")
			}
			destination := filepath.Join(t.TempDir(), "worktree")
			// New worktrees only smudge; clean-only filters are not invoked there.
			if command != "clean" {
				err = repo.AddWorktree("filtered", destination, "refs/heads/next")
				if err == nil || !strings.Contains(err.Error(), "external filter") {
					t.Fatalf("%v", err)
				}
				if _, err := os.Lstat(destination); !os.IsNotExist(err) {
					t.Fatalf("created affected worktree: %v", err)
				}
				if _, err := os.Lstat(filepath.Join(paths.CommonDir, "worktrees", "filtered")); !os.IsNotExist(err) {
					t.Fatalf("created worktree registration: %v", err)
				}
			}
			expectRef(t, repo, "refs/heads/main", base)
		})
	}
}

func TestNativeUnrelatedFiltersAndNativeLineEndings(t *testing.T) {
	t.Parallel()
	repo, root := fixture(t, false)
	// A fixture-specific driver avoids inheriting an installed Git LFS command
	// before the baseline checkout. The operation under test enables it below.
	base := safetyTip(t, repo, ID{}, map[string]string{"content.txt": "base\n", "large.bin": "pointer\n", ".gitattributes": "large.bin filter=tasktool-fixture-lfs\ncontent.txt text eol=crlf\n"})
	publish(t, repo, "refs/heads/main", base, ID{})
	if err := repo.CheckoutMetadata(ID{}, base); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"filter.tasktool-fixture-lfs.clean", "filter.tasktool-fixture-lfs.smudge", "filter.tasktool-fixture-lfs.process"} {
		if err := repo.SetLocalConfig(key, "external-lfs"); err != nil {
			t.Fatal(err)
		}
	}
	next := safetyTip(t, repo, base, map[string]string{"content.txt": "next\n"})
	if err := repo.Checkout(base, next); err != nil {
		t.Fatal(err)
	}
	if got := string(readFile(t, filepath.Join(root, "content.txt"))); got != "next\r\n" {
		t.Fatalf("native smudge bypassed: %q", got)
	}
	if err := repo.ValidateWorktreePaths([]string{"content.txt"}); err != nil {
		t.Fatal(err)
	}
	blob, err := repo.WriteWorktreeBlob("content.txt", []byte("clean\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := repo.Blob(blob)
	if err != nil || string(data) != "clean\n" {
		t.Fatalf("native clean: %q %v", data, err)
	}
	before := safetyFiles(t, root)
	if err := repo.ValidateWorktreePaths([]string{"content.txt", "large.bin"}); err == nil || !strings.Contains(err.Error(), "large.bin") {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, safetyFiles(t, root)) {
		t.Fatal("selected-path validation wrote state")
	}
}

func TestNativeExternalMergeDriverOnlyRejectsRequiredOperation(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"conflict", "clean-content-merge", "unrelated", "one-sided"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			repo, root := fixture(t, false)
			base := safetyTip(t, repo, ID{}, map[string]string{"content.txt": "one\ntwo\nthree\n", "other": "base\n", ".gitattributes": "content.txt merge=custom\n"})
			leftFiles, rightFiles := map[string]string{"content.txt": "left\ntwo\nthree\n"}, map[string]string{"content.txt": "right\ntwo\nthree\n"}
			switch scenario {
			case "clean-content-merge":
				rightFiles["content.txt"] = "one\ntwo\nright\n"
			case "unrelated":
				leftFiles = map[string]string{"other": "left\n"}
				rightFiles = map[string]string{"new": "right\n"}
			case "one-sided":
				leftFiles = map[string]string{"other": "left\n"}
			}
			left, right := safetyTip(t, repo, base, leftFiles), safetyTip(t, repo, base, rightFiles)
			publish(t, repo, "refs/heads/main", left, ID{})
			if err := repo.CheckoutMetadata(ID{}, left); err != nil {
				t.Fatal(err)
			}
			if err := repo.SetLocalConfig("merge.custom.driver", "external-merge %O %A %B"); err != nil {
				t.Fatal(err)
			}
			paths, _ := repo.Paths()
			index := readFile(t, filepath.Join(paths.GitDir, "index"))
			content := readFile(t, filepath.Join(root, "content.txt"))
			_, err := repo.MergeIntoIndex(right)
			reject := scenario == "conflict" || scenario == "clean-content-merge"
			if reject {
				if err == nil || !strings.Contains(err.Error(), "external merge driver") || !strings.Contains(err.Error(), "content.txt") {
					t.Fatalf("%v", err)
				}
				if !bytes.Equal(index, readFile(t, filepath.Join(paths.GitDir, "index"))) || !bytes.Equal(content, readFile(t, filepath.Join(root, "content.txt"))) {
					t.Fatal("rejected driver changed affected state")
				}
				if _, err := os.Lstat(filepath.Join(paths.GitDir, "MERGE_HEAD")); !os.IsNotExist(err) {
					t.Fatalf("created merge state: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			expectRef(t, repo, "refs/heads/main", left)
		})
	}
}

func TestNativeUnsupportedRepositoryFormatsAreNonmutating(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ key, value, want string }{{"extensions.objectformat", "sha256", "SHA-256"}, {"extensions.refstorage", "reftable", "reftable"}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Parallel()
			repo, root := fixture(t, false)
			if err := repo.SetLocalConfig("core.repositoryformatversion", "1"); err != nil {
				t.Fatal(err)
			}
			if err := repo.SetLocalConfig(tc.key, tc.value); err != nil {
				t.Fatal(err)
			}
			repo.Close()
			before := safetyFiles(t, root)
			opened, err := Open(root)
			if opened != nil {
				opened.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v", err)
			}
			if !reflect.DeepEqual(before, safetyFiles(t, root)) {
				t.Fatal("modified unsupported repository")
			}
		})
	}
}
