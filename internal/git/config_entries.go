package git

/*
#include <git2.h>
*/
import "C"

import (
	"runtime"
	"strings"
)

type configurationEntry struct{ name, value string }

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

func (r *Repository) configEntries(prefix string) ([]configurationEntry, error) {
	var entries []configurationEntry
	err := r.call("read configuration entries", func(repo *C.git_repository) error {
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
			name := C.GoString(entry.name)
			if strings.HasPrefix(name, prefix) {
				entries = append(entries, configurationEntry{name, C.GoString(entry.value)})
			}
		}
	})
	return entries, err
}
