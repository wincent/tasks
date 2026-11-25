package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
)

type destination string

func (d destination) ref() string { return "refs/heads/" + string(d) }

func validateTopicOption(a arguments) error {
	if topic, supplied := a.values["--topic"]; supplied {
		if !store.ValidTopic(topic[0]) {
			return fmt.Errorf("--topic requires a local implementation branch name; got %q", topic[0])
		}
		return destination(topic[0]).validate()
	}
	return nil
}

func taskDestination(content string, defaultTarget destination) (destination, error) {
	topic, err := store.Topic(content)
	if err != nil {
		return "", err
	}
	if topic == "" {
		return defaultTarget, nil
	}
	d := destination(topic)
	return d, d.validate()
}

func requireTaskDestination(task store.Task, defaultTarget, expected destination) error {
	actual, err := taskDestination(task.Content, defaultTarget)
	if err != nil {
		return fmt.Errorf("%s: %w", task.ID(), err)
	}
	if actual != expected {
		return fmt.Errorf("%s: topic changed from %q to %q; review and retry", task.ID(), expected, actual)
	}
	return nil
}

func (d destination) validate() error {
	if strings.HasPrefix(strings.ToLower(string(d)), "task/") {
		return fmt.Errorf("topic %q is in the reserved task/ branch namespace", d)
	}
	return nil
}

func (d destination) tip(repo *git.Repository, defaultTarget destination) (git.ID, error) {
	ref, err := repo.Reference(d.ref())
	if err != nil {
		if d != defaultTarget && git.HasCode(err, git.NotFound) {
			return git.ID{}, fmt.Errorf("recorded topic %q is missing; restore it before continuing: %w", d, err)
		}
		return git.ID{}, fmt.Errorf("read local %s: %w", d, err)
	}
	if ref.Symbolic != "" {
		return git.ID{}, fmt.Errorf("local %s is a symbolic ref", d)
	}
	// A case-insensitive filesystem can resolve a differently spelled
	// loose ref. Do not update it as if it were a separate branch.
	refs, err := repo.References(d.ref())
	if err != nil {
		return git.ID{}, err
	}
	if refs[d.ref()] != ref {
		return git.ID{}, fmt.Errorf("local branch %q changed or does not match the exact branch name", d)
	}
	return ref.ID, nil
}

func launchDestination(ctx context.Context, repo *git.Repository, directory string, target, defaultTarget destination, fresh bool) (git.ID, error) {
	base, err := target.tip(repo, defaultTarget)
	if !fresh || target == defaultTarget || !git.HasCode(err, git.NotFound) {
		return base, err
	}

	// Serialize initial creation with other tasks launching into this topic.
	lock, err := state.Acquire(ctx, filepath.Join(directory, "merge.lock"))
	if err != nil {
		return git.ID{}, err
	}
	defer lock.Close()
	base, err = target.tip(repo, defaultTarget)
	if !git.HasCode(err, git.NotFound) {
		return base, err
	}
	base, err = defaultTarget.tip(repo, defaultTarget)
	if err != nil {
		return git.ID{}, err
	}
	actor, err := identity(repo)
	if err != nil {
		return git.ID{}, err
	}
	tx, err := repo.Prepare(target.ref(), base, map[string]git.ID{defaultTarget.ref(): base, target.ref(): {}}, actor, "create task topic")
	if err != nil {
		return git.ID{}, fmt.Errorf("prepare local topic %q: %w", target, err)
	}
	defer tx.Close()
	if err := ctx.Err(); err != nil {
		return git.ID{}, err
	}
	if err := tx.Commit(); err != nil {
		return git.ID{}, fmt.Errorf("create local topic %q: %w", target, err)
	}
	return base, nil
}
