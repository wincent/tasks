// Package git provides in-process Git operations using dynamically linked libgit2.
package git

/*
#cgo pkg-config: libgit2
#cgo linux CFLAGS: -D_GNU_SOURCE
#cgo linux LDFLAGS: -ldl
#include <git2.h>
#include <dlfcn.h>
#include <stdlib.h>

#if LIBGIT2_VER_MAJOR != 1 || LIBGIT2_VER_MINOR != 9
#error "tasktool requires libgit2 1.9.x headers; install libgit2 and configure PKG_CONFIG_PATH, then rebuild"
#endif
#ifdef GIT_EXPERIMENTAL_SHA256
#error "tasktool does not support the experimental libgit2 SHA-256 ABI"
#endif

static int task_dynamic_linked(void) {
    Dl_info library, executable;
    return dladdr((void *)git_libgit2_init, &library) &&
           dladdr((void *)task_dynamic_linked, &executable) &&
           library.dli_fbase != executable.dli_fbase;
}

static int task_repository_init(git_repository **out, const char *path,
                               const char *workdir, int bare) {
    git_repository_init_options opts = GIT_REPOSITORY_INIT_OPTIONS_INIT;
    opts.flags = GIT_REPOSITORY_INIT_MKPATH | GIT_REPOSITORY_INIT_NO_REINIT;
    if (bare) opts.flags |= GIT_REPOSITORY_INIT_BARE;
    if (workdir) opts.flags |= GIT_REPOSITORY_INIT_NO_DOTGIT_DIR;
    opts.workdir_path = workdir;
    opts.initial_head = "main";
    return git_repository_init_ext(out, path, &opts);
}

static int task_worktree_add(git_worktree **out, git_repository *repo,
                            const char *name, const char *path, git_reference *ref) {
    git_worktree_add_options opts = GIT_WORKTREE_ADD_OPTIONS_INIT;
    opts.ref = ref;
    opts.checkout_options.checkout_strategy = GIT_CHECKOUT_NONE | GIT_CHECKOUT_DONT_UPDATE_INDEX;
    return git_worktree_add(out, repo, name, path, &opts);
}

typedef struct {
    size_t name_offset;
    git_oid id;
    git_filemode_t mode;
} task_tree_entry;

static int task_write_tree(git_oid *out, git_repository *repo,
                           const char *names, const task_tree_entry *entries,
                           size_t count, const char **failed_op) {
    git_treebuilder *builder = NULL;
    int error = git_treebuilder_new(&builder, repo, NULL);
    if (error < 0) {
        *failed_op = "create tree builder";
        return error;
    }
    for (size_t i = 0; i < count; i++) {
        error = git_treebuilder_insert(NULL, builder, names + entries[i].name_offset,
                                       &entries[i].id, entries[i].mode);
        if (error < 0) {
            *failed_op = "insert tree entry";
            git_treebuilder_free(builder);
            return error;
        }
    }
    error = git_treebuilder_write(out, builder);
    if (error < 0) {
        *failed_op = "write tree";
    }
    git_treebuilder_free(builder);
    return error;
}
*/
import "C"

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// ID is a full SHA-1 object ID. The all-zero ID denotes an absent reference in
// expectations, never an abbreviation or a revision expression.
type ID [20]byte

func (id ID) String() string { return hex.EncodeToString(id[:]) }

func nativeID(id ID) C.git_oid {
	var result C.git_oid
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&result.id[0])), len(id)), id[:])
	return result
}

func objectID(id *C.git_oid) ID {
	var result ID
	copy(result[:], unsafe.Slice((*byte)(unsafe.Pointer(&id.id[0])), len(result)))
	return result
}

// Error preserves native codes so callers need not classify localized messages.
type Error struct {
	Operation string
	Code      int
	Class     int
	Message   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s (libgit2 %d)", e.Operation, e.Message, e.Code)
}

const (
	NotFound = int(C.GIT_ENOTFOUND)
	Locked   = int(C.GIT_ELOCKED)
)

func HasCode(err error, code int) bool {
	var native *Error
	return errors.As(err, &native) && native.Code == code
}

// Call only on the same locked OS thread as the failed native call: libgit2's
// error storage is thread-local, whereas Go goroutines can migrate at cgo calls.
func nativeError(op string, code C.int) error {
	if code >= 0 {
		return nil
	}
	err := &Error{Operation: op, Code: int(code), Message: "native operation failed"}
	if last := C.git_error_last(); last != nil {
		err.Class = int(last.klass)
		err.Message = C.GoString(last.message)
	}
	return err
}

