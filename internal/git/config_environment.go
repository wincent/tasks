package git

/*
#include <git2.h>
#include <git2/sys/config.h>
#include <git2/sys/errors.h>
#include <git2/sys/repository.h>
#include <stdlib.h>
#include <string.h>
#include <stdatomic.h>

// The 1.9 public backend interface supports an immutable per-handle overlay.
// Memory-backend constructors declared in 1.9 headers are not exported by the
// shared library. Use its supported file parser, never private symbols.
typedef struct {
 git_config_backend base;
 atomic_size_t references;
 size_t count;
 git_config_backend_entry **entries;
} task_config_overlay;

typedef struct {
 git_config_backend_entry base;
 task_config_overlay *owner;
} task_config_reference;

typedef struct {
 git_config_iterator base;
 task_config_overlay *snapshot;
 size_t position;
} task_config_iterator;

static void task_config_entry_free(git_config_backend_entry *entry) {
 if (!entry) return;
 free((void *)entry->entry.name);
 free((void *)entry->entry.value);
 free((void *)entry->entry.origin_path);
 free((void *)entry->entry.backend_type);
 free(entry);
}
static git_config_backend_entry *task_config_entry_new(const char *name, const char *value, git_config_level_t level) {
 git_config_backend_entry *entry = calloc(1, sizeof(*entry));
 if (!entry) return NULL;
 entry->free = task_config_entry_free;
 entry->entry.name = strdup(name);
 entry->entry.value = value ? strdup(value) : NULL;
 entry->entry.level = level;
 entry->entry.backend_type = strdup("command line");
 if (!entry->entry.name || !entry->entry.backend_type || (value && !entry->entry.value)) {
  task_config_entry_free(entry);
  return NULL;
 }
 return entry;
}
static git_config_backend_entry *task_config_entry_copy(const git_config_entry *source) {
 git_config_backend_entry *copy = task_config_entry_new(source->name, source->value, source->level);
 if (!copy) return NULL;
 copy->entry.include_depth = source->include_depth;
 free((void *)copy->entry.backend_type);
 copy->entry.backend_type = source->backend_type ? strdup(source->backend_type) : NULL;
 if (source->backend_type && !copy->entry.backend_type) {
  task_config_entry_free(copy);
  return NULL;
 }
 if (source->origin_path && !(copy->entry.origin_path = strdup(source->origin_path))) {
  task_config_entry_free(copy);
  return NULL;
 }
 return copy;
}
static void task_config_backend_free(git_config_backend *backend) {
 if (!backend) return;
 task_config_overlay *overlay = (task_config_overlay *)backend;
 if (atomic_fetch_sub_explicit(&overlay->references, 1, memory_order_acq_rel) != 1) return;
 for (size_t i = 0; i < overlay->count; i++) task_config_entry_free(overlay->entries[i]);
 free(overlay->entries);
 free(overlay);
}
static int task_config_open(git_config_backend *backend, git_config_level_t level, const git_repository *repo) {
 (void)repo;
 (void)backend;
 (void)level;
 return 0;
}
static void task_config_reference_free(git_config_backend_entry *entry) {
 task_config_reference *reference = (task_config_reference *)entry;
 task_config_backend_free(&reference->owner->base);
 free(reference);
}
static int task_config_get(git_config_backend *backend, const char *key, git_config_backend_entry **out) {
 task_config_overlay *overlay = (task_config_overlay *)backend;
 for (size_t i = overlay->count; i > 0; i--) {
  git_config_entry *entry = &overlay->entries[i-1]->entry;
  if (!strcmp(entry->name, key)) {
   task_config_reference *reference = calloc(1, sizeof(*reference));
   if (!reference) return GIT_ERROR;
   reference->base.entry = *entry;
   reference->base.free = task_config_reference_free;
   reference->owner = overlay;
   atomic_fetch_add_explicit(&overlay->references, 1, memory_order_relaxed);
   *out = &reference->base;
   return 0;
  }
 }
 return GIT_ENOTFOUND;
}
static int task_config_snapshot(git_config_backend **out, git_config_backend *backend);
static int task_config_iterator_next(git_config_backend_entry **out, git_config_iterator *iterator) {
 task_config_iterator *iter = (task_config_iterator *)iterator;
 if (iter->position == iter->snapshot->count) return GIT_ITEROVER;
 *out = iter->snapshot->entries[iter->position++];
 return 0;
}
static void task_config_iterator_free(git_config_iterator *iterator) {
 task_config_iterator *iter = (task_config_iterator *)iterator;
 task_config_backend_free(&iter->snapshot->base);
 free(iter);
}
static int task_config_iterate(git_config_iterator **out, git_config_backend *backend) {
 task_config_iterator *iter = calloc(1, sizeof(*iter));
 git_config_backend *snapshot = NULL;
 if (!iter) return GIT_ERROR;
 if (task_config_snapshot(&snapshot, backend) < 0) { free(iter); return GIT_ERROR; }
 iter->snapshot = (task_config_overlay *)snapshot;
 iter->base.backend = snapshot;
 iter->base.next = task_config_iterator_next;
 iter->base.free = task_config_iterator_free;
 *out = &iter->base;
 return 0;
}
static task_config_overlay *task_config_new(size_t count) {
 task_config_overlay *overlay = calloc(1, sizeof(*overlay));
 if (!overlay) return NULL;
 atomic_init(&overlay->references, 1);
 overlay->base.version = GIT_CONFIG_BACKEND_VERSION;
 overlay->base.readonly = 1;
 overlay->base.open = task_config_open;
 overlay->base.get = task_config_get;
 overlay->base.iterator = task_config_iterate;
 overlay->base.snapshot = task_config_snapshot;
 overlay->base.free = task_config_backend_free;
 overlay->entries = calloc(count, sizeof(*overlay->entries));
 if (count && !overlay->entries) { free(overlay); return NULL; }
 overlay->count = count;
 return overlay;
}
static int task_config_snapshot(git_config_backend **out, git_config_backend *backend) {
 task_config_overlay *source = (task_config_overlay *)backend;
 task_config_overlay *copy = task_config_new(source->count);
 if (!copy) return GIT_ERROR;
 for (size_t i = 0; i < source->count; i++) {
  git_config_entry *entry = &source->entries[i]->entry;
  copy->entries[i] = task_config_entry_copy(entry);
  if (!copy->entries[i]) { task_config_backend_free(&copy->base); return GIT_ERROR; }
 }
 *out = &copy->base;
 return 0;
}
static int task_config_from_pairs(git_config_backend **out, char **values, size_t count) {
 task_config_overlay *overlay = task_config_new(count);
 if (!overlay) return GIT_ERROR;
 for (size_t i = 0; i < count; i++) {
  overlay->entries[i] = task_config_entry_new(values[i*2], values[i*2+1], GIT_CONFIG_LEVEL_APP);
  if (!overlay->entries[i]) { task_config_backend_free(&overlay->base); return GIT_ERROR; }
 }
 *out = &overlay->base;
 return 0;
}
static int task_config_append(task_config_overlay *overlay, const git_config_entry *entry) {
 git_config_backend_entry *copy = task_config_entry_copy(entry);
 if (!copy) return GIT_ERROR;
 git_config_backend_entry **entries = realloc(overlay->entries, (overlay->count + 1) * sizeof(*entries));
 if (!entries) { task_config_entry_free(copy); return GIT_ERROR; }
 overlay->entries = entries;
 overlay->entries[overlay->count++] = copy;
 return 0;
}
static int task_config_append_file(task_config_overlay *overlay, const git_config_entry *entry,
 const char *name, const char *origin) {
 git_config_entry copy = *entry;
 copy.name = name;
 copy.origin_path = origin;
 copy.include_depth = 0;
 return task_config_append(overlay, &copy);
}
static int task_config_append_depth(task_config_overlay *overlay, const git_config_entry *entry, unsigned int depth) {
 git_config_entry copy = *entry;
 copy.include_depth = depth;
 return task_config_append(overlay, &copy);
}
static int task_config_parse_input(git_config **out, const char *path) {
 git_config *config = NULL;
 int result = git_config_new(&config);
 if (result < 0) return result;
 result = git_config_add_file_ondisk(config, path, GIT_CONFIG_LEVEL_APP, NULL, 0);
 if (result < 0) {
  git_config_free(config);
  return result;
 }
 *out = config;
 return 0;
}
static size_t task_config_count(git_config_backend *backend) {
 return ((task_config_overlay *)backend)->count;
}
static git_config_entry *task_config_at(git_config_backend *backend, size_t i) {
 return &((task_config_overlay *)backend)->entries[i]->entry;
}
// Capture the symbolic target once; checking HEAD then letting the parser
// reread the live HEAD would race with detach/checkout.
static int task_config_branch_target(char **out, const git_repository *repo) {
 git_reference *head = NULL;
 int result = git_reference_lookup(&head, (git_repository *)repo, "HEAD");
 if (result == GIT_ENOTFOUND) { git_error_clear(); return 0; }
 if (result < 0) return result;
 const char *target = git_reference_symbolic_target(head);
 if (target && !strncmp(target, "refs/heads/", 11)) {
  *out = strdup(target);
  result = *out ? 1 : GIT_ERROR;
 } else result = 0;
 git_reference_free(head);
 return result;
}
static int task_config_branch_repository(git_repository **out, const char *path, const char *target) {
 git_reference *head = NULL;
 git_repository_init_options options = GIT_REPOSITORY_INIT_OPTIONS_INIT;
 options.flags = GIT_REPOSITORY_INIT_BARE | GIT_REPOSITORY_INIT_MKPATH | GIT_REPOSITORY_INIT_NO_REINIT;
 options.initial_head = "task-config-snapshot";
 int result = git_repository_init_ext(out, path, &options);
 if (result < 0) return result;
 result = git_reference_symbolic_create(&head, *out, "HEAD", target, 1, NULL);
 git_reference_free(head);
 if (result < 0) { git_repository_free(*out); *out = NULL; }
 return result;
}
static int task_config_find(git_buf *out, int level) {
 switch (level) {
 case GIT_CONFIG_LEVEL_SYSTEM: return git_config_find_system(out);
 case GIT_CONFIG_LEVEL_XDG: return git_config_find_xdg(out);
 case GIT_CONFIG_LEVEL_GLOBAL: return git_config_find_global(out);
 default: return GIT_ENOTFOUND;
 }
}
static int task_config_append_record(task_config_overlay *out, const char *name,
 const char *value, const char *origin, const char *backend, int level, unsigned int depth) {
 git_config_entry entry = {0};
 entry.name = name; entry.value = value; entry.origin_path = origin;
 entry.backend_type = backend; entry.level = level; entry.include_depth = depth;
 return task_config_append(out, &entry);
}
static int task_repository_environment(git_repository *repo, git_config_backend *backend) {
 git_config *config = NULL;
 int result = git_repository_config(&config, repo);
 if (result < 0) return result;
 result = git_config_add_backend(config, backend, GIT_CONFIG_LEVEL_APP, repo, 1);
 git_config_free(config);
 return result;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// Injected configuration belongs to an individual native handle, not to global
// libgit2 settings or an on-disk config file. Private worktrees receive the same
// command configuration without redirecting their repository or index identity.
type environmentConfiguration struct {
	original        *C.git_config         // native writable local backend, never the effective snapshot
	backend         *C.git_config_backend // protected, unconditional closure
	commands        *C.git_config_backend // original command records
	includes        map[string]*C.task_config_overlay
	includeContents map[string][]byte
	includePaths    map[string]string
	parsedFiles     configurationParseCache // ordinary files only; command captures stay frozen
	work            *configurationWork
}

func configurationKey(key string) (string, bool) {
	first, last := strings.IndexByte(key, '.'), strings.LastIndexByte(key, '.')
	if first < 1 || last == len(key)-1 || strings.ContainsAny(key, "\n\x00") {
		return "", false
	}
	for partIndex, part := range []string{key[:first], key[last+1:]} {
		for i, c := range part {
			letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
			if !letter && (partIndex == 1 && i == 0 || !(c >= '0' && c <= '9' || c == '-')) {
				return "", false
			}
		}
	}
	return strings.ToLower(key[:first]) + key[first:last+1] + strings.ToLower(key[last+1:]), true
}

func prepareEnvironmentConfiguration() (*environmentConfiguration, error) {
	result := &environmentConfiguration{parsedFiles: configurationParseCache{shared: &sharedConfigurationParses}}
	count := 0
	if value := os.Getenv("GIT_CONFIG_COUNT"); value != "" {
		var err error
		count, err = strconv.Atoi(strings.TrimLeft(value, " \t\r\n\v\f"))
		if err != nil || count < 0 || count > 1<<31-1 {
			return nil, errors.New("GIT_CONFIG_COUNT must be a nonnegative decimal integer no larger than 2147483647")
		}
	}
	// Do not allocate from an untrusted count before checking its actual keys.
	values := []*C.char{}
	defer func() {
		for _, value := range values {
			C.free(unsafe.Pointer(value))
		}
	}()
	appendPair := func(key string, value *string) error {
		key, valid := configurationKey(key)
		if !valid || value != nil && strings.ContainsRune(*value, 0) {
			return errors.New("invalid injected Git configuration key or value")
		}
		if key == "safe.directory" {
			return commandOwnershipPolicyError()
		}
		if key == "include.path" {
			if _, err := commandIncludePath(value); err != nil {
				return err
			}
		}
		var nativeValue *C.char
		if value != nil {
			nativeValue = C.CString(*value)
		}
		values = append(values, C.CString(key), nativeValue)
		return nil
	}
	for i := 0; i < count; i++ {
		key, keyExists := os.LookupEnv(fmt.Sprintf("GIT_CONFIG_KEY_%d", i))
		value, valueExists := os.LookupEnv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", i))
		if !keyExists || key == "" || !valueExists {
			return nil, fmt.Errorf("injected Git configuration entry %d requires a nonempty GIT_CONFIG_KEY_%d and GIT_CONFIG_VALUE_%d", i, i, i)
		}
		if err := appendPair(key, &value); err != nil {
			return nil, fmt.Errorf("injected Git configuration entry %d: %w", i, err)
		}
	}
	// Git propagates explicit -c options after counted environment entries.
	parameters, err := configurationParameters(os.Getenv("GIT_CONFIG_PARAMETERS"))
	if err != nil {
		return nil, err
	}
	for i, pair := range parameters {
		if err := appendPair(pair.key, pair.value); err != nil {
			return nil, fmt.Errorf("GIT_CONFIG_PARAMETERS entry %d: %w", i, err)
		}
	}
	if len(values) == 0 {
		if _, affected, err := result.protectedRecords(); err != nil || affected {
			result.close()
			if err == nil {
				err = conditionalOwnershipPolicyError()
			}
			return nil, err
		}
		return result, nil
	}
	code := C.task_config_from_pairs(&result.backend, (**C.char)(unsafe.Pointer(&values[0])), C.size_t(len(values)/2))
	if code < 0 {
		result.close()
		// Native parser diagnostics can contain credential-bearing keys/values.
		return nil, fmt.Errorf("invalid injected Git configuration (libgit2 %d)", int(code))
	}
	result.commands, result.backend = result.backend, nil
	if _, affected, err := result.protectedRecords(); err != nil || affected {
		result.close()
		if err == nil {
			err = conditionalOwnershipPolicyError()
		}
		return nil, err
	}
	return result, nil
}

// protectedValues reads system/global and frozen command configuration only.
// Repository-local values and repository-dependent includes cannot relax policy.
func (configuration *environmentConfiguration) protectedValues(name string) ([]*string, error) {
	records, _, err := configuration.protectedRecords()
	if err != nil {
		return nil, err
	}
	var values []*string
	for _, record := range records {
		if record.name == name {
			values = append(values, record.value)
		}
	}
	return values, nil
}

func conditionalOwnershipPolicyError() error {
	return errors.New("safe.directory in a hasconfig-conditional global/system include is unsupported before native ownership validation; move ownership policy to unconditional global/system configuration files")
}

func commandOwnershipPolicyError() error {
	return errors.New("command-scope safe.directory is unsupported: libgit2 checks ownership before applying command configuration")
}

func (configuration *environmentConfiguration) close() {
	if configuration.original != nil {
		C.git_config_free(configuration.original)
		configuration.original = nil
	}
	for _, included := range configuration.includes {
		C.task_config_backend_free(&included.base)
	}
	configuration.includes = nil
	configuration.includeContents = nil
	configuration.includePaths = nil
	configuration.parsedFiles.clear()
	if configuration.commands != nil {
		C.task_config_backend_free(configuration.commands)
		configuration.commands = nil
	}
	if configuration.backend != nil {
		C.task_config_backend_free(configuration.backend)
		configuration.backend = nil
	}
}

func configurationExpandPath(path string) (string, error) {
	// libgit2 1.9 config.c copies non-tilde paths verbatim. Keep cstring's
	// NUL diagnostic, but avoid allocating a native input/output for that case.
	if !strings.HasPrefix(path, "~") {
		if strings.ContainsRune(path, 0) {
			return "", errors.New("Git argument contains NUL")
		}
		return path, nil
	}
	value, err := cstring(path)
	if err != nil {
		return "", err
	}
	defer free(value)
	var expanded C.git_buf
	defer C.git_buf_dispose(&expanded)
	if err := nativeError("expand configuration path", C.git_config_parse_path(&expanded, value)); err != nil {
		return "", err
	}
	return C.GoString(expanded.ptr), nil
}

func configurationAbsoluteIncludePath(path string) (string, error) {
	path, err := configurationExpandPath(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", errors.New("cannot resolve command Git include")
		}
		path = cwd + string(filepath.Separator) + path
	}
	return path, nil
}

func (configuration *environmentConfiguration) include(path string) (*C.task_config_overlay, error) {
	path, err := configurationAbsoluteIncludePath(path)
	if err != nil {
		return nil, err
	}
	if included, ok := configuration.includes[path]; ok {
		return included, nil
	}
	if configuration.includes == nil {
		configuration.includes = make(map[string]*C.task_config_overlay)
		configuration.includeContents = make(map[string][]byte)
		configuration.includePaths = make(map[string]string)
	}
	identity, ok := configuration.includePaths[path]
	if !ok {
		identity, err = configurationIncludeIdentity(path)
		if err != nil {
			return nil, errors.New("cannot resolve command Git include")
		}
		configuration.includePaths[path] = identity
	}
	data, captured := configuration.includeContents[identity]
	if !captured {
		data, err = os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, errors.New("cannot read command Git include")
		}
		configuration.includeContents[identity] = data // Missing and empty files are captures too.
		configuration.includePaths[identity] = identity
	}
	included, err := configurationParseInclude(path, data)
	if err != nil {
		return nil, err
	}
	configuration.includes[path] = included
	return included, nil
}

func configurationParseFileRecords(path string, data []byte) ([]hasconfigRecord, error) {
	included, err := configurationParseInclude(path, data)
	if err != nil {
		return nil, err
	}
	defer C.task_config_backend_free(&included.base)
	return configurationBackendRecords(&included.base), nil
}

func configurationParseInclude(path string, data []byte) (*C.task_config_overlay, error) {
	// Mask recursive includes in an immutable private parser input. The native
	// parser preserves syntax, nil values and order; restore lexical origins.
	input, prefix := configurationWithoutIncludes(string(data))
	dir, err := os.MkdirTemp("", "task-config-capture-")
	if err != nil {
		return nil, errors.New("cannot create Git include parser input")
	}
	defer os.RemoveAll(dir)
	parserPath := filepath.Join(dir, "config")
	if err := os.WriteFile(parserPath, []byte(input), 0600); err != nil {
		return nil, errors.New("cannot write Git include parser input")
	}
	nativePath, origin := C.CString(parserPath), C.CString(path)
	defer free(nativePath)
	defer free(origin)
	var parsed *C.git_config
	code := C.task_config_parse_input(&parsed, nativePath)
	if code < 0 {
		return nil, fmt.Errorf("cannot parse command Git include (libgit2 %d)", int(code))
	}
	defer C.git_config_free(parsed)
	var iterator *C.git_config_iterator
	if code := C.git_config_iterator_new(&iterator, parsed); code < 0 {
		return nil, fmt.Errorf("cannot iterate command Git include (libgit2 %d)", int(code))
	}
	defer C.git_config_iterator_free(iterator)
	included := C.task_config_new(0)
	if included == nil {
		return nil, errors.New("cannot allocate included Git configuration")
	}
	for {
		var entry *C.git_config_entry
		code := C.git_config_next(&entry, iterator)
		if code == C.GIT_ITEROVER {
			break
		}
		if code < 0 {
			C.task_config_backend_free(&included.base)
			return nil, fmt.Errorf("cannot parse command Git include (libgit2 %d)", int(code))
		}
		name := C.GoString(entry.name)
		if strings.HasPrefix(name, prefix+".") || strings.HasPrefix(name, prefix+"if.") {
			name = "include" + strings.TrimPrefix(name, prefix)
		}
		key := C.CString(name)
		code = C.task_config_append_file(included, entry, key, origin)
		free(key)
		if code < 0 {
			C.task_config_backend_free(&included.base)
			return nil, errors.New("cannot retain command Git include")
		}
	}
	return included, nil
}

// Future children have no config.worktree, even when their parent does.
func (r *Repository) prospectiveWorktreeConfiguration(repo *C.git_repository, name, path, branch string) (*C.git_config, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return nil, errors.New("worktree requires a single registration name")
	}
	common := filepath.Clean(C.GoString(C.git_repository_commondir(repo)))
	context := &configurationContext{gitdir: filepath.Join(common, "worktrees", name), branch: branch}
	configuration := r.configuration
	if configuration == nil {
		configuration = &environmentConfiguration{parsedFiles: configurationParseCache{shared: &sharedConfigurationParses}}
		defer configuration.close()
	}
	records, err := configuration.effectiveRecords(repo, context)
	if err != nil {
		return nil, err
	}
	return configuration.nativeRecords(records)
}

func configurationBackendRecords(backend *C.git_config_backend) []hasconfigRecord {
	var records []hasconfigRecord
	if backend == nil {
		return records
	}
	count := C.task_config_count(backend)
	if count == 0 {
		return records
	}
	records = make([]hasconfigRecord, 0, int(count))
	for i := C.size_t(0); i < count; i++ {
		entry := C.task_config_at(backend, i)
		record := hasconfigRecord{name: C.GoString(entry.name), origin: C.GoString(entry.origin_path), backend: C.GoString(entry.backend_type), level: int(entry.level), depth: uint(entry.include_depth)}
		if entry.value != nil {
			value := C.GoString(entry.value)
			record.value = &value
		}
		records = append(records, record)
	}
	return records
}

func (configuration *environmentConfiguration) loadRecords(path string) ([]hasconfigRecord, error) {
	included, err := configuration.include(path)
	if err != nil {
		return nil, err
	}
	return configurationBackendRecords(&included.base), nil
}

func (configuration *environmentConfiguration) sources(files *configurationFileCapture, repo *C.git_repository) ([]hasconfigSource, error) {
	var sources []hasconfigSource
	add := func(path string, level int) error {
		records, err := files.borrowRecords(path)
		if err != nil {
			return err
		}
		sources = append(sources, hasconfigSource{records: records, load: files.borrowRecords, level: level})
		return nil
	}
	for _, level := range []C.int{C.GIT_CONFIG_LEVEL_SYSTEM, C.GIT_CONFIG_LEVEL_XDG, C.GIT_CONFIG_LEVEL_GLOBAL} {
		var path C.git_buf
		code := C.task_config_find(&path, level)
		if code == C.GIT_ENOTFOUND {
			C.git_buf_dispose(&path)
			continue
		}
		if code < 0 {
			C.git_buf_dispose(&path)
			return nil, errors.New("cannot locate default Git configuration")
		}
		name := C.GoString(path.ptr)
		C.git_buf_dispose(&path)
		if err := add(name, int(level)); err != nil {
			return nil, err
		}
	}
	if repo != nil {
		common := C.GoString(C.git_repository_commondir(repo))
		if err := add(filepath.Join(common, "config"), int(C.GIT_CONFIG_LEVEL_LOCAL)); err != nil {
			return nil, err
		}
	}
	sources = append(sources, hasconfigSource{records: configurationBackendRecords(configuration.commands), load: configuration.loadRecords, command: true})
	return sources, nil
}

// Pure analysis: the caller must separately decide how to handle an ownership
// policy native admission cannot evaluate. No ownership setting is changed here.
func (configuration *environmentConfiguration) protectedRecords() ([]hasconfigRecord, bool, error) {
	files := newConfigurationFileCapture(&configuration.parsedFiles)
	sources, err := configuration.sources(files, nil)
	if err != nil {
		return nil, false, err
	}
	evaluation := &hasconfigEvaluation{rejectCommandOwnership: true}
	records, err := evaluation.evaluate(sources)
	return records, evaluation.protectedOwnership, err
}

// Ownership admission needs repository-dependent conditions, unlike protected
// bare policy. Scan only global/system sources here: ordinary command branch
// includes retain their existing non-protected scope. Never open a repository
// handle to obtain this context, or ownership admission would already be over.
func (configuration *environmentConfiguration) validateConditionalOwnership(context configurationContext) error {
	files := newConfigurationFileCapture(&configuration.parsedFiles)
	sources, err := configuration.sources(files, nil)
	if err != nil {
		return err
	}
	return (&hasconfigEvaluation{matches: context.matchesOwnership}).validateOwnership(sources)
}

func (configuration *environmentConfiguration) effectiveRecords(repo *C.git_repository, context *configurationContext) ([]hasconfigRecord, error) {
	files := newConfigurationFileCapture(&configuration.parsedFiles)
	files.work = configuration.work
	sources, err := configuration.sources(files, repo)
	if err != nil {
		return nil, err
	}
	if context == nil {
		var target *C.char
		code := C.task_config_branch_target(&target, repo)
		if code < 0 {
			return nil, errors.New("cannot read Git include branch context")
		}
		defer C.free(unsafe.Pointer(target))
		context = &configurationContext{gitdir: filepath.Clean(C.GoString(C.git_repository_path(repo))), branch: C.GoString(target)}
		// Worktree enablement is determined without command-scope overrides.
		ordinary := &hasconfigEvaluation{matches: context.matches, work: configuration.work}
		// Reuse only inside this capture. With no command records and no
		// worktree layer, the ordinary and final graphs have identical inputs.
		// Otherwise later URLs/overrides can change hasconfig selection.
		reusable := len(sources[len(sources)-1].records) == 0
		var ordinaryRecords []hasconfigRecord
		enabled := false
		var conversionError error
		err := ordinary.evaluateEach(sources[:len(sources)-1], func(record hasconfigRecord) error {
			if reusable {
				ordinaryRecords = append(ordinaryRecords, record)
			}
			if record.name != "extensions.worktreeconfig" || conversionError != nil {
				return nil
			}
			var value *C.char
			if record.value != nil {
				value = C.CString(*record.value)
			}
			var parsed C.int
			code := C.git_config_parse_bool(&parsed, value)
			C.free(unsafe.Pointer(value))
			if code < 0 {
				conversionError = errors.New("invalid extensions.worktreeConfig")
				return nil
			}
			enabled = parsed != 0
			return nil
		})
		if err != nil {
			return nil, err
		}
		// Graph errors retain precedence over value conversion errors, as when
		// the full flattened graph was built before inspecting this setting.
		if conversionError != nil {
			return nil, conversionError
		}
		if !enabled && reusable {
			// The traversal borrowed immutable values. Give each output record
			// its own value, just as evaluate does, without copying the slice.
			for i := range ordinaryRecords {
				if ordinaryRecords[i].value != nil {
					value := *ordinaryRecords[i].value
					ordinaryRecords[i].value = &value
				}
			}
			if configuration.work != nil {
				configuration.work.copies += uint64(len(ordinaryRecords))
			}
			return ordinaryRecords, nil
		}
		if enabled {
			records, err := files.borrowRecords(filepath.Join(C.GoString(C.git_repository_path(repo)), "config.worktree"))
			if err != nil {
				return nil, err
			}
			command := sources[len(sources)-1]
			sources = append(sources[:len(sources)-1], hasconfigSource{records: records, load: files.borrowRecords, level: int(C.GIT_CONFIG_LEVEL_WORKTREE)}, command)
		}
	}
	return (&hasconfigEvaluation{matches: context.matches, work: configuration.work}).evaluate(sources)
}

func configurationNativeRecords(records []hasconfigRecord) (*C.git_config, error) {
	overlay := C.task_config_new(0)
	if overlay == nil {
		return nil, errors.New("cannot allocate effective Git configuration")
	}
	owned := true
	defer func() {
		if owned {
			C.task_config_backend_free(&overlay.base)
		}
	}()
	for _, record := range records {
		name, origin, backend := C.CString(record.name), C.CString(record.origin), C.CString(record.backend)
		var value *C.char
		if record.value != nil {
			value = C.CString(*record.value)
		}
		code := C.task_config_append_record(overlay, name, value, origin, backend, C.int(record.level), C.uint(record.depth))
		C.free(unsafe.Pointer(name))
		C.free(unsafe.Pointer(origin))
		C.free(unsafe.Pointer(backend))
		C.free(unsafe.Pointer(value))
		if code < 0 {
			return nil, errors.New("cannot retain effective Git configuration")
		}
	}
	var result *C.git_config
	if err := nativeError("create effective configuration", C.git_config_new(&result)); err != nil {
		return nil, err
	}
	if err := nativeError("attach effective configuration", C.git_config_add_backend(result, &overlay.base, C.GIT_CONFIG_LEVEL_APP, nil, 1)); err != nil {
		C.git_config_free(result)
		return nil, err
	}
	owned = false
	return result, nil
}

func (configuration *environmentConfiguration) nativeRecords(records []hasconfigRecord) (*C.git_config, error) {
	if work := configuration.work; work != nil {
		work.assemblies++
		work.nativeRecords += uint64(len(records))
		start := time.Now()
		defer func() { work.assemblyTime += time.Since(start) }()
	}
	return configurationNativeRecords(records)
}

func (configuration *environmentConfiguration) apply(repo *C.git_repository) error {
	records, err := configuration.effectiveRecords(repo, nil)
	if err != nil {
		return err
	}
	effective, err := configuration.nativeRecords(records)
	if err != nil {
		return err
	}
	defer C.git_config_free(effective)
	if configuration.original == nil {
		if err := nativeError("retain writable configuration", C.git_repository_config(&configuration.original, repo)); err != nil {
			return err
		}
	}
	return nativeError("apply effective configuration", C.git_repository_set_config(repo, effective))
}

func (configuration *environmentConfiguration) call(repo *C.git_repository, operation string, f func(*C.git_repository) error) (err error) {
	if configuration.work != nil && operation == "repository paths" {
		configuration.work.paths++
	}
	if err := configuration.apply(repo); err != nil {
		return err
	}
	if operation != "write local configuration" {
		return f(repo)
	}
	// Local setters retain their native file backend and lock protocol; effective
	// reads are rebuilt independently at the next operation boundary.
	var effective *C.git_config
	if err := nativeError("retain effective configuration", C.git_repository_config(&effective, repo)); err != nil {
		return err
	}
	defer C.git_config_free(effective)
	if err := nativeError("select writable configuration", C.git_repository_set_config(repo, configuration.original)); err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, nativeError("restore effective configuration", C.git_repository_set_config(repo, effective)))
	}()
	return f(repo)
}
