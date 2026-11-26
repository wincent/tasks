package git

import (
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Include directories and mtimes: an empty object fanout directory or freshened
// existing object is a mutation too. Access times are not a write guarantee.
type mergePreflightFile struct {
	Mode     os.FileMode
	Modified int64
	Content  string
}

func mergePreflightSnapshot(t *testing.T, roots ...string) map[string]mergePreflightFile {
	t.Helper()
	result := map[string]mergePreflightFile{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			value := mergePreflightFile{Mode: info.Mode(), Modified: info.ModTime().UnixNano()}
			if info.Mode()&os.ModeSymlink != 0 {
				value.Content, err = os.Readlink(path)
			} else if !entry.IsDir() {
				var data []byte
				data, err = os.ReadFile(path)
				value.Content = string(data)
			}
			result[path] = value
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func mergePreflightBlobID(content string) ID {
	return ID(sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(content), content))))
}

func TestNativeMergePreflightQuarantinesEarlierBlobs(t *testing.T) {
	t.Parallel()
	for _, layout := range []string{"loose", "packed", "alternate"} {
		for _, rejection := range []string{"driver", "filter", "existing-object"} {
			t.Run(layout+"/"+rejection, func(t *testing.T) {
				t.Parallel()
				repo, root := fixture(t, false)
				attributes := "z.txt merge=custom\n"
				if rejection != "driver" {
					attributes = "z.txt filter=custom\n"
				}
				base := safetyTip(t, repo, ID{}, map[string]string{".gitattributes": attributes, "a.txt": "one\ntwo\nthree\nfour\nfive\n", "z.txt": "one\ntwo\nthree\nfour\nfive\n"})
				left := safetyTip(t, repo, base, map[string]string{"a.txt": "left\ntwo\nthree\nfour\nfive\n", "z.txt": "left\ntwo\nthree\nfour\nfive\n"})
				right := safetyTip(t, repo, base, map[string]string{"a.txt": "one\ntwo\nthree\nfour\nright\n", "z.txt": "one\ntwo\nthree\nfour\nright\n"})
				publish(t, repo, "refs/heads/main", left, ID{})
				publish(t, repo, "refs/heads/source", right, ID{})
				checkoutInitial(t, repo, left)
				key, want := "merge.custom.driver", "external merge driver"
				if rejection != "driver" {
					key, want = "filter.custom.smudge", "external filter"
				}
				if err := repo.SetLocalConfig(key, "never-execute-this"); err != nil {
					t.Fatal(err)
				}
				merged := "left\ntwo\nthree\nfour\nright\n"
				id := mergePreflightBlobID(merged)
				if rejection == "existing-object" {
					if _, err := repo.WriteBlob([]byte(merged)); err != nil {
						t.Fatal(err)
					}
				} else if _, err := repo.Blob(id); err == nil {
					t.Fatal("fixture already contains merged blob")
				}
				paths, err := repo.Paths()
				if err != nil {
					t.Fatal(err)
				}
				roots := []string{root}
				if layout == "packed" {
					repo.Close()
					mergePreflightPack(t, filepath.Join(paths.CommonDir, "objects"))
					repo, err = Open(root)
					if err != nil {
						t.Fatal(err)
					}
					defer repo.Close()
				}
				if layout == "alternate" {
					// Reopen cold, so all object reads must traverse the alternate.
					repo.Close()
					alternate := filepath.Join(t.TempDir(), "objects")
					objects := filepath.Join(paths.CommonDir, "objects")
					if err := os.Rename(objects, alternate); err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(filepath.Join(objects, "info"), 0700); err != nil {
						t.Fatal(err)
					}
					writeFile(t, filepath.Join(objects, "info", "alternates"), []byte(alternate+"\n"))
					roots = append(roots, alternate)
					repo, err = Open(root)
					if err != nil {
						t.Fatal(err)
					}
					defer repo.Close()
				}
				before := mergePreflightSnapshot(t, roots...)
				for attempt := 0; attempt < 2; attempt++ {
					_, err := repo.MergeCleanIntoIndex(right)
					if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "z.txt") {
						t.Fatalf("%v", err)
					}
					if after := mergePreflightSnapshot(t, roots...); !reflect.DeepEqual(before, after) {
						t.Fatal("rejection changed ODB/index/worktree/refs/operation state")
					}
					if rejection != "existing-object" {
						if _, err := repo.Blob(id); err == nil {
							t.Fatal("quarantined blob leaked into live ODB/cache")
						}
					}
				}
				expectRef(t, repo, "refs/heads/main", left)
				expectRef(t, repo, "refs/heads/source", right)
				// The original ODB/config remain usable after rejection. A later
				// ordinary merge persists its blobs instead of relying on a cache.
				if err := repo.SetLocalConfig(key, ""); err != nil {
					t.Fatal(err)
				}
				if _, err = repo.MergeCleanIntoIndex(right); err != nil {
					t.Fatalf("%v", err)
				}
				opened, err := Open(root)
				if err != nil {
					t.Fatal(err)
				}
				defer opened.Close()
				if got, err := opened.Blob(id); err != nil || string(got) != merged {
					t.Fatalf("persisted merged blob: %q %v", got, err)
				}
			})
		}
	}
}

