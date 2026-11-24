package state

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestPermanentLockAndCancellation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "lock")
	lock, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := TryLock(path); !errors.Is(err, ErrBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("contended lock: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if other, err := Acquire(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("cancellation: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("lock inode replaced: %v", err)
	}
}

func TestLockRejectsSymlinkAndDirectory(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("precious"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "symlink")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, directory} {
		if lock, err := TryLock(path); err == nil {
			lock.Close()
			t.Fatalf("locked nonregular path: %s", path)
		}
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "precious" {
		t.Fatalf("changed target: %q %v", data, err)
	}
}

func TestAtomicStateReplacement(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	for _, data := range [][]byte{[]byte("first"), {}, []byte("second\n")} {
		if err := AtomicWrite(path, data); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("state: %q %v", got, err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("leaked temporary: %v %v", entries, err)
	}
}

func TestBatchDeduplicatesDirectoriesAndInstallsFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	dirC := filepath.Join(root, "c")
	for _, dir := range []string{dirA, dirB, dirC} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	existing := filepath.Join(dirC, "existing.txt")
	if err := os.WriteFile(existing, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}

	var batch Batch
	if err := batch.AtomicWrite(filepath.Join(dirA, "one.txt"), []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := batch.AtomicWrite(filepath.Join(dirA, "two.txt"), []byte("two")); err != nil {
		t.Fatal(err)
	}
	installed, err := batch.AtomicCreate(filepath.Join(dirB, "three.txt"), []byte("three"))
	if err != nil || !installed {
		t.Fatalf("AtomicCreate new file: installed=%v err=%v", installed, err)
	}
	installed, err = batch.AtomicCreate(filepath.Join(dirB, "four.txt"), []byte("four"))
	if err != nil || !installed {
		t.Fatalf("AtomicCreate second file: installed=%v err=%v", installed, err)
	}
	installed, err = batch.AtomicCreate(existing, []byte("overwrite"))
	if !errors.Is(err, os.ErrExist) || installed {
		t.Fatalf("AtomicCreate existing file: installed=%v err=%v", installed, err)
	}
	if got, err := os.ReadFile(existing); err != nil || string(got) != "original" {
		t.Fatalf("existing file changed: %q %v", got, err)
	}

	wantDirs := []string{dirA, dirB}
	if !slices.Equal(batch.directories, wantDirs) {
		t.Fatalf("recorded directories = %v, want %v", batch.directories, wantDirs)
	}
	if err := batch.SyncDirectories(); err != nil {
		t.Fatal(err)
	}
	if len(batch.directories) != 0 {
		t.Fatalf("directories not cleared after sync: %v", batch.directories)
	}
	for path, want := range map[string]string{
		filepath.Join(dirA, "one.txt"):   "one",
		filepath.Join(dirA, "two.txt"):   "two",
		filepath.Join(dirB, "three.txt"): "three",
		filepath.Join(dirB, "four.txt"):  "four",
	} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("%s: got %q (%v), want %q", path, got, err, want)
		}
	}
	for _, dir := range []string{dirA, dirB} {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 2 {
			t.Fatalf("unexpected entries in %s: %v %v", dir, entries, err)
		}
	}

	ephemeral := filepath.Join(root, "removed")
	if err := os.Mkdir(ephemeral, 0755); err != nil {
		t.Fatal(err)
	}
	if err := batch.AtomicWrite(filepath.Join(ephemeral, "file.txt"), []byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(ephemeral); err != nil {
		t.Fatal(err)
	}
	if err := batch.SyncDirectories(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SyncDirectories on removed dir = %v, want os.ErrNotExist", err)
	}
}

func TestBatchSyncDirectoriesResumesAfterFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	dirC := filepath.Join(root, "c")
	var batch Batch
	for _, dir := range []string{dirA, dirB, dirC} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := batch.AtomicWrite(filepath.Join(dir, "file"), []byte("data")); err != nil {
			t.Fatal(err)
		}
	}

	moved := filepath.Join(root, "moved")
	if err := os.Rename(dirB, moved); err != nil {
		t.Fatal(err)
	}
	if err := batch.SyncDirectories(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SyncDirectories with missing directory = %v, want os.ErrNotExist", err)
	}
	if want := []string{dirB, dirC}; !slices.Equal(batch.directories, want) {
		t.Fatalf("pending directories = %v, want %v", batch.directories, want)
	}
	if want := map[string]bool{dirB: true, dirC: true}; !maps.Equal(batch.seen, want) {
		t.Fatalf("seen directories = %v, want %v", batch.seen, want)
	}

	// Restore the failed directory and make the completed directory unavailable.
	if err := os.Rename(moved, dirB); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dirA, moved); err != nil {
		t.Fatal(err)
	}
	if err := batch.SyncDirectories(); err != nil {
		t.Fatalf("SyncDirectories retry: %v", err)
	}
	if len(batch.directories) != 0 || len(batch.seen) != 0 {
		t.Fatalf("batch not cleared after retry: directories=%v seen=%v", batch.directories, batch.seen)
	}
}

func TestSyncDirectoryRejectsRegularFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := SyncDirectory(path); err == nil {
		t.Fatal("SyncDirectory(regular file) with sync enabled succeeded, want error")
	}
}

func TestDisableSyncForTestingValidatesDescriptorsAndDirectories(t *testing.T) {
	// Intentionally non-parallel: mutates package-wide skipSync and restores it
	// before any parallel tests in this package run.
	if syncSkipped() {
		t.Fatal("real sync must remain enabled by default in internal/state tests")
	}
	defer skipSync.Store(false)
	DisableSyncForTesting()
	if !syncSkipped() {
		t.Fatal("DisableSyncForTesting did not enable skipSync")
	}

	if err := SyncFile(nil); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("SyncFile(nil) = %v, want os.ErrInvalid", err)
	}
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.txt")
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := SyncFile(file); err != nil {
		file.Close()
		t.Fatalf("SyncFile(open) = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := SyncFile(file); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("SyncFile(closed) = %v, want os.ErrClosed", err)
	}

	if err := SyncDirectory(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SyncDirectory(missing) = %v, want os.ErrNotExist", err)
	}
	if err := SyncDirectory(filePath); err == nil {
		t.Fatal("SyncDirectory(regular file) succeeded, want error")
	}
	if err := SyncDirectory(dir); err != nil {
		t.Fatalf("SyncDirectory(valid) = %v", err)
	}

	target := filepath.Join(dir, "written.txt")
	if err := AtomicWrite(target, []byte("hello")); err != nil {
		t.Fatalf("AtomicWrite with sync disabled: %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "hello" {
		t.Fatalf("AtomicWrite content: %q %v", got, err)
	}
}
