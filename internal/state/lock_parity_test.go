package state

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLentLockRetainsOwnershipUntilLastDescriptorCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permanent")
	lock, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := lock.Lend()
	if err != nil {
		t.Fatal(err)
	}
	defer duplicate.Close()
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if other, err := TryLock(path); !errors.Is(err, ErrBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("lost inherited lock: %v", err)
	}
	if err := duplicate.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
}

func TestWaitingLockRejectsReplacedInode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permanent")
	file, err := openLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new owner"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := try(file, syscall.LOCK_EX); err == nil {
		t.Fatal("acquired obsolete inode")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new owner" {
		t.Fatal("changed replacement lock", err)
	}
}