func TestNativeMergePreflightDefaultAndBuiltinDrivers(t *testing.T) {
	t.Parallel()
	for _, driver := range []string{"custom", "text", "binary", "union"} {
		for _, attribute := range []string{"named", "default", "boolean", "one-sided"} {
			t.Run(driver+"/"+attribute, func(t *testing.T) {
				t.Parallel()
				repo, root := fixture(t, false)
				attributes := "content merge=" + driver + "\n"
				if attribute == "default" {
					attributes = ""
				}
				if attribute == "boolean" {
					attributes = "content merge\n"
				}
				base := safetyTip(t, repo, ID{}, map[string]string{".gitattributes": attributes, "content": "one\ntwo\nthree\nfour\nfive\n"})
				leftFiles := map[string]string{"content": "left\ntwo\nthree\nfour\nfive\n"}
				if attribute == "one-sided" {
					leftFiles = map[string]string{"unrelated": "left\n"}
				}
				left := safetyTip(t, repo, base, leftFiles)
				right := safetyTip(t, repo, base, map[string]string{"content": "one\ntwo\nthree\nfour\nright\n"})
				publish(t, repo, "refs/heads/main", left, ID{})
				checkoutInitial(t, repo, left)
				if err := repo.SetLocalConfig("merge."+driver+".driver", "never-execute-this"); err != nil {
					t.Fatal(err)
				}
				if err := repo.SetLocalConfig("merge.default", driver); err != nil {
					t.Fatal(err)
				}
				before := mergePreflightSnapshot(t, root)
				_, err := repo.MergeCleanIntoIndex(right)
				if attribute == "boolean" || attribute == "one-sided" {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					if err == nil || !strings.Contains(err.Error(), "external merge driver") {
						t.Fatalf("%v", err)
					}
					if !reflect.DeepEqual(before, mergePreflightSnapshot(t, root)) {
						t.Fatal("driver refusal mutated state")
					}
				}
			})
		}
	}
}

func TestNativeMergePreflightUsesRenameDriverPath(t *testing.T) {
	t.Parallel()
	repo, root := fixture(t, false)
	content := strings.Repeat("stable line\n", 30)
	base := safetyTip(t, repo, ID{}, map[string]string{".gitattributes": "renamed merge=custom\n", "original": content})
	left := safetyTip(t, repo, base, map[string]string{"original": "left\n" + content})
	blob, err := repo.WriteBlob([]byte(content + "right\n"))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.EditTree(base, []TreeEdit{{Path: "original"}, {Path: "renamed", ID: blob, Mode: 0100644}})
	if err != nil {
		t.Fatal(err)
	}
	right, err := repo.WriteCommit(tree, []ID{base}, testSignature, testSignature, "rename")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, repo, "refs/heads/main", left, ID{})
	checkoutInitial(t, repo, left)
	if err := repo.SetLocalConfig("merge.custom.driver", "never-execute-this"); err != nil {
		t.Fatal(err)
	}
	before := mergePreflightSnapshot(t, root)
	_, err = repo.MergeCleanIntoIndex(right)
	if err == nil || !strings.Contains(err.Error(), "external merge driver") {
		t.Fatalf("%v", err)
	}
	if !reflect.DeepEqual(before, mergePreflightSnapshot(t, root)) {
		t.Fatal("rename rejection mutated state")
	}
}

