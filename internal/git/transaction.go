package git

/*
#include <git2.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// ChangedError means a locked reference no longer has the expected direct
// target. Absent references are represented by the zero ID. Symbolic references
// are rejected even when resolving them would yield the expected object.
type ChangedError struct {
	Name     string
	Expected ID
	Actual   Reference
}

func (e *ChangedError) Error() string {
	if e.Actual.Symbolic != "" {
		return fmt.Sprintf("reference %s became symbolic (%s); refusing publication", e.Name, e.Actual.Symbolic)
	}
	return fmt.Sprintf("reference %s changed: expected %s, found %s; retry", e.Name, e.Expected, e.Actual.ID)
}

// PublicationError deliberately does not claim rollback. A filesystem/reflog
// error may occur after observable effects; the caller must inspect fresh refs
// and its durable workflow receipt before deciding whether publication happened.
type PublicationError struct{ Err error }

func (e *PublicationError) Error() string {
	return "reference publication failed; inspect publication state before retrying: " + e.Err.Error()
}
func (e *PublicationError) Unwrap() error { return e.Err }

// Transaction changes at most one reference. Every expected ref has its own
// native lock transaction: committing the target cannot release verification
// locks early, regardless of libgit2's internal transaction iteration order.
// There is no claim of atomic multi-ref updates. Call Close on every exit path.
// A transaction owns a fresh repository handle, independent of its creator.
type Transaction struct {
	mu     sync.Mutex
	repo   *C.git_repository
	locks  []*C.git_transaction
	target *C.git_transaction
}

// Prepare locks all expected refs in sorted order and compares their direct
// targets after acquiring all locks. The changed ref must be among expected.
// Zero next deletes a ref; zero expected means the ref must not exist.
// No filesystem/index/checkout side effects should precede successful Prepare.
func (r *Repository) Prepare(ref string, next ID, expected map[string]ID, who Signature, message string) (*Transaction, error) {
	if _, ok := expected[ref]; !ok {
		return nil, errors.New("publication target requires an explicit expected value")
	}
	names := make([]string, 0, len(expected))
	for name := range expected {
		if name != "HEAD" && !strings.HasPrefix(name, "refs/heads/") {
			return nil, fmt.Errorf("publication requires a fully qualified branch reference: %q", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	tx := &Transaction{}
	err := r.call("prepare publication", func(repo *C.git_repository) error {
		// Reopen the exact private Git directory without ancestor discovery. This
		// avoids cached refs and keeps Close on the originating handle harmless.
		path := C.git_repository_path(repo)
		if err := nativeError("reopen publication repository", C.git_repository_open_ext(&tx.repo, path, C.GIT_REPOSITORY_OPEN_NO_SEARCH|C.GIT_REPOSITORY_OPEN_NO_DOTGIT, nil)); err != nil {
			return err
		}
		for _, name := range names {
			n, err := cstring(name)
			if err != nil {
				return err
			}
			var valid C.int
			code := C.git_reference_name_is_valid(&valid, n)
			if code < 0 || valid == 0 {
				free(n)
				if err := nativeError("validate reference", code); err != nil {
					return err
				}
				return fmt.Errorf("invalid reference name %q", name)
			}
			var lock *C.git_transaction
			if err := nativeError("allocate reference lock", C.git_transaction_new(&lock, tx.repo)); err != nil {
				free(n)
				return err
			}
			tx.locks = append(tx.locks, lock)
			code = C.git_transaction_lock_ref(lock, n)
			free(n)
			if err := nativeError("lock "+name, code); err != nil {
				return err
			}
			if name == ref {
				tx.target = lock
			}
		}
		for _, name := range names {
			actual, err := lookup(tx.repo, name)
			if err != nil && !HasCode(err, NotFound) {
				return err
			}
			if actual.Symbolic != "" || actual.ID != expected[name] {
				return &ChangedError{Name: name, Expected: expected[name], Actual: actual}
			}
		}
		name, err := cstring(ref)
		if err != nil {
			return err
		}
		defer free(name)
		if next == (ID{}) {
			if ref == "HEAD" {
				return errors.New("cannot delete HEAD")
			}
			if expected[ref] == (ID{}) {
				return errors.New("cannot delete an absent reference")
			}
			return nativeError("prepare reference deletion", C.git_transaction_remove(tx.target, name))
		}
		// Branch refs must point to an existing commit, not a blob or missing ID.
		id := nativeID(next)
		var commit *C.git_commit
		if err := nativeError("lookup publication commit", C.git_commit_lookup(&commit, tx.repo, &id)); err != nil {
			return err
		}
		C.git_commit_free(commit)
		sig, err := signature(who)
		if err != nil {
			return err
		}
		defer C.git_signature_free(sig)
		text, err := cstring(message)
		if err != nil {
			return err
		}
		defer free(text)
		return nativeError("prepare reference update", C.git_transaction_set_target(tx.target, name, &id, sig, text))
	})
	if err != nil {
		tx.Close()
		return nil, err
	}
	return tx, nil
}

// Commit publishes the one queued update and then releases verification locks.
// It consumes the transaction on both success and failure. No checkout or index
// is changed by this layer; the workflow must handle those before publication.
func (tx *Transaction) Commit() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.repo == nil {
		return ErrClosed
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer tx.close()
	if err := nativeError("publish reference", C.git_transaction_commit(tx.target)); err != nil {
		return &PublicationError{Err: err}
	}
	return nil
}

func (tx *Transaction) close() {
	for i := len(tx.locks) - 1; i >= 0; i-- {
		C.git_transaction_free(tx.locks[i])
	}
	tx.locks = nil
	tx.target = nil
	if tx.repo != nil {
		C.git_repository_free(tx.repo)
		tx.repo = nil
	}
}

// Close aborts an uncommitted transaction without changing any ref. Repeated
// Close calls are safe; Commit after Close reports ErrClosed.
func (tx *Transaction) Close() {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.close()
}
