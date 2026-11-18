// Package state implements durable local task state and permanent-inode locks.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWrite replaces data only after flushing its complete contents, then
// flushes the containing directory. It must never be used for lock files.
// An error after rename means publication may already have occurred.
func AtomicWrite(path string, data []byte) error {
	return atomicWrite(path, data, os.Rename)
}

// AtomicCreate publishes a complete file without replacing an existing path.
// installed is true if publication occurred, even if directory sync fails.
func AtomicCreate(path string, data []byte) (installed bool, err error) {
	err = atomicWrite(path, data, func(source, target string) error {
		err := os.Link(source, target)
		installed = err == nil
		return err
	})
	return installed, err
}

func atomicWrite(path string, data []byte, install func(string, string) error) (err error) {
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
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = install(name, path); err != nil {
		return err
	}
	if err = SyncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("state installed but directory sync failed for %s: %w", path, err)
	}
	return nil
}

func SyncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
