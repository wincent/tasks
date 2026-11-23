package git

/*
#include <git2.h>
#include <string.h>

// Git sorts a directory as if its basename ends in '/'. Include that
// implicit byte when comparing a shorter name to the search prefix.
static int history_prefix_cmp(const git_tree_entry *entry, const char *prefix, size_t n) {
 const char *name = git_tree_entry_name(entry);
 size_t len = strlen(name);
 if (len >= n) return memcmp(name, prefix, n);
 int order = memcmp(name, prefix, len);
 if (order) return order;
 return (git_tree_entry_type(entry) == GIT_OBJECT_TREE ? '/' : 0) - (unsigned char)prefix[len];
}

static size_t history_find_record(const git_tree_entry **out, git_tree *tree,
 const char *prefix, size_t n) {
 size_t count = git_tree_entrycount(tree);
 size_t lo = 0, hi = count;
 while (lo < hi) {
  size_t mid = lo + (hi - lo) / 2;
  if (history_prefix_cmp(git_tree_entry_byindex(tree, mid), prefix, n) < 0)
   lo = mid + 1;
  else
   hi = mid;
 }
 for (; lo < count; lo++) {
  const git_tree_entry *entry = git_tree_entry_byindex(tree, lo);
  if (history_prefix_cmp(entry, prefix, n)) break;
  const char *name = git_tree_entry_name(entry) + n;
  size_t len = strlen(name);
  // Match the rest of the ticket filename: .+\.md (no linefeed).
  if (len <= 3 || memcmp(name + len - 3, ".md", 3) || strchr(name, '\n'))
   continue;
  if (*out) return 2;
  *out = entry;
 }
 return *out ? 1 : 0;
}
*/
import "C"

import (
	"fmt"
	"strings"
)

// HistoryRecord searches the immediate root and the given archive bucket,
// using a literal "PREFIX-N " prefix and a nonempty title followed by ".md".
// It checks uniqueness and file mode without walking unrelated directories
// or building a whole-tree index for a single ticket.
func (r *Repository) HistoryRecord(root ID, bucket, prefix string) (TreeEntry, error) {
	var found TreeEntry
	err := r.callNative("look up historical record", false, func(repo *C.git_repository) error {
		p, err := cstring(prefix)
		if err != nil {
			return err
		}
		defer free(p)
		load := func(id *C.git_oid) (*C.git_tree, error) {
			var tree *C.git_tree
			err := nativeError("load history lookup tree", C.git_tree_lookup(&tree, repo, id))
			return tree, err
		}
		id := nativeID(root)
		tree, err := load(&id)
		if err != nil {
			return err
		}
		defer C.git_tree_free(tree)
		search := func(tree *C.git_tree) error {
			var entry *C.git_tree_entry
			count := C.history_find_record(&entry, tree, p, C.size_t(len(prefix)))
			if count > 1 || count == 1 && found.Name != "" {
				return fmt.Errorf("ambiguous committed record")
			}
			if count == 1 {
				mode := uint32(C.git_tree_entry_filemode_raw(entry))
				if mode != 0100644 && mode != 0100755 {
					return fmt.Errorf("not a regular committed record")
				}
				found = TreeEntry{
					Name: C.GoString(C.git_tree_entry_name(entry)),
					ID:   objectID(C.git_tree_entry_id(entry)),
					Mode: mode,
				}
			}
			return nil
		}
		if err := search(tree); err != nil {
			return err
		}
		dir := tree
		var path string
		for part := range strings.SplitSeq(bucket, "/") {
			if path == "" {
				path = part
			} else {
				path += "/" + part
			}
			name, err := cstring(part)
			if err != nil {
				return err
			}
			entry := C.git_tree_entry_byname(dir, name)
			free(name)
			if entry == nil {
				return nil
			}
			if C.git_tree_entry_filemode_raw(entry) != 0040000 {
				if path == "archive" {
					return fmt.Errorf("archive directory is obstructed in tasks: %s", path)
				}
				return fmt.Errorf("expected a directory in the committed tree: %q", path)
			}
			dir, err = load(C.git_tree_entry_id(entry))
			if err != nil {
				return err
			}
			defer C.git_tree_free(dir)
		}
		return search(dir)
	})
	return found, err
}
