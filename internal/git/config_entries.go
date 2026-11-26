package git

/*
#include <git2.h>
*/
import "C"

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
)

// ConfigurationEntry preserves traversal order and valueless assignments.
type ConfigurationEntry struct {
	Name, Value string
	HasValue    bool
}

// ConfigurationEntries reads a fresh effective native snapshot. Command include
// contents retain the repository handle's existing frozen-command semantics.
func (r *Repository) ConfigurationEntries() ([]ConfigurationEntry, error) {
	var entries []ConfigurationEntry
	err := r.call("read ordered configuration", func(repo *C.git_repository) error {
		var config *C.git_config
		if err := nativeError("open configuration", C.git_repository_config_snapshot(&config, repo)); err != nil {
			return err
		}
		defer C.git_config_free(config)
		var iterator *C.git_config_iterator
		if err := nativeError("iterate configuration", C.git_config_iterator_new(&iterator, config)); err != nil {
			return err
		}
		defer C.git_config_iterator_free(iterator)
		for {
			var entry *C.git_config_entry
			code := C.git_config_next(&entry, iterator)
			if code == C.GIT_ITEROVER {
				return nil
			}
			if err := nativeError("read configuration entry", code); err != nil {
				return err
			}
			entries = append(entries, ConfigurationEntry{C.GoString(entry.name), C.GoString(entry.value), entry.value != nil})
		}
	})
	return entries, err
}

// BranchConfigurationEntries resolves configuration for an unchecked-out
// branch. gitdir conditions use the common Git directory, and no worktree's
// private configuration applies.
func (r *Repository) BranchConfigurationEntries(branch string) ([]ConfigurationEntry, error) {
	if !strings.HasPrefix(branch, "refs/heads/") || branch == "refs/heads/" {
		return nil, errors.New("branch configuration requires a fully qualified branch reference")
	}
	var entries []ConfigurationEntry
	err := r.callNative("read branch configuration", false, func(repo *C.git_repository) error {
		if r.configuration == nil {
			return errors.New("branch configuration requires an open repository, not a planning view")
		}
		context := configurationContext{
			gitdir: filepath.Clean(C.GoString(C.git_repository_commondir(repo))),
			branch: branch,
		}
		records, err := r.configuration.effectiveRecords(repo, &context)
		if err != nil {
			return err
		}
		entries = make([]ConfigurationEntry, 0, len(records))
		for _, record := range records {
			entry := ConfigurationEntry{Name: record.name}
			if record.value != nil {
				entry.Value, entry.HasValue = *record.value, true
			}
			entries = append(entries, entry)
		}
		return nil
	})
	return entries, err
}

// ConfigurationBool parses one copied entry without reopening configuration.
func ConfigurationBool(entry ConfigurationEntry) (bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var value *C.char
	if entry.HasValue {
		var err error
		value, err = cstring(entry.Value)
		if err != nil {
			return false, err
		}
		defer free(value)
	}
	var result C.int
	if err := nativeError("parse configuration boolean", C.git_config_parse_bool(&result, value)); err != nil {
		return false, err
	}
	return result != 0, nil
}

// ConfigurationPath applies Git configuration pathname interpolation.
func ConfigurationPath(value string) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return configurationExpandPath(value)
}
