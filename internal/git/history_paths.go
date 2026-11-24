package git

/*
#include <git2.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	uint64_t hash;
	size_t offset; // 1-based byte offset in data (0 = empty)
	size_t len;
} task_byteset_slot;

typedef struct {
	task_byteset_slot *slots;
	size_t cap;
	size_t count;
	char *data;
	size_t data_len;
	size_t data_cap;
} task_byteset;

typedef struct {
	char *data;
	size_t cap;
} task_scratch;

typedef struct {
	git_repository *repo;
	task_byteset paths;
	task_byteset seen_roots;
	task_byteset seen_subtrees;
	task_scratch path_buf;
	task_scratch key_buf;
	const char **failed_op;
} task_history_ctx;

static uint64_t task_fnv1a(const void *ptr, size_t len) {
	const unsigned char *p = (const unsigned char *)ptr;
	uint64_t h = 14695981039346656037ULL;
	for (size_t i = 0; i < len; i++) {
		h ^= (uint64_t)p[i];
		h *= 1099511628211ULL;
	}
	return h;
}

static void task_byteset_free(task_byteset *s) {
	free(s->slots);
	free(s->data);
	s->slots = NULL;
	s->data = NULL;
	s->cap = 0;
	s->count = 0;
	s->data_len = 0;
	s->data_cap = 0;
}

static int task_byteset_grow_slots(task_byteset *s) {
	size_t new_cap = s->cap ? s->cap * 2 : 64;
	if (new_cap < s->cap || new_cap > SIZE_MAX / sizeof(task_byteset_slot)) {
		return -1;
	}
	task_byteset_slot *new_slots = (task_byteset_slot *)calloc(new_cap, sizeof(task_byteset_slot));
	if (!new_slots) {
		return -1;
	}
	size_t mask = new_cap - 1;
	for (size_t i = 0; i < s->cap; i++) {
		if (s->slots[i].offset == 0) {
			continue;
		}
		size_t idx = (size_t)s->slots[i].hash & mask;
		while (new_slots[idx].offset != 0) {
			idx = (idx + 1) & mask;
		}
		new_slots[idx] = s->slots[i];
	}
	free(s->slots);
	s->slots = new_slots;
	s->cap = new_cap;
	return 0;
}

// Returns 1 if newly inserted, 0 if already present, -1 on OOM.
static int task_byteset_add(task_byteset *s, const void *ptr, size_t len, int nul_terminate) {
	if (s->cap == 0 || s->count + 1 > s->cap / 2) {
		if (task_byteset_grow_slots(s) < 0) {
			return -1;
		}
	}
	uint64_t hash = task_fnv1a(ptr, len);
	size_t mask = s->cap - 1;
	size_t idx = (size_t)hash & mask;
	while (s->slots[idx].offset != 0) {
		if (s->slots[idx].hash == hash && s->slots[idx].len == len &&
			(len == 0 || memcmp(s->data + (s->slots[idx].offset - 1), ptr, len) == 0)) {
			return 0;
		}
		idx = (idx + 1) & mask;
	}
	size_t extra = nul_terminate ? 1 : 0;
	if (len > SIZE_MAX - extra || s->data_len > SIZE_MAX - len - extra) {
		return -1;
	}
	size_t needed = s->data_len + len + extra;
	if (needed > s->data_cap) {
		size_t new_cap = s->data_cap ? s->data_cap : 1024;
		while (new_cap < needed) {
			if (new_cap > SIZE_MAX / 2) {
				new_cap = needed;
				break;
			}
			new_cap *= 2;
		}
		char *new_data = (char *)realloc(s->data, new_cap);
		if (!new_data) {
			return -1;
		}
		s->data = new_data;
		s->data_cap = new_cap;
	}
	size_t offset = s->data_len;
	if (len > 0) {
		memcpy(s->data + offset, ptr, len);
	}
	if (nul_terminate) {
		s->data[offset + len] = '\0';
	}
	s->data_len = needed;
	s->slots[idx].hash = hash;
	s->slots[idx].offset = offset + 1;
	s->slots[idx].len = len;
	s->count++;
	return 1;
}

static int task_scratch_reserve(task_scratch *b, size_t needed) {
	if (needed <= b->cap) {
		return 0;
	}
	size_t new_cap = b->cap ? b->cap : 256;
	while (new_cap < needed) {
		if (new_cap > SIZE_MAX / 2) {
			new_cap = needed;
			break;
		}
		new_cap *= 2;
	}
	char *next = (char *)realloc(b->data, new_cap);
	if (!next) {
		return -1;
	}
	b->data = next;
	b->cap = new_cap;
	return 0;
}

static int task_history_visit_subtree(task_history_ctx *ctx, const git_oid *oid,
                                      size_t prefix_len, int depth) {
	if (depth <= 0) {
		return 0;
	}
	if (prefix_len > SIZE_MAX - GIT_OID_SHA1_SIZE) {
		return 1;
	}
	size_t key_len = GIT_OID_SHA1_SIZE + prefix_len;
	if (task_scratch_reserve(&ctx->key_buf, key_len) < 0) {
		return 1;
	}
	memcpy(ctx->key_buf.data, oid->id, GIT_OID_SHA1_SIZE);
	memcpy(ctx->key_buf.data + GIT_OID_SHA1_SIZE, ctx->path_buf.data, prefix_len);
	int added = task_byteset_add(&ctx->seen_subtrees, ctx->key_buf.data, key_len, 0);
	if (added < 0) {
		return 1;
	}
	if (added == 0) {
		return 0;
	}
	git_tree *tree = NULL;
	int rc = git_tree_lookup(&tree, ctx->repo, oid);
	if (rc < 0) {
		*ctx->failed_op = "read historical subtree";
		return rc;
	}
	size_t count = git_tree_entrycount(tree);
	for (size_t i = 0; i < count; i++) {
		const git_tree_entry *child = git_tree_entry_byindex(tree, i);
		const char *name = git_tree_entry_name(child);
		size_t name_len = strlen(name);
		if (name_len > SIZE_MAX - prefix_len - 1) {
			git_tree_free(tree);
			return 1;
		}
		size_t child_len = prefix_len + 1 + name_len;
		if (task_scratch_reserve(&ctx->path_buf, child_len) < 0) {
			git_tree_free(tree);
			return 1;
		}
		ctx->path_buf.data[prefix_len] = '/';
		memcpy(ctx->path_buf.data + prefix_len + 1, name, name_len);
		if (task_byteset_add(&ctx->paths, ctx->path_buf.data, child_len, 1) < 0) {
			git_tree_free(tree);
			return 1;
		}
		if (depth > 1 && git_tree_entry_filemode_raw(child) == GIT_FILEMODE_TREE) {
			rc = task_history_visit_subtree(ctx, git_tree_entry_id(child), child_len, depth - 1);
			if (rc != 0) {
				git_tree_free(tree);
				return rc;
			}
		}
	}
	git_tree_free(tree);
	return 0;
}

static int task_history_paths(git_repository *repo, const git_oid *tips, size_t tip_count,
                              const char *directory, size_t dir_len, int depth,
                              char **out_data, size_t *out_len, size_t *out_count,
                              const char **failed_op) {
	*out_data = NULL;
	*out_len = 0;
	*out_count = 0;
	*failed_op = NULL;

	git_revwalk *walk = NULL;
	int rc = git_revwalk_new(&walk, repo);
	if (rc < 0) {
		*failed_op = "create history walker";
		return rc;
	}
	for (size_t i = 0; i < tip_count; i++) {
		rc = git_revwalk_push(walk, &tips[i]);
		if (rc < 0) {
			*failed_op = "add history tip";
			git_revwalk_free(walk);
			return rc;
		}
	}

	task_history_ctx ctx;
	memset(&ctx, 0, sizeof(ctx));
	ctx.repo = repo;
	ctx.failed_op = failed_op;

	git_oid oid;
	for (;;) {
		rc = git_revwalk_next(&oid, walk);
		if (rc == GIT_ITEROVER) {
			rc = 0;
			break;
		}
		if (rc < 0) {
			*failed_op = "walk history";
			break;
		}
		git_commit *commit = NULL;
		rc = git_commit_lookup(&commit, repo, &oid);
		if (rc < 0) {
			*failed_op = "lookup commit";
			break;
		}
		const git_oid *tree_id = git_commit_tree_id(commit);
		int unseen_root = task_byteset_add(&ctx.seen_roots, tree_id->id, GIT_OID_SHA1_SIZE, 0);
		if (unseen_root < 0) {
			git_commit_free(commit);
			rc = 1;
			break;
		}
		if (unseen_root == 0) {
			git_commit_free(commit);
			continue;
		}
		git_tree *tree = NULL;
		rc = git_commit_tree(&tree, commit);
		git_commit_free(commit);
		if (rc < 0) {
			*failed_op = "read commit tree";
			break;
		}
		size_t count = git_tree_entrycount(tree);
		for (size_t i = 0; i < count; i++) {
			const git_tree_entry *entry = git_tree_entry_byindex(tree, i);
			const char *name = git_tree_entry_name(entry);
			size_t name_len = strlen(name);
			if (task_byteset_add(&ctx.paths, name, name_len, 1) < 0) {
				rc = 1;
				break;
			}
			if (dir_len > 0 && depth > 0 && name_len == dir_len &&
				memcmp(name, directory, dir_len) == 0 &&
				git_tree_entry_filemode_raw(entry) == GIT_FILEMODE_TREE) {
				if (task_scratch_reserve(&ctx.path_buf, dir_len) < 0) {
					rc = 1;
					break;
				}
				memcpy(ctx.path_buf.data, directory, dir_len);
				rc = task_history_visit_subtree(&ctx, git_tree_entry_id(entry), dir_len, depth);
				if (rc != 0) {
					break;
				}
			}
		}
		git_tree_free(tree);
		if (rc != 0) {
			break;
		}
	}

	git_revwalk_free(walk);
	task_byteset_free(&ctx.seen_roots);
	task_byteset_free(&ctx.seen_subtrees);
	free(ctx.path_buf.data);
	free(ctx.key_buf.data);

	if (rc != 0) {
		task_byteset_free(&ctx.paths);
		return rc;
	}

	*out_data = ctx.paths.data;
	*out_len = ctx.paths.data_len;
	*out_count = ctx.paths.count;
	free(ctx.paths.slots);
	return 0;
}
*/
import "C"

