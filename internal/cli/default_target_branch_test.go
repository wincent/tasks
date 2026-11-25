package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/testutil"
)

func masterWorkflowFixture(t *testing.T, config string) workflowFixture {
	t.Helper()
	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, "pi"), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	env := withEnv(nil,
		"PATH="+path+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PI_CODING_AGENT_DIR=",
		"HOME="+home,
		"XDG_CONFIG_HOME",
	)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "repository")
	repo, err := git.Init(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repo.Close)
	for key, value := range map[string]string{"user.name": "Task Test", "user.email": "task@example.invalid", "commit.gpgsign": "false"} {
		if err := repo.SetLocalConfig(key, value); err != nil {
			t.Fatal(err)
		}
	}
	const masterRef = "refs/heads/master"
	if err := repo.SetHead(masterRef); err != nil {
		t.Fatal(err)
	}
	who := git.Signature{Name: "Task Test", Email: "task@example.invalid", When: time.Unix(1700000000, 0)}
	empty, err := repo.WriteTree(nil)
	if err != nil {
		t.Fatal(err)
	}
	base, err := repo.WriteCommit(empty, nil, who, who, "initial master\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Checkout(base, base); err != nil {
		t.Fatal(err)
	}
	setRef(t, repo, masterRef, base, git.ID{})

	if config != "" {
		commitTasksConfig(t, repo, config)
	} else {
		seedEmptyLocalTasks(t, repo)
	}
	info := filepath.Join(root, ".git", "info")
	if err := os.MkdirAll(info, 0700); err != nil {
		t.Fatal(err)
	}
	writeSafetyFile(t, filepath.Join(info, "exclude"), "/.agent-worktrees/\n")
	return workflowFixture{repo: repo, root: root, home: home, env: env}
}

func TestInitBootstrapWithoutConfigThenCommitConfig(t *testing.T) {
	t.Parallel()
	wf := masterWorkflowFixture(t, "")
	root := wf.root

	// tasks init succeeds on an empty orphan refs/heads/tasks (no .tasks.yml yet).
	got := strings.TrimSpace(run(t, root, "init"))
	if got != filepath.Join(root, "tasks") {
		t.Fatalf("init path = %q, want %q", got, filepath.Join(root, "tasks"))
	}

	// Commands requiring prefix fail cleanly with refs/heads/tasks guidance.
	for _, args := range [][]string{
		{"create", "Before config", "--body", "Needs config"},
		{"list", "all"},
		{"show", "1"},
		{"cleanup"},
	} {
		out, _, err := invoke(root, "", args...)
		if out != "" || err == nil || !strings.Contains(err.Error(), ".tasks.yml is missing from refs/heads/tasks") {
			t.Fatalf("%v without .tasks.yml: out=%q err=%v", args, out, err)
		}
	}

	// Uncommitted tasks/.tasks.yml is not enough.
	tasksDir := filepath.Join(root, "tasks")
	writeSafetyFile(t, filepath.Join(tasksDir, ".tasks.yml"), "prefix: DEMO\ndefault_target_branch: master\n")
	if _, _, err := invoke(root, "", "create", "Still uncommitted", "--body", "x"); err == nil || !strings.Contains(err.Error(), ".tasks.yml is missing from refs/heads/tasks") {
		t.Fatalf("uncommitted tasks/.tasks.yml accepted: %v", err)
	}

	// Commit tasks/.tasks.yml on refs/heads/tasks; commands now succeed.
	systemGit(t, tasksDir, "add", ".tasks.yml")
	systemGit(t, tasksDir, "commit", "-m", "chore: configure tasks")
	created := strings.TrimSpace(run(t, root, "create", "First task", "--body", "Configured on tasks branch"))
	if filepath.Base(created) != "DEMO-1 First task.md" {
		t.Fatalf("created = %q", created)
	}
	if out := run(t, root, "check"); !strings.Contains(out, "Checked 1 task(s)") {
		t.Fatalf("check: %s", out)
	}
}

