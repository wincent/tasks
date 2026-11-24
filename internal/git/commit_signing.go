package git

/*
#include <git2.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"unsafe"
)

// CommitBuffer returns the exact unsigned bytes for an object without writing
// an object or ref. No native pointers escape the serialized repository call.
func (r *Repository) CommitBuffer(treeID ID, parents []ID, author, committer Signature, message string) ([]byte, error) {
	var result []byte
	err := r.callNative("create commit buffer", false, func(repo *C.git_repository) error {
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
		var payload C.git_buf
		defer C.git_buf_dispose(&payload)
		if err := nativeError("write commit", C.git_commit_create_buffer(&payload, repo, a, c, nil, text, tree, C.size_t(len(parents)), parentList)); err != nil {
			return err
		}
		result = C.GoBytes(unsafe.Pointer(payload.ptr), C.int(payload.size))
		return nil
	})
	return result, err
}

// WriteSignedCommit stores precisely the supplied payload with a gpgsig header.
// It does not resolve actors again, sign data, update refs, or fall back unsigned.
func (r *Repository) WriteSignedCommit(payload, signature []byte) (ID, error) {
	var result ID
	if len(signature) == 0 {
		return result, errors.New("empty commit signature")
	}
	err := r.callNative("write signed commit", false, func(repo *C.git_repository) error {
		text, err := cstring(string(payload))
		if err != nil {
			return err
		}
		defer free(text)
		sig, err := cstring(string(signature))
		if err != nil {
			return err
		}
		defer free(sig)
		var id C.git_oid
		if err := nativeError("write signed commit", C.git_commit_create_with_signature(&id, repo, text, sig, nil)); err != nil {
			return err
		}
		result = objectID(&id)
		return nil
	})
	return result, err
}

// ExtractCommitSignature returns copied signature and signed payload bytes.
// An unsigned commit returns the native NotFound error.
func (r *Repository) ExtractCommitSignature(id ID) (signature, payload []byte, err error) {
	err = r.callNative("extract commit signature", false, func(repo *C.git_repository) error {
		var sig, data C.git_buf
		defer C.git_buf_dispose(&sig)
		defer C.git_buf_dispose(&data)
		oid := nativeID(id)
		if err := nativeError("extract commit signature", C.git_commit_extract_signature(&sig, &data, repo, &oid, nil)); err != nil {
			return err
		}
		signature = C.GoBytes(unsafe.Pointer(sig.ptr), C.int(sig.size))
		payload = C.GoBytes(unsafe.Pointer(data.ptr), C.int(data.size))
		return nil
	})
	return
}
