//go:build darwin

package state

import (
	"os"
	"syscall"
)

// SameVersion catches replacement and in-place edits, including edits that
// restore the old content and mtime while an editor or launcher is unlocked.
func SameVersion(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	before, ok := a.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	after, ok := b.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime() == b.ModTime() && before.Ctimespec == after.Ctimespec
}
