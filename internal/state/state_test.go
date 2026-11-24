package state

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
