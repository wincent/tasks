package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/process"
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
	systemGit(t, root, "branch", "-f", "delivery", implementation)
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
	if systemGit(t, root, "branch", "--list", "task/example-1") != "" || systemGit(t, root, "rev-parse", "main") != main || systemGit(t, root, "rev-parse", "delivery") != implementation {
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

func TestTopicOptionRejectsNonBranchValues(t *testing.T) {
	for _, value := range []string{"", "HEAD", "main^", "-name", "@{-1}", "refs/heads/topic", "one two", "line\nbreak", "tasks"} {
		for _, args := range [][]string{{"go", "Title"}, {"create", "Title", "--body", "Description"}} {
			if _, err := parse(append(args, "--topic", value)); err == nil || !strings.Contains(err.Error(), "local implementation branch name") {
				t.Errorf("%s accepted %q: %v", args[0], value, err)
			}
		}
	}
}