func TestDefaultTargetBranchEndToEndWorkflow(t *testing.T) {
	t.Parallel()
	wf := masterWorkflowFixture(t, "prefix: DEMO\ndefault_target_branch: master\n")
	repo, root := wf.repo, wf.root
	const masterRef = "refs/heads/master"

	// Confirm main does not exist in this repository.
	if fixtureHasBranch(t, repo, "main") {
		t.Fatal("fixture unexpectedly has refs/heads/main")
	}

	if got := strings.TrimSpace(run(t, root, "init")); got != filepath.Join(root, "tasks") {
		t.Fatalf("init: %q", got)
	}

	// 1. Create tasks: omitted --topic and --topic master both omit Topic:;
	// --topic main writes Topic: main as a custom topic branch.
	run(t, root, "create", "Default work", "--body", "Lands on master.")
	run(t, root, "create", "Explicit default", "--topic", "master", "--body", "Also lands on master.")
	run(t, root, "create", "Main topic", "--topic", "main", "--body", "Uses main as custom topic.")

	for _, id := range []string{"DEMO-1", "DEMO-2"} {
		raw := run(t, root, "show", id, "--raw")
		if strings.Contains(raw, "Topic:") {
			t.Fatalf("%s should omit Topic: line:\n%s", id, raw)
		}
	}
	if raw := run(t, root, "show", "DEMO-3", "--raw"); !strings.Contains(raw, "\nStatus: open\nTopic: main\n") {
		t.Fatalf("DEMO-3 should record Topic: main:\n%s", raw)
	}

	// 2. Edit: changing DEMO-2 from default (master) to Topic: main requires
	// the task lock; adding Topic: master (same effective destination) does not.
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "capture.json")
	visual := "'" + strings.ReplaceAll(binary, "'", "'\\''") + "' -test.run=TestEditorProcess --"
	runEdit := func(content string, args ...string) error {
		master, slave, err := testutil.OpenPTY()
		if err != nil {
			t.Fatal(err)
		}
		defer testutil.ClosePTY(master, slave)
		env := withEnv(nil,
			"TASK_TEST_EDITOR_HELPER=1",
			"TASK_TEST_CAPTURE="+capture,
			"VISUAL="+visual,
			"TASK_TEST_CONTENT="+content,
		)
		var out bytes.Buffer
		return Run(context.Background(), root, args, IO{In: slave, Out: &out, Err: slave, Env: env})
	}
	directory, err := operationState(repo)
	if err != nil {
		t.Fatal(err)
	}
	lock2, err := taskLock(directory, "DEMO", 2)
	if err != nil {
		t.Fatal(err)
	}
	// Changing effective topic from master to main must be blocked by taskLock.
	if err := runEdit("# DEMO-2: Explicit default\n\nStatus: open\nTopic: main\n\n## Description\n\nRetargeted.\n", "edit", "2"); err == nil || !strings.Contains(err.Error(), "change topic:") {
		lock2.Close()
		t.Fatalf("edit changing default master to Topic: main while locked: %v", err)
	}
	// Writing Topic: master keeps the same destination (master), so it succeeds while locked.
	if err := runEdit("# DEMO-2: Explicit default\n\nStatus: open\nTopic: master\n\n## Description\n\nExplicit master.\n", "edit", "2"); err != nil {
		lock2.Close()
		t.Fatalf("edit keeping destination master while locked: %v", err)
	}
	lock2.Close()
	// After releasing the lock, retargeting to Topic: main succeeds.
	if err := runEdit("# DEMO-2: Explicit default\n\nStatus: open\nTopic: main\n\n## Description\n\nRetargeted to main.\n", "edit", "2"); err != nil {
		t.Fatalf("edit retargeting to Topic: main: %v", err)
	}
	if raw := run(t, root, "show", "2", "--raw"); !strings.Contains(raw, "\nTopic: main\n") {
		t.Fatalf("DEMO-2 after edit:\n%s", raw)
	}

	// 3. Run DEMO-1 (default_target_branch = master): starts from master, merges into master.
	masterTip := commitImplementation(t, root, "base.txt", "from master\n")
	var demo1Commit git.ID
	out, err := wf.invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		wt := piWorktree(t, child)
		if head := fixtureRevision(t, openFixtureRepo(t, wt), "HEAD"); head != masterTip {
			t.Fatalf("DEMO-1 started at %s, want master %s", head, masterTip)
		}
		demo1Commit = commitImplementation(t, wt, "demo1.txt", "on master\n")
		return process.Result{}
	}, "run", "1")
	if err != nil || !strings.Contains(out, "Topic: master\n") || !strings.Contains(out, "DEMO-1 (Default work) merged into local master.\n") {
		t.Fatalf("run 1: %v\n%s", err, out)
	}
	if got := fixtureRevision(t, repo, masterRef); got != demo1Commit {
		t.Fatalf("master = %s, want %s", got, demo1Commit)
	}
	if fixtureHasBranch(t, repo, "main") {
		t.Fatal("running default-target task created main")
	}

	// 4. Interactive `tasks pi 3` (Topic: main): auto-creates refs/heads/main from
	// master without merging; then `tasks merge 3` merges into local main.
	var demo3Worktree string
	var demo3Commit git.ID
	ptyMaster, ptySlave, err := testutil.OpenPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.ClosePTY(ptyMaster, ptySlave)
	var piErr bytes.Buffer
	err = Run(context.Background(), root, []string{"pi", "3"}, IO{
		In: ptySlave, Out: ptySlave, Err: &piErr, Env: wf.env,
		Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
			demo3Worktree = piWorktree(t, child)
			if head := fixtureRevision(t, openFixtureRepo(t, demo3Worktree), "HEAD"); head != demo1Commit {
				t.Fatalf("DEMO-3 started at %s, want master %s", head, demo1Commit)
			}
			demo3Commit = commitImplementation(t, demo3Worktree, "main-only.txt", "on main topic\n")
			return process.Result{}
		}),
	})
	if err != nil || !strings.Contains(piErr.String(), "Topic: main\n") {
		t.Fatalf("pi 3: %v\n%s", err, piErr.String())
	}
	if got := fixtureRevision(t, repo, mainRef); got != demo1Commit {
		t.Fatalf("auto-created topic main = %s, want master %s", got, demo1Commit)
	}
	if got := fixtureRevision(t, repo, masterRef); got != demo1Commit {
		t.Fatalf("pi 3 moved master to %s", got)
	}

	mergeOut := run(t, root, "merge", "3")
	if !strings.Contains(mergeOut, "DEMO-3 (Main topic) merged into local main.\n") {
		t.Fatalf("merge 3: %s", mergeOut)
	}
	if got := fixtureRevision(t, repo, mainRef); got != demo3Commit {
		t.Fatalf("main = %s, want %s", got, demo3Commit)
	}
	if got := fixtureRevision(t, repo, masterRef); got != demo1Commit {
		t.Fatalf("merge 3 into Topic: main moved master to %s", got)
	}

	// 5. `tasks go` with a custom topic auto-creates it from master and merges there.
	var autoCommit git.ID
	goOut, err := wf.invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		wt := piWorktree(t, child)
		if head := fixtureRevision(t, openFixtureRepo(t, wt), "HEAD"); head != demo1Commit {
			t.Fatalf("DEMO-4 started at %s, want master %s", head, demo1Commit)
		}
		autoCommit = commitImplementation(t, wt, "auto.txt", "auto topic\n")
		return process.Result{}
	}, "go", "Auto topic", "--topic", "feature/auto")
	if err != nil || !strings.Contains(goOut, "DEMO-4 (Auto topic) merged into local feature/auto.\n") {
		t.Fatalf("go --topic feature/auto: %v\n%s", err, goOut)
	}
	if got := fixtureRevision(t, repo, "refs/heads/feature/auto"); got != autoCommit {
		t.Fatalf("feature/auto = %s, want %s", got, autoCommit)
	}
	if got := fixtureRevision(t, repo, masterRef); got != demo1Commit {
		t.Fatalf("go --topic feature/auto moved master to %s", got)
	}

	// 6. Cleanup: orphaned branches without ticket records fall back to master;
	// DEMO-2 (Topic: main) checks main, not master.
	// Create a branch for DEMO-2 containing demo3Commit (in main, not in master)
	// and another branch for DEMO-2 containing a new unmerged commit not in main.
	setRef(t, repo, "refs/heads/task/demo-2-in-main", demo3Commit, git.ID{})
	setRef(t, repo, "refs/heads/task/demo-2-not-in-main", autoCommit, git.ID{})
	// Orphaned branches for unrecorded task 99: one in master, one only in main.
	setRef(t, repo, "refs/heads/task/demo-99-in-master", demo1Commit, git.ID{})
	setRef(t, repo, "refs/heads/task/demo-99-only-in-main", demo3Commit, git.ID{})

	for _, apply := range []bool{false, true} {
		args, action := []string{"cleanup"}, "Would remove"
		if apply {
			args, action = append(args, "--apply"), "Removed"
		}
		cleanupOut := run(t, root, args...)
		for _, want := range []string{
			action + " branch task/demo-2-in-main",
			"Keep branch task/demo-2-not-in-main: branch is not merged into local main",
			action + " branch task/demo-99-in-master",
			"Keep branch task/demo-99-only-in-main: branch is not merged into local master",
		} {
			if !strings.Contains(cleanupOut, want+"\n") {
				t.Fatalf("%v missing %q in:\n%s", args, want, cleanupOut)
			}
		}
	}
	if fixtureHasBranch(t, repo, "task/demo-2-in-main") || fixtureHasBranch(t, repo, "task/demo-99-in-master") {
		t.Fatal("cleanup --apply did not remove eligible branches")
	}
	if !fixtureHasBranch(t, repo, "task/demo-2-not-in-main") || !fixtureHasBranch(t, repo, "task/demo-99-only-in-main") {
		t.Fatal("cleanup --apply removed unmerged branches")
	}
}