func TestNativeMergePreflightTargetNestedAttributes(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"new-filter", "nested-override", "merged-attributes"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			repo, root := fixture(t, false)
			baseFiles := map[string]string{".gitattributes": "* -filter\n", "dir/content": "base\n", "a.txt": "one\ntwo\nthree\nfour\nfive\n"}
			if scenario == "nested-override" {
				baseFiles[".gitattributes"] = "* filter=custom\n"
				baseFiles["dir/.gitattributes"] = "* -filter\n"
			}
			if scenario == "merged-attributes" {
				baseFiles["dir/.gitattributes"] = "# one\n# two\n# three\n# four\n# five\n"
			}
			base := safetyTip(t, repo, ID{}, baseFiles)
			leftFiles := map[string]string{"a.txt": "left\ntwo\nthree\nfour\nfive\n"}
			rightFiles := map[string]string{"dir/content": "right\n", "a.txt": "one\ntwo\nthree\nfour\nright\n"}
			if scenario == "new-filter" {
				rightFiles["dir/.gitattributes"] = "content filter=custom\n"
			}
			if scenario == "merged-attributes" {
				leftFiles["dir/.gitattributes"] = "# left\n# two\n# three\n# four\n# five\n"
				rightFiles["dir/.gitattributes"] = "# one\n# two\n# three\n# four\ncontent filter=custom\n"
			}
			// Keep other paths native in the nested override success case.
			if scenario == "nested-override" {
				delete(leftFiles, "a.txt")
				leftFiles["dir/other"] = "left\n"
				delete(rightFiles, "a.txt")
			}
			left, right := safetyTip(t, repo, base, leftFiles), safetyTip(t, repo, base, rightFiles)
			publish(t, repo, "refs/heads/main", left, ID{})
			checkoutInitial(t, repo, left)
			if err := repo.SetLocalConfig("filter.custom.smudge", "never-execute-this"); err != nil {
				t.Fatal(err)
			}
			before := mergePreflightSnapshot(t, root)
			_, err := repo.MergeCleanIntoIndex(right)
			if scenario == "nested-override" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "external filter") || !strings.Contains(err.Error(), "dir/content") {
					t.Fatalf("%v", err)
				}
				if !reflect.DeepEqual(before, mergePreflightSnapshot(t, root)) {
					t.Fatal("target attribute refusal mutated state")
				}
			}
		})
	}
}

func TestCleanupMergeQuarantinesTreeAndBlob(t *testing.T) {
	t.Parallel()
	repo, root := fixture(t, false)
	base := safetyTip(t, repo, ID{}, map[string]string{"file": "one\ntwo\nthree\nfour\nfive\n"})
	ours := safetyTip(t, repo, base, map[string]string{"file": "left\ntwo\nthree\nfour\nfive\n"})
	theirs := safetyTip(t, repo, base, map[string]string{"file": "one\ntwo\nthree\nfour\nright\n"})
	publish(t, repo, "refs/heads/main", ours, ID{})
	publish(t, repo, "refs/heads/source", theirs, ID{})
	checkoutInitial(t, repo, ours)
	before := mergePreflightSnapshot(t, root)
	if same, err := repo.MergeWouldKeepTree(theirs, ours, "refs/heads/main"); same || err != nil {
		t.Fatalf("expected a clean but different result, got %v, %v", same, err)
	}
	if after := mergePreflightSnapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("simulation changed ODB/index/worktree/refs/operation state")
	}
	id := mergePreflightBlobID("left\ntwo\nthree\nfour\nright\n")
	if _, err := repo.Blob(id); !HasCode(err, NotFound) {
		t.Fatalf("merged blob accessible in live ODB: %v", err)
	}
}
