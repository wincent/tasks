//go:build darwin || linux

package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

var ErrBusy = errors.New("task operation is already running")

// Lock owns a permanent lock inode. Closing releases only this descriptor; it
// never removes the path or another session's lock. File descriptors are not
// inherited by subprocesses unless explicitly lent by the workflow adapter.
type Lock struct {
	mu   sync.Mutex
	file *os.File
}

func openLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("task lock is not a regular file: %s", path)
	}
	return file, nil
}

func try(file *os.File, mode int) error {
	for {
		err := syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrBusy
		}
		if err != nil {
			return err
		}
		actual, err := os.Lstat(file.Name())
		if err != nil {
			return err
		}
		opened, err := file.Stat()
		if err != nil {
			return err
		}
		if !actual.Mode().IsRegular() || !os.SameFile(actual, opened) {
			return fmt.Errorf("task lock inode changed while waiting: %s", file.Name())
		}
		return nil
	}
}

func TryLock(path string) (*Lock, error) {
	return tryLock(path, syscall.LOCK_EX)
}

// TrySharedLock allows concurrent users while excluding an exclusive owner.
func TrySharedLock(path string) (*Lock, error) {
	return tryLock(path, syscall.LOCK_SH)
}

func tryLock(path string, mode int) (*Lock, error) {
	file, err := openLock(path)
	if err != nil {
		return nil, err
	}
	if err := try(file, mode); err != nil {
		file.Close()
		return nil, err
	}
	return &Lock{file: file}, nil
}

// Acquire waits interruptibly. Polling is bounded by context cancellation and
// avoids a goroutine stuck in a blocking flock after its caller has stopped.
func Acquire(ctx context.Context, path string) (*Lock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := openLock(path)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		err := try(file, syscall.LOCK_EX)
		if err == nil {
			return &Lock{file: file}, nil
		}
		if !errors.Is(err, ErrBusy) {
			file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Lend duplicates the same open-file description, preserving continuous flock
// ownership across a launcher/child handoff. The caller owns the duplicate.
func (lock *Lock) Lend() (*os.File, error) {
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.file == nil {
		return nil, errors.New("task operation lock is closed")
	}
	descriptor, err := syscall.Dup(int(lock.file.Fd()))
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(descriptor)
	return os.NewFile(uintptr(descriptor), lock.file.Name()), nil
}

func (lock *Lock) Close() error {
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	return file.Close()
}
