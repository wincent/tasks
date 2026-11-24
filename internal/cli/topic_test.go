package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/testutil"
)

func TestTopicCreationIsDeferredUntilRun(t *testing.T) {
	repo, root := taskWorkflowFixture(t)
	run(t, root, "init")
	worktrees, err := repo.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	branches := systemGit(t, root, "branch", "--list")
	// A same-named remote-tracking ref must not provide the base.
	systemGit(t, root, "update-ref", "refs/remotes/origin/feature/export", "main")
	run(t, root, "create", "Export", "--topic", "feature/export", "--body", "Export CSV.")
	if got := run(t, root, "show", "1", "--raw"); !strings.Contains(got, "\nStatus: open\nTopic: feature/export\n\n") {
		t.Fatal(got)
	}
	if after, err := repo.Worktrees(); err != nil || !slices.Equal(after, worktrees) || systemGit(t, root, "branch", "--list") != branches {
		t.Fatal("filing created an implementation branch or checkout")
	}
	main := commitImplementation(t, root, "new-base.txt", "new local main\n")
	systemGit(t, root, "switch", "-c", "other", "main^")
	other := commitImplementation(t, root, "other.txt", "caller branch\n")
	var implementation string
	out, err := invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		worktree := piWorktree(t, child)
		if systemGit(t, worktree, "rev-parse", "HEAD") != main || systemGit(t, root, "rev-parse", "refs/heads/feature/export") != main {
			t.Fatal("topic and worktree must start at the latest local main")
		}
		implementation = commitImplementation(t, worktree, "export.txt", "done\n")
		return process.Result{}
	}, "run", "1")
	if err != nil || !strings.Contains(out, "Topic: feature/export\n") || !strings.Contains(out, "merged into local feature/export.\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	assertStatus(t, root, "merged")
	if systemGit(t, root, "rev-parse", "feature/export") != implementation || systemGit(t, root, "rev-parse", "main") != main || systemGit(t, root, "rev-parse", "HEAD") != other {
		t.Fatal("run must update only the recorded topic, not main or the caller")
	}
}

func TestGoUsesExistingTopicAndUpdatesItsCheckout(t *testing.T) {
	_, root := taskWorkflowFixture(t)
	main := systemGit(t, root, "rev-parse", "main")
	topicDir := filepath.Join(t.TempDir(), "topic")
	systemGit(t, root, "worktree", "add", "-b", "delivery", topicDir, "main")
	topic := commitImplementation(t, topicDir, ".tasks.yml", "prefix: DIFFERENT\n")
	var topicAdvance, implementation string
	out, err := invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		worktree := piWorktree(t, child)
		if got := systemGit(t, worktree, "rev-parse", "HEAD"); got != topic {
			t.Fatalf("started at %s, want existing topic %s", got, topic)
		}
		if got := run(t, topicDir, "show", "EXAMPLE-1", "--raw"); !strings.Contains(got, "\nStatus: active\nTopic: delivery\n") {
			t.Fatal(got)
		}
		implementation = commitImplementation(t, worktree, "delivery.txt", "done\n")
		topicAdvance = commitImplementation(t, topicDir, "meanwhile.txt", "topic moved\n")
		return process.Result{}
	}, "go", "Deliver", "--topic=delivery")
	if err != nil || !strings.Contains(out, "merged into local delivery.\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := systemGit(t, topicDir, "show", "-s", "--format=%P", "HEAD"); got != topicAdvance+" "+implementation {
		t.Fatalf("wrong topic merge parents: %s", got)
	}
	if got := string(uiBytes(t, filepath.Join(topicDir, "delivery.txt"))); got != "done\n" {
		t.Fatal(got)
	}
	if systemGit(t, root, "rev-parse", "main") != main || systemGit(t, root, "status", "--porcelain") != "" {
		t.Fatal("topic merge changed the main checkout")
	}
}

