package git

/*
#include <git2.h>

static int task_status(git_status_list **out, git_repository *repo, int ignored) {
    git_status_options options = GIT_STATUS_OPTIONS_INIT;
    options.show = GIT_STATUS_SHOW_INDEX_AND_WORKDIR;
    options.flags = GIT_STATUS_OPT_INCLUDE_UNTRACKED |
        GIT_STATUS_OPT_RECURSE_UNTRACKED_DIRS | GIT_STATUS_OPT_NO_REFRESH |
        GIT_STATUS_OPT_INCLUDE_UNREADABLE;
    if (ignored) options.flags |= GIT_STATUS_OPT_INCLUDE_IGNORED |
        GIT_STATUS_OPT_RECURSE_IGNORED_DIRS;
    return git_status_list_new(out, repo, &options);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"
)

// Head reports both the checked-out branch and resolved commit. An unborn
// branch has a name and zero ID; a detached HEAD has an ID and empty branch.
// Symbolic branch aliases are rejected, not silently treated as their targets.
type Head struct {
	Branch string
	ID     ID
}

func (r *Repository) Head() (Head, error) {
	var result Head
	err := r.call("read HEAD", func(repo *C.git_repository) error {
		var err error
		result, err = readHead(repo)
		return err
	})
	return result, err
}

// HeadWorkdir returns the workdir under the same configuration snapshot and
// repository lock as HEAD validation. It retains Head's unborn-branch handling
// and refusals without a separate Paths operation. It does not cache discovery.
func (r *Repository) HeadWorkdir() (string, error) {
	var workdir string
	err := r.call("read HEAD workdir", func(repo *C.git_repository) error {
		if _, err := readHead(repo); err != nil {
			return err
		}
		if work := C.git_repository_workdir(repo); work != nil {
			workdir = filepath.Clean(C.GoString(work))
		}
		return nil
	})
	return workdir, err
}

func readHead(repo *C.git_repository) (Head, error) {
	var result Head
	head, err := lookup(repo, "HEAD")
	if err != nil {
		return result, err
	}
	if head.Symbolic == "" {
		result.ID = head.ID
	} else {
		if !strings.HasPrefix(head.Symbolic, "refs/heads/") {
			return result, fmt.Errorf("HEAD refers to a non-branch reference: %s", head.Symbolic)
		}
		result.Branch = head.Symbolic
		target, err := lookup(repo, result.Branch)
		if HasCode(err, NotFound) {
			return result, nil
		}
		if err != nil {
			return result, err
		}
		if target.Symbolic != "" {
			return result, fmt.Errorf("HEAD branch is symbolic: %s", result.Branch)
		}
		result.ID = target.ID
	}
	id := nativeID(result.ID)
	var commit *C.git_commit
	if err := nativeError("resolve HEAD commit", C.git_commit_lookup(&commit, repo, &id)); err != nil {
		return result, err
	}
	C.git_commit_free(commit)
	return result, nil
}

// ResolveCommit accepts Git revision expressions, but never command-line
// options. It peels annotated tags and refuses non-commit objects.
func (r *Repository) ResolveCommit(revision string) (ID, error) {
	var result ID
	err := r.call("resolve commit", func(repo *C.git_repository) error {
		name, err := cstring(revision)
		if err != nil {
			return err
		}
		defer free(name)
		var object, commit *C.git_object
		if err := nativeError("parse revision", C.git_revparse_single(&object, repo, name)); err != nil {
			return err
		}
		defer C.git_object_free(object)
		if err := nativeError("peel commit", C.git_object_peel(&commit, object, C.GIT_OBJECT_COMMIT)); err != nil {
			return err
		}
		defer C.git_object_free(commit)
		result = objectID(C.git_object_id(commit))
		return nil
	})
	return result, err
}

// References returns an independent, freshly enumerated snapshot. Prefixes
// are literal: "refs/heads/main" also matches "refs/heads/main-extra".
func (r *Repository) References(prefix string) (map[string]Reference, error) {
	result := make(map[string]Reference)
	err := r.call("list references", func(repo *C.git_repository) error {
		var iterator *C.git_reference_iterator
		if err := nativeError("create reference iterator", C.git_reference_iterator_new(&iterator, repo)); err != nil {
			return err
		}
		defer C.git_reference_iterator_free(iterator)
		for {
			var ref *C.git_reference
			code := C.git_reference_next(&ref, iterator)
			if code == C.GIT_ITEROVER {
				return nil
			}
			if err := nativeError("iterate references", code); err != nil {
				return err
			}
			name := C.GoString(C.git_reference_name(ref))
			if strings.HasPrefix(name, prefix) {
				value := Reference{}
				if C.git_reference_type(ref) == C.GIT_REFERENCE_SYMBOLIC {
					value.Symbolic = C.GoString(C.git_reference_symbolic_target(ref))
				} else {
					value.ID = objectID(C.git_reference_target(ref))
				}
				result[name] = value
			}
			C.git_reference_free(ref)
		}
	})
	return result, err
}

// MergeBases returns all best common ancestors, including criss-cross merges.
// An empty result means independent histories; missing objects remain errors.
func (r *Repository) MergeBases(a, b ID) ([]ID, error) {
	var result []ID
	err := r.call("find merge bases", func(repo *C.git_repository) error {
		left, right := nativeID(a), nativeID(b)
		// ENOTFOUND can mean either unrelated histories or a missing object.
		for _, id := range []*C.git_oid{&left, &right} {
			var commit *C.git_commit
			if err := nativeError("lookup merge input", C.git_commit_lookup(&commit, repo, id)); err != nil {
				return err
			}
			C.git_commit_free(commit)
		}
		var ids C.git_oidarray
		defer C.git_oidarray_dispose(&ids)
		code := C.git_merge_bases(&ids, repo, &left, &right)
		if code == C.GIT_ENOTFOUND {
			return nil
		}
		if err := nativeError("find merge bases", code); err != nil {
			return err
		}
		for _, id := range unsafe.Slice(ids.ids, int(ids.count)) {
			result = append(result, objectID(&id))
		}
		return nil
	})
	return result, err
}

func commitTree(repo *C.git_repository, revision ID) (*C.git_tree, error) {
	id := nativeID(revision)
	var commit *C.git_commit
	if err := nativeError("lookup commit", C.git_commit_lookup(&commit, repo, &id)); err != nil {
		return nil, err
	}
	defer C.git_commit_free(commit)
	var tree *C.git_tree
	if err := nativeError("read commit tree", C.git_commit_tree(&tree, commit)); err != nil {
		return nil, err
	}
	return tree, nil
}

func treeEntries(tree *C.git_tree) []TreeEntry {
	result := make([]TreeEntry, 0, int(C.git_tree_entrycount(tree)))
	for i := C.size_t(0); i < C.git_tree_entrycount(tree); i++ {
		entry := C.git_tree_entry_byindex(tree, i)
		result = append(result, TreeEntry{
			Name: C.GoString(C.git_tree_entry_name(entry)),
			ID:   objectID(C.git_tree_entry_id(entry)),
			Mode: uint32(C.git_tree_entry_filemode_raw(entry)),
		})
	}
	return result
}

// Tree returns root entries of a commit, without touching its checkout/index.
func (r *Repository) Tree(revision ID) ([]TreeEntry, error) {
	var result []TreeEntry
	err := r.call("read tree", func(repo *C.git_repository) error {
		tree, err := commitTree(repo, revision)
		if err != nil {
			return err
		}
		defer C.git_tree_free(tree)
		result = treeEntries(tree)
		return nil
	})
	return result, err
}

// TreeAt returns the immediate entries of a literal directory in a commit.
func (r *Repository) TreeAt(revision ID, path string) ([]TreeEntry, error) {
	entry, err := r.Entry(revision, path)
	if err != nil {
		return nil, err
	}
	if entry.Mode != 0040000 {
		return nil, fmt.Errorf("expected a directory in the committed tree: %q", path)
	}
	var result []TreeEntry
	err = r.call("read subtree", func(repo *C.git_repository) error {
		var tree *C.git_tree
		id := nativeID(entry.ID)
		if err := nativeError("lookup subtree", C.git_tree_lookup(&tree, repo, &id)); err != nil {
			return err
		}
		defer C.git_tree_free(tree)
		result = treeEntries(tree)
		return nil
	})
	return result, err
}

func literalPath(path string) error {
	if path == "" || strings.ContainsRune(path, 0) || strings.HasPrefix(path, "/") {
		return fmt.Errorf("expected a literal repository-relative path: %q", path)
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return fmt.Errorf("unsafe repository-relative path: %q", path)
		}
	}
	return nil
}

// Entry reads one literal path at a commit; no revision/pathspec parsing is
// applied to path, and symlinks are entries, never followed on the filesystem.
func (r *Repository) Entry(revision ID, path string) (TreeEntry, error) {
	var result TreeEntry
	if err := literalPath(path); err != nil {
		return result, err
	}
	err := r.call("read tree entry", func(repo *C.git_repository) error {
		tree, err := commitTree(repo, revision)
		if err != nil {
			return err
		}
		defer C.git_tree_free(tree)
		name, _ := cstring(path)
		defer free(name)
		var entry *C.git_tree_entry
		if err := nativeError("lookup tree path", C.git_tree_entry_bypath(&entry, tree, name)); err != nil {
			return err
		}
		defer C.git_tree_entry_free(entry)
		result = TreeEntry{Name: path, ID: objectID(C.git_tree_entry_id(entry)), Mode: uint32(C.git_tree_entry_filemode_raw(entry))}
		return nil
	})
	return result, err
}

// Blob returns uninterpreted object bytes (no filters, text decoding or CRLF
// conversion), for metadata and revision-pinned completion evidence.
func (r *Repository) Blob(id ID) ([]byte, error) {
	var result []byte
	err := r.call("read blob", func(repo *C.git_repository) error {
		native := nativeID(id)
		var blob *C.git_blob
		if err := nativeError("lookup blob", C.git_blob_lookup(&blob, repo, &native)); err != nil {
			return err
		}
		defer C.git_blob_free(blob)
		size := uint64(C.git_blob_rawsize(blob))
		if size > uint64(int(^uint(0)>>1)) {
			return errors.New("blob does not fit in memory")
		}
		result = append([]byte{}, unsafe.Slice((*byte)(C.git_blob_rawcontent(blob)), int(size))...)
		return nil
	})
	return result, err
}

// HistoryPaths observes every root entry in every reachable commit, plus
// entries up to depth levels within directory (if nonempty). Visiting trees
// includes deleted files and both sides of merges.
func (r *Repository) HistoryPaths(tips []ID, directory string, depth int) ([]string, error) {
	paths := make(map[string]bool)
	err := r.call("read historical paths", func(repo *C.git_repository) error {
		seen := make(map[TreeEntry]bool)
		var visit func(TreeEntry, int) error
		visit = func(entry TreeEntry, depth int) error {
			paths[entry.Name] = true
			if depth <= 0 || entry.Mode != 0040000 || seen[entry] {
				return nil
			}
			seen[entry] = true
			var tree *C.git_tree
			id := nativeID(entry.ID)
			if err := nativeError("read historical subtree", C.git_tree_lookup(&tree, repo, &id)); err != nil {
				return err
			}
			defer C.git_tree_free(tree)
			for _, child := range treeEntries(tree) {
				child.Name = entry.Name + "/" + child.Name
				if err := visit(child, depth-1); err != nil {
					return err
				}
			}
			return nil
		}
		var walk *C.git_revwalk
		if err := nativeError("create history walker", C.git_revwalk_new(&walk, repo)); err != nil {
			return err
		}
		defer C.git_revwalk_free(walk)
		for _, tip := range tips {
			id := nativeID(tip)
			if err := nativeError("add history tip", C.git_revwalk_push(walk, &id)); err != nil {
				return err
			}
		}
		for {
			var id C.git_oid
			code := C.git_revwalk_next(&id, walk)
			if code == C.GIT_ITEROVER {
				return nil
			}
			if err := nativeError("walk history", code); err != nil {
				return err
			}
			tree, err := commitTree(repo, objectID(&id))
			if err != nil {
				return err
			}
			entries := treeEntries(tree)
			C.git_tree_free(tree)
			for _, entry := range entries {
				if entry.Name == directory {
					if err := visit(entry, depth); err != nil {
						return err
					}
				} else {
					paths[entry.Name] = true
				}
			}
		}
	})
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result, err
}

// Status flags keep index changes separate from worktree changes. Renames are
// deliberately not coalesced: both literal paths must be protected by callers.
type Status uint32

const (
	IndexNew           Status = C.GIT_STATUS_INDEX_NEW
	IndexModified      Status = C.GIT_STATUS_INDEX_MODIFIED
	IndexDeleted       Status = C.GIT_STATUS_INDEX_DELETED
	IndexRenamed       Status = C.GIT_STATUS_INDEX_RENAMED
	IndexTypeChange    Status = C.GIT_STATUS_INDEX_TYPECHANGE
	WorktreeNew        Status = C.GIT_STATUS_WT_NEW
	WorktreeModified   Status = C.GIT_STATUS_WT_MODIFIED
	WorktreeDeleted    Status = C.GIT_STATUS_WT_DELETED
	WorktreeTypeChange Status = C.GIT_STATUS_WT_TYPECHANGE
	WorktreeRenamed    Status = C.GIT_STATUS_WT_RENAMED
	WorktreeUnreadable Status = C.GIT_STATUS_WT_UNREADABLE
	Ignored            Status = C.GIT_STATUS_IGNORED
	Conflicted         Status = C.GIT_STATUS_CONFLICTED
)

type Change struct {
	Path   string
	Status Status
}

// DescribeChanges names the actual status categories, with bounded path samples.
func DescribeChanges(changes []Change) string {
	groups := []struct {
		name string
		mask Status
	}{
		{"conflicted", Conflicted},
		{"staged", IndexNew | IndexModified | IndexDeleted | IndexRenamed | IndexTypeChange},
		{"unstaged", WorktreeModified | WorktreeDeleted | WorktreeTypeChange | WorktreeRenamed},
		{"untracked", WorktreeNew},
		{"unreadable", WorktreeUnreadable},
		{"ignored", Ignored},
	}
	parts := []string{}
	for _, group := range groups {
		count := 0
		paths := []string{}
		for _, change := range changes {
			if change.Status&group.mask == 0 {
				continue
			}
			count++
			if len(paths) < 3 {
				paths = append(paths, fmt.Sprintf("%q", change.Path))
			}
		}
		if count > 0 {
			if count > len(paths) {
				paths = append(paths, fmt.Sprintf("and %d more", count-len(paths)))
			}
			parts = append(parts, fmt.Sprintf("%d %s (%s)", count, group.name, strings.Join(paths, ", ")))
		}
	}
	if len(parts) == 0 && len(changes) != 0 {
		return fmt.Sprintf("%d changes with unknown status", len(changes))
	}
	return strings.Join(parts, "; ")
}

// libgit2 1.9 can omit unreadable directories even with INCLUDE_UNREADABLE.
// Check directory access independently so an incomplete scan cannot report a
// clean checkout. Ignore rules may skip untracked trees, never tracked paths.
func readableDirectories(repo *C.git_repository, index *C.git_index, includeIgnored bool) error {
	root := C.GoString(C.git_repository_workdir(repo))
	tracked := make(map[string]bool)
	for i := C.size_t(0); i < C.git_index_entrycount(index); i++ {
		entry := C.git_index_get_byindex(index, i)
		for parent := filepath.Dir(C.GoString(entry.path)); parent != "." && parent != string(filepath.Separator); parent = filepath.Dir(parent) {
			tracked[parent] = true
		}
	}
	var visit func(string) error
	visit = func(relative string) error {
		entries, err := os.ReadDir(filepath.Join(root, relative))
		if err != nil {
			return fmt.Errorf("inspect worktree directories: %w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == ".git" {
				continue
			}
			path := filepath.Join(relative, entry.Name())
			if !includeIgnored && !tracked[path] {
				name, err := cstring(filepath.ToSlash(path) + "/")
				if err != nil {
					return err
				}
				var ignored C.int
				code := C.git_ignore_path_is_ignored(&ignored, repo, name)
				free(name)
				if err := nativeError("check directory ignore rules", code); err != nil {
					return err
				}
				if ignored != 0 {
					continue
				}
			}
			if err := visit(path); err != nil {
				return err
			}
		}
		return nil
	}
	return visit("")
}

// Status never refreshes/writes the real index, including on read-only query
// paths. A fresh index read avoids cached state on a long-lived native handle.
func (r *Repository) Status(includeIgnored bool) ([]Change, error) {
	if err := r.validateIndex(); err != nil {
		return nil, err
	}
	var result []Change
	err := r.call("read status", func(repo *C.git_repository) error {
		restore := encodingComparison(repo)
		defer restore()
		var index *C.git_index
		if err := nativeError("open status index", C.git_repository_index(&index, repo)); err != nil {
			return err
		}
		defer C.git_index_free(index)
		if err := nativeError("refresh status index", C.git_index_read(index, 1)); err != nil {
			return err
		}
		var flags C.int
		if includeIgnored {
			flags = 1
		}
		var list *C.git_status_list
		if err := nativeError("read status", C.task_status(&list, repo, flags)); err != nil {
			return err
		}
		defer C.git_status_list_free(list)
		for i := C.size_t(0); i < C.git_status_list_entrycount(list); i++ {
			entry := C.git_status_byindex(list, i)
			delta := entry.index_to_workdir
			if delta == nil {
				delta = entry.head_to_index
			}
			if delta == nil {
				return errors.New("status entry has no path")
			}
			path := C.GoString(delta.old_file.path)
			status := Status(entry.status)
			tracked := C.git_index_get_bypath(index, delta.old_file.path, 0)
			if tracked != nil && tracked.flags_extended&C.GIT_INDEX_ENTRY_SKIP_WORKTREE != 0 {
				if _, err := os.Lstat(filepath.Join(C.GoString(C.git_repository_workdir(repo)), filepath.FromSlash(path))); errors.Is(err, os.ErrNotExist) {
					// Absence is intentional, but staged deltas remain visible.
					status &^= WorktreeDeleted
				}
			}
			if status != 0 {
				result = append(result, Change{Path: path, Status: status})
			}
		}
		return readableDirectories(repo, index, includeIgnored)
	})
	return result, err
}

// Config reads a fresh effective configuration snapshot. NotFound is distinct
// from a configured empty string. Callers choose their own fallback policy.
func (r *Repository) Config(key string) (string, error) {
	var result string
	err := r.call("read configuration", func(repo *C.git_repository) error {
		var config *C.git_config
		if err := nativeError("open configuration", C.git_repository_config_snapshot(&config, repo)); err != nil {
			return err
		}
		defer C.git_config_free(config)
		name, err := cstring(key)
		if err != nil {
			return err
		}
		defer free(name)
		var value *C.char
		if err := nativeError("read configuration value", C.git_config_get_string(&value, config, name)); err != nil {
			return err
		}
		result = C.GoString(value)
		return nil
	})
	return result, err
}

// ConfigBool preserves Git's distinction between a valueless boolean (true)
// and an explicitly empty value (false), including command configuration.
func (r *Repository) ConfigBool(key string) (bool, error) {
	var result bool
	err := r.call("read boolean configuration", func(repo *C.git_repository) error {
		var config *C.git_config
		if err := nativeError("open configuration", C.git_repository_config_snapshot(&config, repo)); err != nil {
			return err
		}
		defer C.git_config_free(config)
		name, err := cstring(key)
		if err != nil {
			return err
		}
		defer free(name)
		var value C.int
		if err := nativeError("read boolean configuration value", C.git_config_get_bool(&value, config, name)); err != nil {
			return err
		}
		result = value != 0
		return nil
	})
	return result, err
}

// SetLocalConfig updates only this repository's local configuration, never
// a user's system/global settings. libgit2 owns its config-file lock protocol.
func (r *Repository) SetLocalConfig(key, value string) error {
	return r.call("write local configuration", func(repo *C.git_repository) error {
		var effective, local *C.git_config
		if err := nativeError("open configuration", C.git_repository_config(&effective, repo)); err != nil {
			return err
		}
		defer C.git_config_free(effective)
		if err := nativeError("open local configuration", C.git_config_open_level(&local, effective, C.GIT_CONFIG_LEVEL_LOCAL)); err != nil {
			return err
		}
		defer C.git_config_free(local)
		name, err := cstring(key)
		if err != nil {
			return err
		}
		defer free(name)
		text, err := cstring(value)
		if err != nil {
			return err
		}
		defer free(text)
		return nativeError("set local configuration", C.git_config_set_string(local, name, text))
	})
}
