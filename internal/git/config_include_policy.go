package git

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The cache retains only Go-owned, unevaluated parser records. Every operation
// still captures current file bytes and identity before looking anything up.
// Bound both entry count and retained bytes; oversized files are parsed normally
// but not retained. Eviction affects cost only, never configuration semantics.
const configurationParseCacheEntries = 128
const configurationParseCacheBytes = 8 << 20

type configurationParsedFile struct {
	identity string
	contents configurationFileContents
	records  []hasconfigRecord
	size     int
}

// Only prepared repository handles opt into this process-lifetime pool. Its
// zero-value cache has no parent, so standalone local-cache semantics stay local.
// No native resource, evaluated graph, or command capture enters either layer.
var sharedConfigurationParses configurationParseCache

type configurationParseCache struct {
	shared  *configurationParseCache // immutable after construction; parent has no parent
	mu      sync.Mutex
	entries map[string]configurationParsedFile
	bytes   int
	parses  uint64 // parser attempts on misses, including malformed inputs
}

func (cache *configurationParseCache) clear() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.entries, cache.bytes = nil, 0
}

func (cache *configurationParseCache) forget(path string) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.bytes -= cache.entries[path].size
	delete(cache.entries, path)
}

func cloneConfigurationRecords(records []hasconfigRecord) []hasconfigRecord {
	result := append([]hasconfigRecord(nil), records...)
	for i := range result {
		if result[i].value != nil {
			value := *result[i].value
			result[i].value = &value
		}
	}
	return result
}

func (cache *configurationParseCache) records(path, identity string, contents configurationFileContents) ([]hasconfigRecord, error) {
	records, err := cache.borrowRecords(path, identity, contents)
	return cloneConfigurationRecords(records), err
}

// Borrowed records and their value pointers are immutable, including after
// eviction. Only nonmutating internal traversal may retain these references.
func (cache *configurationParseCache) borrowRecords(path, identity string, contents configurationFileContents) ([]hasconfigRecord, error) {
	file, _, err := cache.borrowFile(path, identity, contents)
	return file.records, err
}

// Lock order is handle then pool; the pool never calls back into a handle.
// Holding the pool lock through parsing coalesces concurrent identical misses.
// Returned references remain live Go objects after eviction or handle close.
func (cache *configurationParseCache) borrowFile(path, identity string, contents configurationFileContents) (configurationParsedFile, bool, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	previous, ok := cache.entries[path]
	if ok && previous.identity == identity && os.SameFile(previous.contents.info, contents.info) && bytes.Equal(previous.contents.data, contents.data) {
		return previous, false, nil
	}
	// A failed replacement never becomes a hit on a previous successful parse.
	cache.bytes -= previous.size
	delete(cache.entries, path)
	var file configurationParsedFile
	var err error
	parsed := true
	if cache.shared != nil {
		file, parsed, err = cache.shared.borrowFile(path, identity, contents)
	} else {
		var records []hasconfigRecord
		records, err = configurationParseFileRecords(path, contents.data)
		if err == nil {
			// Own the exact-byte witness, even if the caller later reuses its
			// capture buffer. Account slice capacity and record/string storage.
			data := bytes.Clone(contents.data)
			size := 512 + len(path) + len(identity) + cap(data) + cap(records)*128
			for _, record := range records {
				size += len(record.name) + len(record.origin) + len(record.backend)
				if record.value != nil {
					size += len(*record.value)
				}
			}
			file = configurationParsedFile{identity: identity, contents: configurationFileContents{data: data, info: contents.info}, records: records, size: size}
		}
	}
	if parsed {
		cache.parses++ // actual parser attempts attributable to this handle
	}
	if err != nil {
		return configurationParsedFile{}, parsed, err
	}
	size := file.size
	if size <= configurationParseCacheBytes {
		if len(cache.entries) >= configurationParseCacheEntries || cache.bytes+size > configurationParseCacheBytes {
			cache.entries, cache.bytes = nil, 0
		}
		if cache.entries == nil {
			cache.entries = make(map[string]configurationParsedFile)
		}
		cache.entries[path] = file
		cache.bytes += size
	}
	return file, parsed, nil
}

type configurationFileContents struct {
	data []byte
	info os.FileInfo
}

// A fresh capture is created at each operation boundary. Aliases share captured
// bytes, but retain distinct lexical origins for relative includes and gitdir:./.
// Neither condition results nor missing files survive the operation boundary.
type configurationFileCapture struct {
	work     *configurationWork
	cache    *configurationParseCache
	contents map[string]configurationFileContents
	records  map[string][]hasconfigRecord
}

func newConfigurationFileCapture(cache *configurationParseCache) *configurationFileCapture {
	return &configurationFileCapture{cache: cache, contents: make(map[string]configurationFileContents), records: make(map[string][]hasconfigRecord)}
}

func (capture *configurationFileCapture) loadRecords(path string) ([]hasconfigRecord, error) {
	records, err := capture.borrowRecords(path)
	return cloneConfigurationRecords(records), err
}