import (
	"errors"
	"math"
	"sort"
	"strings"
	"unsafe"
)

// HistoryPaths observes every root entry in every reachable commit, plus
// entries up to depth levels within directory (if nonempty). Visiting trees
// includes deleted files and both sides of merges.
func (r *Repository) HistoryPaths(tips []ID, directory string, depth int) ([]string, error) {
	if depth < 0 {
		depth = 0
	} else if depth > math.MaxInt32 {
		depth = math.MaxInt32
	}
	var result []string
	err := r.callNative("read historical paths", false, func(repo *C.git_repository) error {
		var nativeTips []C.git_oid
		var tipsPtr *C.git_oid
		if len(tips) > 0 {
			nativeTips = make([]C.git_oid, len(tips))
			for i, tip := range tips {
				nativeTips[i] = nativeID(tip)
			}
			tipsPtr = &nativeTips[0]
		}
		var dirBytes []byte
		var dirPtr *C.char
		var dirLen C.size_t
		if directory != "" && !strings.ContainsRune(directory, 0) {
			dirBytes = []byte(directory)
			dirPtr = (*C.char)(unsafe.Pointer(&dirBytes[0]))
			dirLen = C.size_t(len(dirBytes))
		}
		var outData *C.char
		var outLen, outCount C.size_t
		var failedOp *C.char
		rc := C.task_history_paths(repo, tipsPtr, C.size_t(len(nativeTips)), dirPtr, dirLen, C.int(depth), &outData, &outLen, &outCount, &failedOp)
		if outData != nil {
			defer C.free(unsafe.Pointer(outData))
		}
		if rc < 0 {
			op := "read historical paths"
			if failedOp != nil {
				op = C.GoString(failedOp)
			}
			return nativeError(op, rc)
		}
		if rc > 0 {
			return errors.New("read historical paths: out of memory")
		}
		result = make([]string, 0, int(outCount))
		if outLen > 0 {
			blob := string(unsafe.Slice((*byte)(unsafe.Pointer(outData)), int(outLen)))
			for len(blob) > 0 {
				end := strings.IndexByte(blob, 0)
				if end < 0 {
					break
				}
				result = append(result, blob[:end])
				blob = blob[end+1:]
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(result)
	return result, nil
}