func TestTopicReviewResumeAndExplicitMerge(t *testing.T) {
	_, root := taskWorkflowFixture(t)
	main := systemGit(t, root, "rev-parse", "main")
	topicDir := filepath.Join(t.TempDir(), "topic")
	systemGit(t, root, "worktree", "add", "-b", "review", topicDir, "main")
	topic := commitImplementation(t, topicDir, "review.txt", "topic base\n")
	run(t, root, "create", "Review me", "--topic", "review", "--body", "Review before merging.")
	var worktree, branch, implementation string
	out, err := invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		worktree = piWorktree(t, child)
		branch = systemGit(t, worktree, "branch", "--show-current")
		implementation = commitImplementation(t, worktree, "feature.txt", "done\n")
		return process.Result{}
	}, "run", "1", "--no-merge")
	if err != nil || !strings.Contains(out, "ready for review") || systemGit(t, topicDir, "rev-parse", "HEAD") != topic {
		t.Fatalf("review run must not merge: %v\n%s", err, out)
	}
	for _, retained := range []bool{false, true} {
		if retained {
			systemGit(t, root, "worktree", "remove", worktree)
		}
		out, err := invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
			current := piWorktree(t, child)
			if (!retained && current != worktree) || systemGit(t, current, "branch", "--show-current") != branch || systemGit(t, current, "rev-parse", "HEAD") != implementation {
				t.Fatal("did not resume existing task branch or checkout")
			}
			worktree = current
			return process.Result{}
		}, "run", "1", "--no-merge")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	unsaved := filepath.Join(topicDir, "unsaved.txt")
	writeSafetyFile(t, unsaved, "keep\n")
	fails(t, root, "worktree is dirty: review", "merge", "1")
	if systemGit(t, topicDir, "rev-parse", "HEAD") != topic || string(uiBytes(t, unsaved)) != "keep\n" {
		t.Fatal("failed merge modified the topic")
	}
	if err := os.Remove(unsaved); err != nil {
		t.Fatal(err)
	}
	if out := run(t, root, "merge", "1"); !strings.Contains(out, "merged into local review.\n") {
		t.Fatal(out)
	}
	if systemGit(t, topicDir, "rev-parse", "HEAD") != implementation || systemGit(t, root, "rev-parse", "main") != main {
		t.Fatal("explicit merge must update only the topic")
	}
}