// The capture and parse cache share immutable records. Public-to-this-package
// callers that need owned values use loadRecords; evaluation borrows read-only.
func (capture *configurationFileCapture) borrowRecords(path string) ([]hasconfigRecord, error) {
	path, err := configurationAbsoluteIncludePath(path)
	if err != nil {
		return nil, err
	}
	if records, ok := capture.records[path]; ok {
		return records, nil
	}
	if work := capture.work; work != nil {
		work.captures++
		start := time.Now()
		defer func() { work.captureTime += time.Since(start) }()
	}
	identity, err := configurationIncludeIdentity(path)
	if err != nil {
		capture.cache.forget(path)
		return nil, errors.New("cannot resolve ordinary Git configuration")
	}
	contents, ok := capture.contents[identity]
	if !ok {
		if capture.work != nil {
			capture.work.reads++
		}
		file, err := os.Open(path)
		if os.IsNotExist(err) {
			capture.contents[identity] = contents
		} else if err != nil {
			capture.cache.forget(path)
			return nil, errors.New("cannot read ordinary Git configuration")
		} else {
			contents.info, err = file.Stat()
			if err == nil {
				contents.data, err = io.ReadAll(file)
			}
			err = errors.Join(err, file.Close())
			if err != nil {
				capture.cache.forget(path)
				return nil, errors.New("cannot read ordinary Git configuration")
			}
			capture.contents[identity] = contents
		}
	}
	if contents.info == nil {
		capture.cache.forget(path)
		capture.records[path] = nil
		return nil, nil
	}
	records, err := capture.cache.borrowRecords(path, identity, contents)
	if err != nil {
		return nil, err
	}
	capture.records[path] = records
	return records, nil
}

// Resolve existing aliases, including an existing parent of a missing target.
// The caller remembers this mapping before any later deletion or replacement.
func configurationIncludeIdentity(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Dir(path) + string(filepath.Separator) + target
		}
		return configurationIncludeIdentity(target)
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path, nil
	}
	resolved, err = configurationIncludeIdentity(parent)
	return filepath.Join(resolved, filepath.Base(path)), err
}

// Rename all include sections without interpreting their values.
// Only a header at the beginning of a logical line can start a section; quoted
// values, comments and continued lines remain byte-for-byte parser inputs.
// Pick a namespace absent from the input so real user keys cannot collide.
func configurationWithoutIncludes(input string) (string, string) {
	prefix := "tasktool-frozen-include"
	for strings.Contains(strings.ToLower(input), prefix) {
		prefix += "x"
	}
	var out strings.Builder
	quoted, comment, escaped, lineStart := false, false, false, true
	for i := 0; i < len(input); i++ {
		if i == 0 && strings.HasPrefix(input, "\xef\xbb\xbf") {
			out.WriteString(input[:3])
			i = 2
			continue
		}
		c := input[i]
		if lineStart && !quoted && !comment && !escaped {
			if c == '[' && len(input)-i >= 8 && strings.EqualFold(input[i+1:i+8], "include") &&
				(i+8 == len(input) || strings.ContainsRune("]. \t\r\n\v\f", rune(input[i+8])) ||
					len(input)-i >= 10 && strings.EqualFold(input[i+8:i+10], "if") &&
						(i+10 == len(input) || strings.ContainsRune("]. \t\r\n\v\f", rune(input[i+10])))) {
				out.WriteByte('[')
				out.WriteString(prefix)
				i += 7
				lineStart = false
				continue
			}
			if !strings.ContainsRune(" \t\r\v\f", rune(c)) {
				lineStart = false
			}
		}
		out.WriteByte(c)
		if c == '\n' {
			lineStart = !escaped
			escaped, comment = false, false
			continue
		}
		if comment {
			continue
		}
		if escaped && c == '\r' && i+1 < len(input) && input[i+1] == '\n' {
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		switch c {
		case '\\':
			escaped = true
		case '"':
			quoted = !quoted
		case '#', ';':
			if !quoted {
				comment = true
			}
		}
	}
	return out.String(), prefix
}

// Subsections escape only quotes and backslashes, unlike variable values.
// Refuse unrepresentable line breaks rather than silently changing a pattern.
func quoteConfigurationSection(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("Git include condition cannot be represented in a configuration subsection")
	}
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(value) + "\"", nil
}

// Quote a Git variable value, not a shell word. Real overlay values remain
// in the native copy; this is used only for the synthetic marker path.
func quoteConfigurationFile(value string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\t", "\\t", "\b", "\\b").Replace(value) + "\""
}

// Command configuration has no originating file against which to resolve a
// relative include. Included files themselves retain their native file context.
func commandIncludePath(value *string) (string, error) {
	if value == nil {
		return "", errors.New("command Git include.path requires a value")
	}
	if !filepath.IsAbs(*value) && !strings.HasPrefix(*value, "~/") {
		return "", errors.New("relative command Git include.path has no originating configuration file")
	}
	return *value, nil
}
