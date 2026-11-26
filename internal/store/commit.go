package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/state"
)

// CommitOptions supplies identity and cross-workflow ref expectations. The
// callback runs under the prepared ref locks before any task files change;
// completion uses it to recheck source checkout identity and cleanliness.
type CommitOptions struct {
	Signature git.Signature
	// FromEnvironment resolves independent actors in the actual tasks checkout.
	// Otherwise Signature supplies both actors for deterministic fixture callers.
	FromEnvironment bool
	Message         string
	ExpectedRefs    map[string]git.ID
	BeforePublish   func() error

	artifactDirectory string // Nonempty only for append-only artifact batches.
	moves             []archiveMove
}

// RecoveryError never promises rollback once reference publication has been
// attempted. Recovery artifacts retain originals, proposed contents and index.
type RecoveryError struct {
	Revision  git.ID
	Directory string
	Err       error
}

func (e *RecoveryError) Error() string {
	return fmt.Sprintf("task publication/index state is uncertain at %s; inspect refs and recovery artifacts in %s before retrying: %v", e.Revision, e.Directory, e.Err)
}
func (e *RecoveryError) Unwrap() error { return e.Err }

func regularContents(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("task must be a regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Preserve the distinction between an absent and an empty file.
	if data == nil {
		data = []byte{}
	}
	return data, nil
}

func equalContents(a, b []byte) bool { return (a == nil) == (b == nil) && bytes.Equal(a, b) }

// RequireClean validates selected metadata before an unlocked editor or workflow.
// Publication repeats these checks; this is not a transferable cleanliness receipt.
func (store *Store) RequireClean(names ...string) error {
	if store.prospective {
		return store.Repository.WithProspectiveWorktree("tasks", Branch, func(view *git.Repository) error {
			return view.ValidateIndexPaths(names)
		})
	}
	return store.cleanSelected(names)
}

func (store *Store) cleanSelected(names []string) error {
	return store.cleanSelectedWithIndex(names, nil, false)
}

func (store *Store) cleanSelectedWithIndex(names []string, entries []git.IndexEntry, haveIndex bool) error {
	// Check the complete selection before status inspection or any index,
	// object, content or reference preparation. Do not reject unrelated paths.
	if err := store.Repository.ValidateWorktreePaths(names); err != nil {
		return err
	}
	if err := store.noOperation(); err != nil {
		return err
	}
	selected := make(map[string]bool)
	for _, name := range names {
		selected[name] = true
	}
	changes, err := store.Repository.Status(true)
	if err != nil {
		return err
	}
	for _, change := range changes {
		if selected[change.Path] {
			return fmt.Errorf("manual changes to %s; commit or move those changes before retrying", filepath.Join(store.Path, change.Path))
		}
	}
	if !haveIndex {
		entries, err = store.Repository.IndexEntries()
		if err != nil {
			return err
		}
	}
	for _, entry := range entries {
		if selected[entry.Path] && (entry.AssumeValid || entry.SkipWorktree || entry.IntentToAdd || entry.Stage != 0) {
			return fmt.Errorf("task index flags or conflict hide local state: %s", entry.Path)
		}
	}
	return nil
}

// CommitFiles commits only selected root-level task files. It acquires the
// shared metadata flock, reserves the real index, prepares distinct commit and
// user indexes, and holds verification ref locks until task ref publication.
// The index reservation remains held during installation. It never runs hooks or subprocesses. The caller must acquire
// any task-lifetime/integration locks before this metadata lock.
func CommitFiles(ctx context.Context, repository *git.Repository, changes map[string][]byte, options CommitOptions) (git.ID, error) {
	if options.FromEnvironment {
		if _, err := MetadataSignatures(repository); err != nil {
			return git.ID{}, err
		}
	}
	session, err := Begin(ctx, repository)
	if err != nil {
		return git.ID{}, err
	}
	defer session.Close()
	return session.CommitFiles(changes, options)
}

func commitFiles(ctx context.Context, repository *git.Repository, changes map[string][]byte, options CommitOptions) (revision git.ID, err error) {
	if len(changes) == 0 {
		return revision, errors.New("task metadata commit requires at least one file")
	}
	names := make([]string, 0, len(changes))
	// Freeze caller-owned data before lock acquisition.
	contents := make(map[string][]byte)
	sources := make(map[string]bool, len(options.moves))
	for _, move := range options.moves {
		sources[move.source] = true
	}
	for name, content := range changes {
		if len(options.moves) == 0 && options.artifactDirectory == "" && (name == "" || name == "." || name == ".." || strings.EqualFold(name, ".git") || strings.ContainsAny(name, "/\x00")) {
			return revision, fmt.Errorf("task commit requires a literal root filename: %q", name)
		}
		if content == nil {
			if !sources[name] {
				return revision, errors.New("task metadata deletion is not supported")
			}
		} else {
			contents[name] = append([]byte{}, content...)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	expected := make(map[string]git.ID)
	for ref, value := range options.ExpectedRefs {
		expected[ref] = value
	}
	store, err := Open(repository)
	if err != nil {
		return revision, err
	}
	defer store.Close()
	storeDirInfo, err := os.Lstat(store.Path)
	if err != nil {
		return revision, err
	}
	storeGitDirInfo, err := os.Lstat(store.Paths.GitDir)
	if err != nil {
		return revision, err
	}
	actors := git.Signatures{Author: options.Signature, Committer: options.Signature}
	if options.FromEnvironment {
		actors, err = store.Repository.Signatures()
		if err != nil {
			return revision, err
		}
	}
	head, err := store.Repository.Head()
	if err != nil {
		return revision, err
	}
	checkSelected := func() error {
		var indexEntries []git.IndexEntry
		var indexedTickets map[ticketKey][]string
		for _, move := range options.moves {
			if err := move.check(store, head.ID, &indexEntries, &indexedTickets); err != nil {
				return err
			}
		}
		if options.artifactDirectory != "" {
			if err := store.requireNewPaths(options.artifactDirectory, "artifact", names, head.ID); err != nil {
				return err
			}
		}
		return store.cleanSelectedWithIndex(names, indexEntries, indexedTickets != nil)
	}
	if value, exists := expected[Branch]; exists && value != head.ID {
		return revision, errors.New("tasks ref differs from caller expectation")
	}
	expected[Branch] = head.ID
	indexPath := filepath.Join(store.Paths.GitDir, "index")
	indexLockPath := indexPath + ".lock"
	indexLock, err := os.OpenFile(indexLockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return revision, fmt.Errorf("reserve task index (inspect existing locks, do not remove a live lock): %w", err)
	}
	ownedIndexLock, err := indexLock.Stat()
	if err != nil {
		indexLock.Close()
		return revision, err
	}
	defer func() {
		indexLock.Close()
		if current, statErr := os.Lstat(indexLockPath); statErr == nil && os.SameFile(ownedIndexLock, current) {
			os.Remove(indexLockPath)
		}
	}()
	if err := checkSelected(); err != nil {
		return revision, err
	}
	if err := store.Repository.ValidateWorktreeContents(contents); err != nil {
		return revision, err
	}
	originals := make(map[string][]byte)
	for _, name := range names {
		data, err := regularContents(filepath.Join(store.Path, name))
		if err != nil {
			return revision, err
		}
		originals[name] = data
	}
	temporary, err := os.MkdirTemp(store.Paths.GitDir, "task-commit-")
	if err != nil {
		return revision, err
	}
	retain := false
	defer func() {
		if !retain {
			os.RemoveAll(temporary)
		}
	}()
	index, err := regularContents(indexPath)
	if err != nil {
		return revision, err
	}
	userIndex := filepath.Join(temporary, "user-index")
	base := head.ID
	if index != nil {
		if err := os.WriteFile(userIndex, index, 0600); err != nil {
			return revision, err
		}
		base = git.ID{}
	}
	var edits []git.TreeEdit
	if len(options.moves) != 0 {
		for _, move := range options.moves {
			data := originals[move.source]
			if data == nil || !equalContents(data, contents[move.destination]) {
				return revision, fmt.Errorf("task changed during archive planning: %s; retry", move.source)
			}
			moveEdits, err := move.treeEdits(store.Repository, head.ID, data)
			if err != nil {
				return revision, err
			}
			edits = append(edits, moveEdits...)
		}
	} else {
		for _, name := range names {
			blob, err := store.Repository.WriteWorktreeBlob(name, contents[name])
			if err != nil {
				return revision, err
			}
			edits = append(edits, git.TreeEdit{Path: name, ID: blob, Mode: 0100644})
		}
	}
	tree, err := store.Repository.EditTree(head.ID, edits)
	if err != nil {
		return revision, err
	}
	revision, err = store.Repository.WriteCommit(tree, []git.ID{head.ID}, actors.Author, actors.Committer, options.Message+"\n")
	if err != nil {
		return revision, err
	}
	if err := store.Repository.EditIndexFile(userIndex, base, edits); err != nil {
		return revision, err
	}
	recovery, err := json.MarshalIndent(struct {
		Version   int               `json:"version"`
		Store     string            `json:"store"`
		Base      string            `json:"base"`
		Revision  string            `json:"revision"`
		Originals map[string][]byte `json:"originals"`
		Changes   map[string][]byte `json:"changes"`
	}{1, store.Path, head.ID.String(), revision.String(), originals, contents}, "", "  ")
	if err != nil {
		return revision, err
	}
	if err := state.AtomicWrite(filepath.Join(temporary, "recovery.json"), append(recovery, '\n')); err != nil {
		return revision, err
	}
	tx, err := store.Repository.Prepare(Branch, revision, expected, actors.Committer, options.Message)
	if err != nil {
		return revision, err
	}
	defer tx.Close()
	if options.BeforePublish != nil {
		if err := options.BeforePublish(); err != nil {
			return revision, err
		}
	}
	if err := ctx.Err(); err != nil {
		return revision, err
	}
	if currentDir, statErr := os.Lstat(store.Path); statErr != nil || !os.SameFile(storeDirInfo, currentDir) {
		return revision, errors.New("task checkout identity changed during metadata commit")
	}
	if currentGitDir, statErr := os.Lstat(store.Paths.GitDir); statErr != nil || !os.SameFile(storeGitDirInfo, currentGitDir) {
		return revision, errors.New("task checkout identity changed during metadata commit")
	}
	if err := store.noOperation(); err != nil {
		return revision, err
	}
	if options.BeforePublish != nil {
		fresh, err := Open(repository)
		if err != nil {
			return revision, err
		}
		fresh.Close()
		if fresh.Paths != store.Paths {
			return revision, errors.New("task checkout identity changed during metadata commit")
		}
		if err := checkSelected(); err != nil {
			return revision, err
		}
	}
	if current, statErr := os.Lstat(indexLockPath); statErr != nil || !os.SameFile(ownedIndexLock, current) {
		return revision, errors.New("task index lock identity changed during metadata commit")
	}
	currentHead, err := store.Repository.Head()
	if err != nil {
		return revision, err
	}
	if currentHead != head {
		return revision, errors.New("task checkout HEAD changed during metadata commit")
	}
	currentIndex, err := regularContents(indexPath)
	if err != nil {
		return revision, err
	}
	if !equalContents(currentIndex, index) {
		return revision, errors.New("task index changed despite reserved index lock")
	}
	for _, name := range names {
		current, err := regularContents(filepath.Join(store.Path, name))
		if err != nil {
			return revision, err
		}
		if !equalContents(current, originals[name]) {
			return revision, fmt.Errorf("task changed during metadata commit: %s; retry", name)
		}
	}
	attempted := false
	var created []string
	defer func() {
		if err != nil && !attempted {
			for i := len(created) - 1; i >= 0; i-- {
				os.Remove(created[i]) // Never remove unrelated directory contents.
			}
		}
	}()
	for _, move := range options.moves {
		if err := metadataParents(store.Path, move.directory(), "archive", &created); err != nil {
			return revision, err
		}
	}
	if options.artifactDirectory != "" {
		if err := metadataParents(store.Path, options.artifactDirectory, "artifact", &created); err != nil {
			return revision, err
		}
	}
	var archiveLinks []archiveLink
	var written []string
	defer func() {
		if err == nil {
			return
		}
		if attempted {
			retain = true
			err = &RecoveryError{Revision: revision, Directory: temporary, Err: err}
			return
		}
		for i := len(archiveLinks) - 1; i >= 0; i-- {
			if restoreErr := archiveLinks[i].rollback(); restoreErr != nil {
				retain = true
				err = errors.Join(err, fmt.Errorf("restore archive move (recovery in %s): %w", temporary, restoreErr))
			}
		}
		for _, name := range written {
			path := filepath.Join(store.Path, name)
			current, readErr := regularContents(path)
			if readErr != nil || !equalContents(current, contents[name]) {
				continue
			}
			var restoreErr error
			if originals[name] == nil {
				restoreErr = os.Remove(path)
			} else {
				restoreErr = state.AtomicWrite(path, originals[name])
			}
			if restoreErr != nil {
				retain = true
				err = errors.Join(err, fmt.Errorf("restore %s (recovery in %s): %w", path, temporary, restoreErr))
			}
		}
	}()
	if len(options.moves) != 0 {
		archiveLinks, err = installArchiveMoves(store.Path, options.moves, created, state.SyncDirectory)
		if err != nil {
			return revision, err
		}
	} else {
		written = make([]string, 0, len(names))
		var batch state.Batch
		for _, name := range names {
			path := filepath.Join(store.Path, name)
			if options.artifactDirectory != "" {
				installed, err := batch.AtomicCreate(path, contents[name])
				if installed {
					written = append(written, name)
				}
				if err != nil {
					return revision, err
				}
			} else {
				written = append(written, name)
				if err := batch.AtomicWrite(path, contents[name]); err != nil {
					return revision, err
				}
			}
		}
		if err := batch.SyncDirectories(); err != nil {
			return revision, err
		}
	}
	if err := ctx.Err(); err != nil {
		return revision, err
	}
	attempted = true
	if err := tx.Commit(); err != nil {
		return revision, err
	}
	if current, statErr := os.Lstat(indexLockPath); statErr != nil || !os.SameFile(ownedIndexLock, current) {
		return revision, errors.New("task index lock lost after reference publication")
	}
	newIndex, err := os.ReadFile(userIndex)
	if err != nil {
		return revision, err
	}
	if _, err := indexLock.Write(newIndex); err != nil {
		return revision, err
	}
	if err := state.SyncFile(indexLock); err != nil {
		return revision, err
	}
	if err := indexLock.Close(); err != nil {
		return revision, err
	}
	if err := os.Rename(indexLockPath, indexPath); err != nil {
		return revision, err
	}
	if err := state.SyncDirectory(filepath.Dir(indexPath)); err != nil {
		return revision, err
	}
	return revision, nil
}
