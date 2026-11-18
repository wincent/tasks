package git

/*
#include <git2.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Inspect native config before opening the repository: the stable libgit2 ABI
// cannot represent SHA-256 OIDs and must never attempt format conversion.
func validateRepositoryFormat(path *C.char, flags C.uint, ceiling *C.char) error {
	var discovered C.git_buf
	across := C.int(0)
	if flags&C.GIT_REPOSITORY_OPEN_CROSS_FS != 0 {
		across = 1
	}
	if err := nativeError("discover repository format", C.git_repository_discover(&discovered, path, across, ceiling)); err != nil {
		return err
	}
	defer C.git_buf_dispose(&discovered)
	dir := C.GoString(discovered.ptr)
	common, err := os.ReadFile(filepath.Join(dir, "commondir"))
	if err == nil {
		name := strings.TrimSpace(string(common))
		if filepath.IsAbs(name) {
			dir = name
		} else {
			dir = filepath.Join(dir, name)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	name, err := cstring(filepath.Join(dir, "config"))
	if err != nil {
		return err
	}
	defer free(name)
	var config *C.git_config
	if err := nativeError("read repository format", C.git_config_open_ondisk(&config, name)); err != nil {
		return err
	}
	defer C.git_config_free(config)
	for _, setting := range []struct{ key, supported, description string }{
		{"extensions.objectformat", "sha1", "SHA-256 object format"},
		{"extensions.refstorage", "files", "reftable reference storage"},
	} {
		key, _ := cstring(setting.key)
		var value C.git_buf
		code := C.git_config_get_string_buf(&value, config, key)
		free(key)
		if code == C.GIT_ENOTFOUND {
			continue
		}
		if err := nativeError("read "+setting.key, code); err != nil {
			return err
		}
		format := C.GoString(value.ptr)
		C.git_buf_dispose(&value)
		if !strings.EqualFold(format, setting.supported) {
			return fmt.Errorf("unsupported repository %s (%s=%q); tasktool requires SHA-1 with files-based refs and leaves this repository unchanged; use a compatible checkout or your normal Git workflow", setting.description, setting.key, format)
		}
	}
	return nil
}