var library struct {
	sync.Once
	home string
	err  error
}

func initialize() error {
	library.Do(func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if C.task_dynamic_linked() != 1 {
			library.err = errors.New("tasktool requires dynamically linked libgit2; install the shared library and rebuild using pkg-config (static linking is not supported)")
			return
		}
		var major, minor, patch C.int
		C.git_libgit2_version(&major, &minor, &patch)
		if major != 1 || minor != 9 {
			library.err = fmt.Errorf("tasktool requires dynamically linked libgit2 1.9.x; loaded %d.%d.%d; install the supported library and rebuild", major, minor, patch)
			return
		}
		if C.git_libgit2_features()&C.GIT_FEATURE_SHA256 != 0 {
			library.err = errors.New("tasktool was built for the stable SHA-1 libgit2 ABI but loaded an experimental SHA-256 library; select matching shared libgit2 1.9.x and rebuild")
			return
		}
		library.err = nativeError("initialize libgit2", C.git_libgit2_init())
		if library.err == nil {
			library.err = initializeEncodingFilter()
		}
		if library.err == nil {
			// libgit2 resolves its process-lifetime home directory during init.
			// Capture it once so ~/ config paths avoid per-call cgo crossings.
			tilde := C.CString("~")
			var expanded C.git_buf
			if C.git_config_parse_path(&expanded, tilde) == 0 && expanded.ptr != nil {
				library.home = C.GoString(expanded.ptr)
			}
			C.git_buf_dispose(&expanded)
			free(tilde)
		}
	})
	// Keep libgit2 initialized for the process lifetime. Shutting it down while
	// another goroutine holds native objects would invalidate those objects.
	return library.err
}

func cstring(value string) (*C.char, error) {
	if strings.ContainsRune(value, 0) {
		return nil, errors.New("Git argument contains NUL")
	}
	return C.CString(value), nil
}

func free(value *C.char) { C.free(unsafe.Pointer(value)) }

// Repository owns one native handle. Calls and Close are serialized; separate
// handles are used at transaction boundaries rather than sharing cached refs.
type Repository struct {
	mu            sync.Mutex
	ptr           *C.git_repository
	configuration *environmentConfiguration
}

var ErrClosed = errors.New("Git handle is closed")

func (r *Repository) call(operation string, f func(*C.git_repository) error) error {
	return r.callNative(operation, true, f)
}

// An operation over already identified immutable objects can skip refreshing
// repository configuration, but still needs the same handle and thread guard.
func (r *Repository) callNative(operation string, configured bool, f func(*C.git_repository) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ptr == nil {
		return fmt.Errorf("%s: %w", operation, ErrClosed)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if configured && r.configuration != nil {
		return r.configuration.call(r.ptr, operation, f)
	}
	return f(r.ptr)
}

// CheckoutSummary captures paths, HEAD, and an optional named reference from a
// freshly opened repository under the same configuration validation pass.
// Head and reference errors are returned on the summary so callers can check
// repository identity (Paths) before inspecting HEAD or refs.
type CheckoutSummary struct {
	Paths     Paths
	Head      Head
	HeadErr   error
	Reference Reference
	RefErr    error
}

// Open discovers a repository from a checkout, subdirectory, or Git directory.
// The explicit path determines identity; unsupported ambient overrides fail
// explicitly rather than silently redirecting or changing private worktrees.
func Open(path string) (*Repository, error) {
	repo, _, err := openRepository(path, "", false)
	return repo, err
}

// OpenCheckout opens a repository like Open and immediately reads Paths, Head,
// and (when refName is nonempty) Reference(refName) under the same initial
// configuration application, avoiding redundant follow-up config refreshes.
func OpenCheckout(path, refName string) (*Repository, CheckoutSummary, error) {
	return openRepository(path, refName, true)
}

func openRepository(path, refName string, inspect bool) (*Repository, CheckoutSummary, error) {
	var summary CheckoutSummary
	if err := validateEnvironment(); err != nil {
		return nil, summary, err
	}
	if err := initialize(); err != nil {
		return nil, summary, err
	}
	configuration, err := prepareEnvironmentConfiguration()
	if err != nil {
		return nil, summary, err
	}
	retained := false
	defer func() {
		if !retained {
			configuration.close()
		}
	}()
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, summary, err
	}
	name, err := cstring(path)
	if err != nil {
		return nil, summary, err
	}
	defer free(name)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var ceiling *C.char
	if value := os.Getenv("GIT_CEILING_DIRECTORIES"); value != "" {
		ceiling, err = cstring(value)
		if err != nil {
			return nil, summary, err
		}
		defer free(ceiling)
	}
	var flags C.uint
	switch strings.ToLower(os.Getenv("GIT_DISCOVERY_ACROSS_FILESYSTEM")) {
	case "", "0", "false", "no", "off":
	case "1", "true", "yes", "on":
		flags |= C.GIT_REPOSITORY_OPEN_CROSS_FS
	default:
		return nil, summary, errors.New("GIT_DISCOVERY_ACROSS_FILESYSTEM must be a boolean")
	}
	// Public discovery is read-only and does not perform ownership admission.
	var discovered C.git_buf
	defer C.git_buf_dispose(&discovered)
	across := C.int(0)
	if flags&C.GIT_REPOSITORY_OPEN_CROSS_FS != 0 {
		across = 1
	}
	if err := nativeError("discover ownership include context", C.git_repository_discover(&discovered, name, across, ceiling)); err != nil {
		return nil, summary, err
	}
	context, err := configurationAdmissionContext(C.GoString(discovered.ptr), "")
	if err != nil {
		return nil, summary, err
	}
	if err := configuration.validateConditionalOwnership(context); err != nil {
		return nil, summary, err
	}
	if err := validateBareDiscoveryPolicy(path, flags, ceiling, configuration); err != nil {
		return nil, summary, err
	}
	if err := validateRepositoryFormat(name, flags, ceiling); err != nil {
		return nil, summary, err
	}
	var repo *C.git_repository
	if err := nativeError("open repository", C.git_repository_open_ext(&repo, name, flags, ceiling)); err != nil {
		return nil, summary, err
	}
	if err := configuration.apply(repo); err != nil {
		C.git_repository_free(repo)
		return nil, summary, err
	}
	if inspect {
		summary.Paths = readPaths(repo)
		summary.Head, summary.HeadErr = readHead(repo)
		if refName != "" {
			summary.Reference, summary.RefErr = lookup(repo, refName)
		}
	}
	retained = true
	return &Repository{ptr: repo, configuration: configuration}, summary, nil
}

