package git

/*
#include <git2.h>
*/
import "C"

import (
	"fmt"
	"time"
)

// Signatures keeps the independently resolved commit actors together. Explicit
// Signature arguments to WriteCommit and Prepare remain deterministic APIs.
type Signatures struct {
	Author, Committer Signature
}

func resolvedSignature(value *C.git_signature) Signature {
	return Signature{
		Name: C.GoString(value.name), Email: C.GoString(value.email),
		When: time.Unix(int64(value.when.time), 0).In(time.FixedZone("", int(value.when.offset)*60)),
	}
}

func signatureError(code C.int) error {
	err := nativeError("resolve commit identity from configuration/environment", code)
	if HasCode(err, NotFound) {
		return fmt.Errorf("configure user.name and user.email before task metadata commits (or supply GIT_AUTHOR_NAME/EMAIL and GIT_COMMITTER_NAME/EMAIL): %w", err)
	}
	return err
}

func repositorySignatures(repo *C.git_repository) (Signatures, error) {
	var author, committer *C.git_signature
	code := C.git_signature_default_from_env(&author, &committer, repo)
	defer C.git_signature_free(author)
	defer C.git_signature_free(committer)
	if err := signatureError(code); err != nil {
		return Signatures{}, err
	}
	return Signatures{resolvedSignature(author), resolvedSignature(committer)}, nil
}

// Signatures resolves both actors in one native call, including a common
// timestamp when neither date is supplied. It never changes process environment.
func (r *Repository) Signatures() (Signatures, error) {
	var result Signatures
	err := r.call("resolve commit signatures", func(repo *C.git_repository) error {
		var err error
		result, err = repositorySignatures(repo)
		return err
	})
	return result, err
}

// CommitterSignature resolves only the reflog actor; an author is not required
// for a reference transaction that does not create a commit.
func (r *Repository) CommitterSignature() (Signature, error) {
	var result Signature
	err := r.call("resolve reflog signature", func(repo *C.git_repository) error {
		var committer *C.git_signature
		code := C.git_signature_default_from_env(nil, &committer, repo)
		defer C.git_signature_free(committer)
		if err := signatureError(code); err != nil {
			return err
		}
		result = resolvedSignature(committer)
		return nil
	})
	return result, err
}

// ProspectiveWorktreeSignatures admits identity before a checkout, exclude rule,
// index, object, or reservation is written. Configuration is evaluated in the
// future worktree context, not the implementation branch that requested it.
func (r *Repository) ProspectiveWorktreeSignatures(name, branch string) (Signatures, error) {
	var result Signatures
	err := r.WithProspectiveWorktree(name, branch, func(planning *Repository) error {
		var err error
		result, err = planning.Signatures()
		return err
	})
	return result, err
}