func TestUncheckedOutTopicEnforcesSignaturePolicy(t *testing.T) {
	tests := []struct {
		name, policy, failure string
	}{
		{"signing", "[merge]\nff = false\n[commit]\ngpgSign = true\n[gpg]\nformat = openpgp\n[gpg \"openpgp\"]\nprogram = ./topic-signer\n", "topic signer refused"},
		{"verification", "[merge]\nverifySignatures = true\n", "verify incoming commit signature"},
		{"invalid policy", "[merge]\nverifySignatures = invalid\n", "merge.verifysignatures"},
		{"ignore caller policy", "[merge]\nverifySignatures = false\n", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, root := taskWorkflowFixture(t)
			const topic = "release/delivery"
			run(t, root, "create", "Deliver", "--topic", topic, "--body", "Topic only.")
			systemGit(t, root, "branch", topic, "main")
			base := systemGit(t, root, "rev-parse", topic)
			source := filepath.Join(t.TempDir(), "task")
			systemGit(t, root, "worktree", "add", "-b", "task/example-1", source, topic)
			implementation := commitImplementation(t, source, "delivery.txt", "done\n")

			common := filepath.Join(root, ".git")
			if test.name == "signing" {
				if err := os.WriteFile(filepath.Join(common, "topic-signer"), []byte("#!/bin/sh\necho 'topic signer refused' >&2\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			writeSafetyFile(t, filepath.Join(common, "topic-policy"), test.policy)
			if err := repo.SetLocalConfig("includeIf.onbranch:release/.path", "topic-policy"); err != nil {
				t.Fatal(err)
			}
			if test.failure == "" {
				systemGit(t, root, "config", "extensions.worktreeConfig", "true")
				systemGit(t, source, "config", "--worktree", "merge.verifySignatures", "true")
				run(t, source, "merge", "1")
				if got := systemGit(t, root, "rev-parse", topic); got != implementation {
					t.Fatalf("expected %s to fast-forward to %s, got %s", topic, implementation, got)
				}
				return
			}
			fails(t, source, test.failure, "merge", "1")
			if got := systemGit(t, root, "rev-parse", topic); got != base {
				t.Fatalf("merge moved %s from %s to %s despite its policy", topic, base, got)
			}
			assertStatus(t, root, "blocked")
		})
	}
}

func TestTopicCleanupUsesEachRecordedDestination(t *testing.T) {
	_, root := taskWorkflowFixture(t)
	main := systemGit(t, root, "rev-parse", "main")
	run(t, root, "create", "Topic work", "--topic", "delivery", "--body", "Topic only.")
	run(t, root, "create", "Default work", "--topic", "main", "--body", "Use main.")
	if got := run(t, root, "show", "2", "--raw"); strings.Contains(got, "\nTopic:") {
		t.Fatal(got)
	}
	systemGit(t, root, "branch", "delivery", "main")
	worktree := filepath.Join(filepath.Dir(root), "task")
	systemGit(t, root, "worktree", "add", "-b", "task/example-1", worktree, "delivery")
	implementation := commitImplementation(t, worktree, "topic-only.txt", "not in main\n")
	systemGit(t, root, "branch", "task/example-2", implementation)
	tree := systemGit(t, root, "rev-parse", implementation+"^{tree}")
	rewritten := systemGit(t, root, "commit-tree", tree, "-p", main, "-m", "integrate the task via a different commit")
	systemGit(t, root, "branch", "-f", "delivery", rewritten)
	for _, args := range [][]string{{"cleanup"}, {"cleanup", "--apply"}} {
		out := run(t, root, args...)
		action := "Would remove"
		if len(args) == 2 {
			action = "Removed"
		}
		for _, want := range []string{action + " " + worktree, action + " branch task/example-1", "Keep branch task/example-2: branch is not merged into local main"} {
			if !strings.Contains(out, want+"\n") {
				t.Fatalf("missing %q in:\n%s", want, out)
			}
		}
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("topic checkout not removed: %v", err)
	}
	if systemGit(t, root, "branch", "--list", "task/example-1") != "" || systemGit(t, root, "rev-parse", "main") != main || systemGit(t, root, "rev-parse", "delivery") != rewritten {
		t.Fatal("cleanup removed the wrong branch or changed a destination")
	}
}

func TestMissingTopicKeepsExistingWork(t *testing.T) {
	_, root := taskWorkflowFixture(t)
	run(t, root, "create", "Restore topic", "--topic", "missing", "--body", "Do not fall back to main.")
	worktree := filepath.Join(t.TempDir(), "task")
	// The task tip is in main. Falling back would allow both merge and cleanup.
	systemGit(t, root, "worktree", "add", "-b", "task/example-1", worktree, "main")
	for _, retained := range []bool{false, true} {
		if retained {
			systemGit(t, root, "worktree", "remove", worktree)
		}
		out, err := invokePi(context.Background(), root, func(context.Context, process.Command) process.Result {
			t.Fatal("Pi launched with a missing topic")
			return process.Result{}
		}, "run", "1")
		if err == nil || !strings.Contains(err.Error(), `recorded topic "missing" is missing`) {
			t.Fatalf("%v\n%s", err, out)
		}
		fails(t, root, `recorded topic "missing" is missing`, "merge", "1")
		if out := run(t, root, "cleanup", "1", "--apply"); strings.Contains(out, "Removed") || !strings.Contains(out, `Keep branch task/example-1: recorded topic "missing" is missing`) {
			t.Fatal(out)
		}
		if systemGit(t, root, "branch", "--list", "task/example-1") == "" || systemGit(t, root, "branch", "--list", "missing") != "" {
			t.Fatal("lost the task branch or created the missing topic")
		}
	}
}

func TestPiCreatesTopicWithoutMerging(t *testing.T) {
	_, root := taskWorkflowFixture(t)
	run(t, root, "create", "Interactive", "--topic", "interactive", "--body", "Write the implementation.")
	main := commitImplementation(t, root, "new-base.txt", "latest main\n")
	master, slave, err := testutil.OpenPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	var worktree string
	err = Run(context.Background(), root, []string{"pi", "1"}, IO{In: slave, Out: slave, Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
		worktree = piWorktree(t, child)
		if systemGit(t, worktree, "rev-parse", "HEAD") != main {
			t.Fatal("Pi did not start from the new topic")
		}
		commitImplementation(t, worktree, "interactive.txt", "done\n")
		return process.Result{}
	})})
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, root, "done")
	if systemGit(t, root, "rev-parse", "interactive") != main || systemGit(t, root, "rev-parse", "main") != main {
		t.Fatal("interactive run merged implementation work")
	}
	uiBytes(t, filepath.Join(worktree, "interactive.txt"))
}