func TestDefaultTargetBranchMissingRefusedAndSymbolicRejected(t *testing.T) {
	t.Parallel()
	wf := masterWorkflowFixture(t, "prefix: DEMO\ndefault_target_branch: trunk\n")
	repo, root := wf.repo, wf.root

	// Missing trunk is tolerated by init and create.
	if got := strings.TrimSpace(run(t, root, "init")); got != filepath.Join(root, "tasks") {
		t.Fatalf("init with missing trunk: %q", got)
	}
	run(t, root, "create", "Needs trunk", "--body", "Default target is missing.")
	run(t, root, "create", "Custom from missing trunk", "--topic", "feat/x", "--body", "Needs trunk as base.")

	// Launching DEMO-1 refuses to auto-create default_target_branch (trunk).
	if _, err := wf.invokePi(context.Background(), root, func(_ context.Context, _ process.Command) process.Result {
		t.Fatal("Pi ran when default_target_branch was missing")
		return process.Result{}
	}, "run", "1"); err == nil || !strings.Contains(err.Error(), "read local trunk") {
		t.Fatalf("run 1 with missing trunk: %v", err)
	}
	if fixtureHasBranch(t, repo, "trunk") {
		t.Fatal("auto-created missing default_target_branch trunk")
	}

	// Launching DEMO-2 refuses to create topic feat/x when trunk is missing.
	if _, err := wf.invokePi(context.Background(), root, func(_ context.Context, _ process.Command) process.Result {
		t.Fatal("Pi ran when default_target_branch base was missing")
		return process.Result{}
	}, "run", "2"); err == nil || !strings.Contains(err.Error(), "read local trunk") {
		t.Fatalf("run 2 with missing trunk: %v", err)
	}
	if fixtureHasBranch(t, repo, "feat/x") {
		t.Fatal("created topic branch without default_target_branch")
	}

	// Symbolic default_target_branch is rejected by init / store verification.
	systemGit(t, root, "symbolic-ref", "refs/heads/trunk", "refs/heads/master")
	if _, _, err := invoke(root, "", "init"); err == nil || !strings.Contains(err.Error(), "trunk must be a direct branch to verify task history independence") {
		t.Fatalf("symbolic default_target_branch: %v", err)
	}
}

