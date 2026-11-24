package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func archiveMoveFixture(t *testing.T, archive bool) (root string, moves []archiveMove, created []string, originals []os.FileInfo) {
	t.Helper()
	root = t.TempDir()
	for i, name := range []string{"EXAMPLE-1 One.md", "EXAMPLE-1001 Two.md", "EXAMPLE-2 Three.md"} {
		bucket := []string{"archive/0", "archive/1000", "archive/0"}[i]
		source, destination := name, filepath.Join(bucket, name)
		if !archive {
			source, destination = destination, source
			if err := os.MkdirAll(filepath.Join(root, bucket), 0755); err != nil {
				t.Fatal(err)
			}
		}
		move := archiveMove{source: source, destination: destination}
		if err := metadataParents(root, move.directory(), "archive", &created); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, source)
		mustWrite(t, path, []byte(name))
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		moves = append(moves, move)
		originals = append(originals, info)
	}
	return root, moves, created, originals
}

func assertArchiveInode(t *testing.T, path string, want os.FileInfo) {
	t.Helper()
	got, err := os.Lstat(path)
	if want == nil {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: want no file, got %v (%v)", path, got, err)
		}
	} else if err != nil || !os.SameFile(got, want) {
		t.Fatalf("%s: want original inode, got %v (%v)", path, got, err)
	}
}

func TestInstallArchiveMovesBatchesDirectorySyncs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		archive   bool
		wantSyncs []string
		destSyncs int
	}{
		{"existing buckets", true, []string{"archive/0", "archive/1000", "."}, 2},
		{"new buckets", true, []string{"archive/0", "archive/1000", "archive", ".", "."}, 4},
		{"unarchive", false, []string{".", "archive/0", "archive/1000"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, moves, created, originals := archiveMoveFixture(t, tc.archive)
			if tc.name == "existing buckets" {
				created = nil
			}
			var synced []string
			_, err := installArchiveMoves(root, moves, created, func(dir string) error {
				for i, move := range moves {
					assertArchiveInode(t, filepath.Join(root, move.destination), originals[i])
					var wantSource os.FileInfo
					if len(synced) < tc.destSyncs {
						wantSource = originals[i]
					}
					assertArchiveInode(t, filepath.Join(root, move.source), wantSource)
				}
				rel, err := filepath.Rel(root, dir)
				if err != nil {
					t.Fatal(err)
				}
				synced = append(synced, rel)
				return nil
			})
			if err != nil || !slices.Equal(synced, tc.wantSyncs) {
				t.Fatalf("directory syncs = %v, want %v (err: %v)", synced, tc.wantSyncs, err)
			}
		})
	}
}

func TestInstallArchiveMovesRollback(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"link", "destination sync", "source changed", "source sync", "rollback conflict"} {
		t.Run(failure, func(t *testing.T) {
			root, moves, _, originals := archiveMoveFixture(t, true)
			last := len(moves) - 1
			source := filepath.Join(root, moves[last].source)
			destination := filepath.Join(root, moves[last].destination)
			if failure == "link" {
				mustWrite(t, destination, []byte("existing destination"))
			}
			syncErr := errors.New("injected sync failure")
			links, err := installArchiveMoves(root, moves, nil, func(dir string) error {
				switch {
				case failure == "link":
					t.Fatal("synced directories before linking all destinations")
				case failure == "destination sync" && dir == filepath.Join(root, "archive/1000"):
					return syncErr
				case failure == "source changed" && dir == filepath.Join(root, "archive/1000"):
					if err := os.Rename(source, source+".original"); err != nil {
						t.Fatal(err)
					}
					mustWrite(t, source, []byte("replacement source"))
				case dir == root && (failure == "source sync" || failure == "rollback conflict"):
					if failure == "rollback conflict" {
						mustWrite(t, source, []byte("replacement source"))
					}
					return syncErr
				}
				return nil
			})
			switch failure {
			case "link":
				if !errors.Is(err, os.ErrExist) || len(links) != last {
					t.Fatalf("want partial link failure, got %d links (%v)", len(links), err)
				}
			case "source changed":
				if err == nil || !strings.Contains(err.Error(), "source changed") {
					t.Fatalf("want source identity error, got %v", err)
				}
			default:
				if !errors.Is(err, syncErr) {
					t.Fatalf("want sync failure, got %v", err)
				}
			}
			for i := len(links) - 1; i >= 0; i-- {
				err := links[i].rollback()
				if failure == "rollback conflict" && i == last {
					if !errors.Is(err, os.ErrExist) {
						t.Fatalf("want rollback conflict, got %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
			for i, move := range moves {
				from, to := filepath.Join(root, move.source), filepath.Join(root, move.destination)
				if i == last && (failure == "source changed" || failure == "rollback conflict") {
					if got := string(mustRead(t, from)); got != "replacement source" {
						t.Fatalf("replacement source changed: %q", got)
					}
				} else {
					assertArchiveInode(t, from, originals[i])
				}
				switch {
				case i == last && failure == "link":
					if got := string(mustRead(t, to)); got != "existing destination" {
						t.Fatalf("existing destination changed: %q", got)
					}
				case i == last && failure == "rollback conflict":
					assertArchiveInode(t, to, originals[i])
				default:
					assertArchiveInode(t, to, nil)
				}
			}
		})
	}
}
