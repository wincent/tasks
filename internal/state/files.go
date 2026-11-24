// Package state implements durable local task state and permanent-inode locks.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

var skipSync atomic.Bool

// DisableSyncForTesting skips file and directory sync calls inside test
// binaries while preserving file/directory open and validation checks. It
// panics if called outside a `go test` binary.
func DisableSyncForTesting() {
	if !testing.Testing() {
		panic("state.DisableSyncForTesting called outside a test binary")
	}
	skipSync.Store(true)
}

func syncSkipped() bool {
	return testing.Testing() && skipSync.Load()
}

// AtomicWrite replaces data only after flushing its complete contents, then
// flushes the containing directory. It must never be used for lock files.
// An error after rename means publication may already have occurred.
func AtomicWrite(path string, data []byte) error {
	return atomicWrite(path, data, true, os.Rename)
}

// AtomicCreate publishes a complete file without replacing an existing path.
// installed is true if publication occurred, even if directory sync fails.
func AtomicCreate(path string, data []byte) (installed bool, err error) {
	err = atomicWrite(path, data, true, func(source, target string) error {
		err := os.Link(source, target)
		installed = err == nil
		return err
	})
	return installed, err
}

// Batch installs multiple atomic files and flushes each unique containing
// directory once in SyncDirectories. Each file's contents are flushed before
// installation; callers must call SyncDirectories before publishing references
// that depend on the installed files.
type Batch struct {
	directories []string
	seen        map[string]bool
}

// AtomicWrite writes and installs path without syncing its containing directory
// yet; call SyncDirectories after installing all files in the batch.
func (batch *Batch) AtomicWrite(path string, data []byte) error {
	if err := atomicWrite(path, data, false, os.Rename); err != nil {
		return err
	}
	batch.record(filepath.Dir(path))
	return nil
}

// AtomicCreate installs path without replacing an existing file and defers
// directory sync until SyncDirectories. installed is true if publication
// occurred.
func (batch *Batch) AtomicCreate(path string, data []byte) (installed bool, err error) {
	err = atomicWrite(path, data, false, func(source, target string) error {
		err := os.Link(source, target)
		installed = err == nil
		return err
	})
	if installed {
		batch.record(filepath.Dir(path))
	}
	return installed, err
}

// SyncDirectories flushes each unique containing directory recorded by the
// batch once. On error, the failed directory and any unattempted directories
// remain pending for a retry.
func (batch *Batch) SyncDirectories() error {
	for len(batch.directories) > 0 {
		directory := batch.directories[0]
		if err := SyncDirectory(directory); err != nil {
			return fmt.Errorf("state installed but directory sync failed for %s: %w", directory, err)
		}
		delete(batch.seen, directory)
		batch.directories[0] = ""
		batch.directories = batch.directories[1:]
	}
	batch.directories = nil
	batch.seen = nil
	return nil
}

func (batch *Batch) record(directory string) {
	directory = filepath.Clean(directory)
	if batch.seen == nil {
		batch.seen = make(map[string]bool)
	}
	if !batch.seen[directory] {
		batch.seen[directory] = true
		batch.directories = append(batch.directories, directory)
	}
}

func atomicWrite(path string, data []byte, syncDir bool, install func(string, string) error) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".task-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer func() {
		file.Close()
		if removeErr := os.Remove(name); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove temporary state: %w", removeErr))
		}
	}()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = SyncFile(file); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = install(name, path); err != nil {
		return err
	}
	if syncDir {
		if err = SyncDirectory(filepath.Dir(path)); err != nil {
			return fmt.Errorf("state installed but directory sync failed for %s: %w", path, err)
		}
	}
	return nil
}

// SyncFile flushes an open file's contents to stable storage.
func SyncFile(file *os.File) error {
	if syncSkipped() {
		_, err := file.Stat()
		return err
	}
	return file.Sync()
}

func SyncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	if syncSkipped() {
		info, err := directory.Stat()
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("not a directory: %s", path)
		}
		return nil
	}
	return directory.Sync()
}