func TestTopicEditRespectsTaskLock(t *testing.T) {
	repo, root, _ := editorFixture(t)
	run(t, root, "create", "Retarget me", "--topic", "before", "--body", "Original")
	original := run(t, root, "show", "1", "--raw")
	directory, err := operationState(repo)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := taskLock(directory, "EXAMPLE", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	revised := strings.Replace(original, "Topic: before", "Topic: after", 1)
	t.Setenv("TASK_TEST_CONTENT", revised)
	if _, err := runTerminal(t, root, "edit", "1"); err == nil || !strings.Contains(err.Error(), "change topic: EXAMPLE-1 already has a running") {
		t.Fatalf("retarget under task lock: %v", err)
	}
	if got := run(t, root, "show", "1", "--raw"); got != original {
		t.Fatalf("blocked edit changed task: %s", got)
	}
	t.Setenv("TASK_TEST_CONTENT", strings.Replace(original, "Original", "Revised body", 1))
	if _, err := runTerminal(t, root, "edit", "1"); err != nil {
		t.Fatalf("body-only edit unexpectedly blocked: %v", err)
	}
	lock.Close()
	t.Setenv("TASK_TEST_CONTENT", revised)
	if _, err := runTerminal(t, root, "edit", "1"); err != nil {
		t.Fatalf("retarget after releasing lock: %v", err)
	}
	if got := run(t, root, "show", "1", "--raw"); got != revised {
		t.Fatalf("retarget did not publish: %s", got)
	}
}

func TestMergeRechecksTopicAfterPreMergeHook(t *testing.T) {
	repo, root := fixture(t)
	ticket := strings.TrimSpace(run(t, root, "create", "Merge me", "--topic", "before", "--body", "Original"))
	systemGit(t, root, "branch", "after")
	systemGit(t, root, "switch", "-c", "before")
	before := commitImplementation(t, root, "destination.txt", "destination change\n")
	systemGit(t, root, "switch", "main")
	worktree := filepath.Join(t.TempDir(), "task")
	systemGit(t, root, "worktree", "add", "-b", "task/example-1", worktree, "main")
	commitImplementation(t, worktree, "feature.txt", "task change\n")

	hooks := t.TempDir()
	systemGit(t, root, "config", "core.hooksPath", hooks)
	if err := os.WriteFile(filepath.Join(hooks, "pre-merge-commit"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var called bool
	err := Run(context.Background(), root, []string{"merge", "1"}, IO{
		Out: io.Discard, Err: io.Discard,
		Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
			if filepath.Base(child.Args[0]) != "pre-merge-commit" {
				t.Fatalf("unexpected command: %q", child.Args)
			}
			called = true
			// Simulate a direct Git edit, outside the tasks lock protocol.
			writeSafetyFile(t, ticket, strings.Replace(string(uiBytes(t, ticket)), "Topic: before", "Topic: after", 1))
			metadata := filepath.Dir(ticket)
			systemGit(t, metadata, "add", "--", filepath.Base(ticket))
			systemGit(t, metadata, "commit", "-m", "retarget task")
			return process.Result{}
		}),
	})
	if !called || err == nil || !strings.Contains(err.Error(), `topic changed from "before" to "after"`) {
		t.Fatalf("merge did not reject the changed topic: %v (hook called: %v)", err, called)
	}
	if got := systemGit(t, root, "rev-parse", "before"); got != before {
		t.Fatalf("old destination moved: %s", got)
	}
	if got := systemGit(t, root, "rev-parse", "after"); got != systemGit(t, root, "rev-parse", "main") {
		t.Fatalf("new destination moved: %s", got)
	}
	if err := setResultForTopic(context.Background(), repo, "EXAMPLE", 1, "merged", "", "before"); err == nil || !strings.Contains(err.Error(), "topic changed") {
		t.Fatalf("could record merged on a different topic: %v", err)
	}
	assertStatus(t, root, "open")
}

func TestMergePublicationRejectsRawCommitAfterConfirmation(t *testing.T) {
	ctx := context.Background()
	repo, root := fixture(t)
	ticket := strings.TrimSpace(run(t, root, "create", "Merge me", "--topic", "before", "--body", "Original"))
	systemGit(t, root, "branch", "before")
	base, err := destination("before").tip(repo)
	if err != nil {
		t.Fatal(err)
	}
	source, err := createCheckout(repo, "example-1-", base, "")
	if err != nil {
		t.Fatal(err)
	}
	commitImplementation(t, source.Paths.WorkDir, "feature.txt", "task change\n")
	ref, err := repo.Reference(source.Head.Branch)
	if err != nil {
		t.Fatal(err)
	}
	source.Head.ID = ref.ID
	actor, err := repo.CommitterSignature()
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := confirmMergeDestination(ctx, repo, "EXAMPLE", 1, "before")
	if err != nil {
		t.Fatal(err)
	}
	writeSafetyFile(t, ticket, strings.Replace(string(uiBytes(t, ticket)), "Topic: before", "Topic: after", 1))
	systemGit(t, filepath.Dir(ticket), "commit", "-am", "retarget after confirmation")
	tx, err := prepareMergePublication(repo, "before", source.Head.ID, base, metadata, source.Head, actor)
	if tx != nil {
		tx.Close()
	}
	var changed *git.ChangedError
	if !errors.As(err, &changed) || changed.Name != store.Branch || changed.Expected != metadata {
		t.Fatalf("expected rejection of stale metadata, got %v", err)
	}
	if tip, err := destination("before").tip(repo); err != nil || tip != base {
		t.Fatalf("old destination moved: %s, %v", tip, err)
	}
}

func TestRecordingMergeResultRejectsRawCommitAfterRead(t *testing.T) {
	repo, root := fixture(t)
	ticket := strings.TrimSpace(run(t, root, "create", "Merge me", "--topic", "before", "--body", "Original"))
	session, err := store.Begin(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tasks, err := session.Preview("EXAMPLE")
	if err != nil {
		t.Fatal(err)
	}
	defer tasks.Close()
	head, err := tasks.Head()
	if err != nil {
		t.Fatal(err)
	}
	task, err := tasks.ReadWorking(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireTaskDestination(task, "before"); err != nil {
		t.Fatal(err)
	}
	result, err := store.WithStatus(task.Content, "merged")
	if err != nil {
		t.Fatal(err)
	}

	// A raw Git commit can retarget the task after the result check but
	// before the conditional write, bypassing the metadata session lock.
	retargeted := strings.Replace(task.Content, "Topic: before", "Topic: after", 1)
	writeSafetyFile(t, ticket, retargeted)
	metadata := filepath.Dir(ticket)
	systemGit(t, metadata, "commit", "-am", "retarget after reading task")
	after := systemGit(t, metadata, "rev-parse", "HEAD")
	if _, err := writeTask(session, tasks, head.ID, filepath.Base(ticket), result, "record merged for "+task.ID()); err == nil || !strings.Contains(err.Error(), "tasks ref differs from caller expectation") {
		t.Fatalf("expected stale merge result to be rejected, got %v", err)
	}
	if got := systemGit(t, metadata, "rev-parse", "HEAD"); got != after {
		t.Errorf("metadata ref moved from %s to %s", after, got)
	}
	if got := string(uiBytes(t, ticket)); got != retargeted {
		t.Errorf("stale merge result changed retargeted task: %s", got)
	}
}

func TestTopicOptionRejectsTaskNamespace(t *testing.T) {
	for _, topic := range []string{"task/example-2", "Task/other-1-build", "task/nested/branch"} {
		for _, args := range [][]string{{"go", "Title"}, {"create", "Title"}} {
			if _, err := parse(append(args, "--topic", topic)); err == nil || !strings.Contains(err.Error(), "reserved task/") {
				t.Errorf("%s accepted %q: %v", args[0], topic, err)
			}
		}
	}
}

func TestTopicOptionRejectsNonBranchValues(t *testing.T) {
	for _, value := range []string{"", "HEAD", "main^", "-name", "@{-1}", "refs/heads/topic", "one two", "line\nbreak", "tasks"} {
		for _, args := range [][]string{{"go", "Title"}, {"create", "Title", "--body", "Description"}} {
			if _, err := parse(append(args, "--topic="+value)); err == nil || !strings.Contains(err.Error(), "local implementation branch name") {
				t.Errorf("%s accepted %q: %v", args[0], value, err)
			}
		}
	}
}