func TestDefaultTargetBranchOrphanCheckAndSymlinkConfig(t *testing.T) {
	t.Parallel()
	t.Run("shared history with configured default_target_branch", func(t *testing.T) {
		t.Parallel()
		wf := masterWorkflowFixture(t, "")
		repo, root := wf.repo, wf.root
		master, err := repo.ResolveCommit("refs/heads/master")
		if err != nil {
			t.Fatal(err)
		}
		oldTasks, err := repo.ResolveCommit(store.Branch)
		if err != nil {
			t.Fatal(err)
		}
		blob, err := repo.WriteBlob([]byte("prefix: DEMO\ndefault_target_branch: master\n"))
		if err != nil {
			t.Fatal(err)
		}
		tree, err := repo.WriteTree([]git.TreeEntry{{Name: ".tasks.yml", ID: blob, Mode: 0100644}})
		if err != nil {
			t.Fatal(err)
		}
		who := git.Signature{Name: "Task Test", Email: "task@example.invalid", When: time.Unix(1700000000, 0)}
		shared, err := repo.WriteCommit(tree, []git.ID{master}, who, who, "non-orphan tasks\n")
		if err != nil {
			t.Fatal(err)
		}
		setRef(t, repo, store.Branch, shared, oldTasks)

		for _, args := range [][]string{{"init"}, {"create", "Shared", "--body", "x"}, {"list", "all"}} {
			if _, _, err := invoke(root, "", args...); err == nil || !strings.Contains(err.Error(), "tasks shares implementation history with master") {
				t.Fatalf("%v shared with master: %v", args, err)
			}
		}
	})

	t.Run("symlink .tasks.yml rejected", func(t *testing.T) {
		t.Parallel()
		wf := masterWorkflowFixture(t, "")
		repo, root := wf.repo, wf.root
		oldTasks, err := repo.ResolveCommit(store.Branch)
		if err != nil {
			t.Fatal(err)
		}
		target, err := repo.WriteBlob([]byte("other.yml"))
		if err != nil {
			t.Fatal(err)
		}
		tree, err := repo.WriteTree([]git.TreeEntry{{Name: ".tasks.yml", ID: target, Mode: 0120000}})
		if err != nil {
			t.Fatal(err)
		}
		who := git.Signature{Name: "Task Test", Email: "task@example.invalid", When: time.Unix(1700000000, 0)}
		symlinkCommit, err := repo.WriteCommit(tree, []git.ID{oldTasks}, who, who, "symlink config\n")
		if err != nil {
			t.Fatal(err)
		}
		setRef(t, repo, store.Branch, symlinkCommit, oldTasks)

		for _, args := range [][]string{{"init"}, {"show", "1"}, {"cleanup"}} {
			if _, _, err := invoke(root, "", args...); err == nil || !strings.Contains(err.Error(), ".tasks.yml on refs/heads/tasks must be a regular file") {
				t.Fatalf("%v symlink config: %v", args, err)
			}
		}
	})
}