// Init creates a repository without reinitializing an existing one. For a
// separate Git directory, path is the Git directory and workdir is the checkout.
// For ordinary or bare repositories, leave workdir empty.
func Init(path, workdir string, bare bool) (*Repository, error) {
	if bare && workdir != "" {
		return nil, errors.New("a bare repository cannot have a workdir")
	}
	if err := validateEnvironment(); err != nil {
		return nil, err
	}
	if err := initialize(); err != nil {
		return nil, err
	}
	configuration, err := prepareEnvironmentConfiguration()
	if err != nil {
		return nil, err
	}
	retained := false
	defer func() {
		if !retained {
			configuration.close()
		}
	}()
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	name, err := cstring(path)
	if err != nil {
		return nil, err
	}
	defer free(name)
	var work *C.char
	if workdir != "" {
		absolute, err := filepath.Abs(workdir)
		if err != nil {
			return nil, err
		}
		work, err = cstring(absolute)
		if err != nil {
			return nil, err
		}
		defer free(work)
	}
	gitdir := path
	if !bare && workdir == "" && filepath.Base(gitdir) != ".git" {
		gitdir = filepath.Join(gitdir, ".git")
	}
	context, err := configurationAdmissionContext(gitdir, "refs/heads/main")
	if err != nil {
		return nil, err
	}
	if err := configuration.validateConditionalOwnership(context); err != nil {
		return nil, err
	}
	var flags C.int
	if bare {
		flags = 1
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var repo *C.git_repository
	if err := nativeError("initialize repository", C.task_repository_init(&repo, name, work, flags)); err != nil {
		return nil, err
	}
	if err := configuration.apply(repo); err != nil {
		C.git_repository_free(repo)
		return nil, err
	}
	retained = true
	return &Repository{ptr: repo, configuration: configuration}, nil
}

func (r *Repository) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ptr != nil {
		C.git_repository_free(r.ptr)
		r.ptr = nil
		if r.configuration != nil {
			r.configuration.close()
		}
	}
}

type Paths struct {
	GitDir    string
	CommonDir string
	WorkDir   string
	Bare      bool
}

func readPaths(repo *C.git_repository) Paths {
	var paths Paths
	paths.GitDir = filepath.Clean(C.GoString(C.git_repository_path(repo)))
	paths.CommonDir = filepath.Clean(C.GoString(C.git_repository_commondir(repo)))
	if work := C.git_repository_workdir(repo); work != nil {
		paths.WorkDir = filepath.Clean(C.GoString(work))
	}
	paths.Bare = C.git_repository_is_bare(repo) != 0
	return paths
}

func (r *Repository) Paths() (Paths, error) {
	var paths Paths
	err := r.call("repository paths", func(repo *C.git_repository) error {
		paths = readPaths(repo)
		return nil
	})
	return paths, err
}

// Reference distinguishes direct targets from symbolic aliases. Publication
// accepts only direct references, so a concurrent symbolic replacement cannot
// cause it to update a different ref by dereferencing an alias.
type Reference struct {
	ID       ID
	Symbolic string
}

func lookup(repo *C.git_repository, name string) (Reference, error) {
	var result Reference
	refname, err := cstring(name)
	if err != nil {
		return result, err
	}
	defer free(refname)
	var ref *C.git_reference
	if err := nativeError("lookup "+name, C.git_reference_lookup(&ref, repo, refname)); err != nil {
		return result, err
	}
	defer C.git_reference_free(ref)
	if C.git_reference_type(ref) == C.GIT_REFERENCE_SYMBOLIC {
		result.Symbolic = C.GoString(C.git_reference_symbolic_target(ref))
	} else {
		result.ID = objectID(C.git_reference_target(ref))
	}
	return result, nil
}

func (r *Repository) Reference(name string) (Reference, error) {
	var result Reference
	err := r.call("lookup reference", func(repo *C.git_repository) error {
		var err error
		result, err = lookup(repo, name)
		return err
	})
	return result, err
}

func (r *Repository) IsAncestor(ancestor, descendant ID) (bool, error) {
	var result bool
	err := r.callNative("check ancestry", false, func(repo *C.git_repository) error {
		a, d := nativeID(ancestor), nativeID(descendant)
		// Equality is ancestry too, but nonexistent or non-commit IDs are not.
		for _, id := range []*C.git_oid{&a, &d} {
			var commit *C.git_commit
			if err := nativeError("lookup ancestry commit", C.git_commit_lookup(&commit, repo, id)); err != nil {
				return err
			}
			C.git_commit_free(commit)
		}
		if ancestor == descendant {
			result = true
			return nil
		}
		code := C.git_graph_descendant_of(repo, &d, &a)
		if err := nativeError("check ancestry", code); err != nil {
			return err
		}
		result = code != 0
		return nil
	})
	return result, err
}

// WriteBlob writes bytes to the object database, without filters or worktree IO.
func (r *Repository) WriteBlob(data []byte) (ID, error) {
	var result ID
	err := r.callNative("write blob", false, func(repo *C.git_repository) error {
		var id C.git_oid
		var buffer unsafe.Pointer
		if len(data) != 0 {
			buffer = unsafe.Pointer(&data[0])
		}
		if err := nativeError("write blob", C.git_blob_create_from_buffer(&id, repo, buffer, C.size_t(len(data)))); err != nil {
			return err
		}
		result = objectID(&id)
		return nil
	})
	return result, err
}

type TreeEntry struct {
	Name string
	ID   ID
	Mode uint32
}

// WriteTree builds one tree level from entries. Nested trees are explicit tree
// entries, not slash-containing paths or paths interpreted as pathspecs.
func (r *Repository) WriteTree(entries []TreeEntry) (ID, error) {
	var result ID
	err := r.callNative("write tree", false, func(repo *C.git_repository) error {
		var names []byte
		var items []C.task_tree_entry
		if len(entries) > 0 {
			totalNameBytes := 0
			for _, entry := range entries {
				totalNameBytes += len(entry.Name) + 1
			}
			names = make([]byte, 0, totalNameBytes)
			items = make([]C.task_tree_entry, len(entries))
			var seen map[string]struct{}
			if len(entries) > 1 {
				seen = make(map[string]struct{}, len(entries))
			}
			for i, entry := range entries {
				if seen != nil {
					if _, duplicate := seen[entry.Name]; duplicate {
						return fmt.Errorf("duplicate tree entry %q", entry.Name)
					}
					seen[entry.Name] = struct{}{}
				}
				if strings.ContainsRune(entry.Name, 0) {
					return errors.New("Git argument contains NUL")
				}
				items[i] = C.task_tree_entry{
					name_offset: C.size_t(len(names)),
					id:          nativeID(entry.ID),
					mode:        C.git_filemode_t(entry.Mode),
				}
				names = append(names, entry.Name...)
				names = append(names, 0)
			}
		}
		var namesPtr *C.char
		var itemsPtr *C.task_tree_entry
		if len(items) > 0 {
			namesPtr = (*C.char)(unsafe.Pointer(&names[0]))
			itemsPtr = &items[0]
		}
		var id C.git_oid
		var failedOp *C.char
		code := C.task_write_tree(&id, repo, namesPtr, itemsPtr, C.size_t(len(items)), &failedOp)
		if code < 0 {
			op := "write tree"
			if failedOp != nil {
				op = C.GoString(failedOp)
			}
			return nativeError(op, code)
		}
		result = objectID(&id)
		return nil
	})
	return result, err
}

type Signature struct {
	Name  string
	Email string
	When  time.Time
}

func signature(value Signature) (*C.git_signature, error) {
	name, err := cstring(value.Name)
	if err != nil {
		return nil, err
	}
	defer free(name)
	email, err := cstring(value.Email)
	if err != nil {
		return nil, err
	}
	defer free(email)
	_, offset := value.When.Zone()
	var sig *C.git_signature
	if err := nativeError("create signature", C.git_signature_new(&sig, name, email, C.git_time_t(value.When.Unix()), C.int(offset/60))); err != nil {
		return nil, err
	}
	return sig, nil
}

// WriteCommit creates an object only. Updating a branch requires a separately
// prepared reference transaction with explicit expected old values.
func (r *Repository) WriteCommit(treeID ID, parents []ID, author, committer Signature, message string) (ID, error) {
	var result ID
	err := r.callNative("write commit", false, func(repo *C.git_repository) error {
		text, err := cstring(message)
		if err != nil {
			return err
		}
		defer free(text)
		a, err := signature(author)
		if err != nil {
			return err
		}
		defer C.git_signature_free(a)
		c, err := signature(committer)
		if err != nil {
			return err
		}
		defer C.git_signature_free(c)
		var tree *C.git_tree
		id := nativeID(treeID)
		if err := nativeError("lookup commit tree", C.git_tree_lookup(&tree, repo, &id)); err != nil {
			return err
		}
		defer C.git_tree_free(tree)
		var parentList **C.git_commit
		if len(parents) > 0 {
			memory := C.calloc(C.size_t(len(parents)), C.size_t(unsafe.Sizeof(parentList)))
			if memory == nil {
				return errors.New("allocate commit parents: out of memory")
			}
			defer C.free(memory)
			parentList = (**C.git_commit)(memory)
			slots := unsafe.Slice(parentList, len(parents))
			for i, parent := range parents {
				id := nativeID(parent)
				if err := nativeError("lookup commit parent", C.git_commit_lookup(&slots[i], repo, &id)); err != nil {
					return err
				}
				defer C.git_commit_free(slots[i])
			}
		}
		var commitID C.git_oid
		if err := nativeError("write commit", C.git_commit_create(&commitID, repo, nil, a, c, nil, text, tree, C.size_t(len(parents)), parentList)); err != nil {
			return err
		}
		result = objectID(&commitID)
		return nil
	})
	return result, err
}

// AddWorktree checks out an existing branch. Branch creation is a separate
// compare-and-create operation; no existing branch is reset or overwritten.
func (r *Repository) AddWorktree(name, path, branch string) error {
	if !strings.HasPrefix(branch, "refs/heads/") {
		return errors.New("worktree requires a fully qualified branch reference")
	}
	return r.call("add worktree", func(repo *C.git_repository) error {
		if err := validateRepositoryIndex(repo); err != nil {
			return err
		}
		n, err := cstring(name)
		if err != nil {
			return err
		}
		defer free(n)
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		p, err := cstring(absolute)
		if err != nil {
			return err
		}
		defer free(p)
		b, err := cstring(branch)
		if err != nil {
			return err
		}
		defer free(b)
		var ref *C.git_reference
		if err := nativeError("lookup worktree branch", C.git_reference_lookup(&ref, repo, b)); err != nil {
			return err
		}
		defer C.git_reference_free(ref)
		if C.git_reference_type(ref) != C.GIT_REFERENCE_DIRECT {
			return errors.New("worktree branch must not be symbolic")
		}
		tree, err := commitTree(repo, objectID(C.git_reference_target(ref)))
		if err != nil {
			return err
		}
		defer C.git_tree_free(tree)
		config, err := r.prospectiveWorktreeConfiguration(repo, name, absolute, branch)
		if err != nil {
			return err
		}
		defer C.git_config_free(config)
		if err := preflightWorktree(repo, config, tree, nil); err != nil {
			return err
		}
		var worktree *C.git_worktree
		if err := nativeError("add worktree", C.task_worktree_add(&worktree, repo, n, p, ref)); err != nil {
			return err
		}
		C.git_worktree_free(worktree)
		return materializeWorktree(p, config, tree)
	})
}
