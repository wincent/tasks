package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/process"
)

func TestRepositoryTaskPrefix(t *testing.T) {
	repo, root := taskWorkflowFixture(t)
	oldMain, err := repo.ResolveCommit(mainRef)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := createCheckout(repo, "stale-", oldMain, "")
	if err != nil {
		t.Fatal(err)
	}
	commitMainConfig(t, repo, "# Ticket key\nprefix: 'RUN' # shared by all worktrees\n")

	// The linked checkout still has prefix: EXAMPLE, but uses the config on main.
	path := strings.TrimSpace(run(t, stale.Paths.WorkDir, "create", "Configured prefix", "--body", "Example task"))
	if filepath.Base(path) != "RUN-1 Configured prefix.md" {
		t.Fatalf("create: %s", path)
	}
	for _, arg := range []string{"1", "RUN-1", "run-1"} {
		if out := run(t, root, "show", arg, "--raw"); !strings.HasPrefix(out, "# RUN-1: Configured prefix\n") {
			t.Fatalf("show %s: %s", arg, out)
		}
	}
	if _, _, err := invoke(root, "", "show", "EXAMPLE-1"); err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "RUN-1") {
		t.Fatalf("wrong prefix: %v", err)
	}
	if out := run(t, root, "list", "all"); !strings.Contains(out, "RUN-1\topen\tConfigured prefix") {
		t.Fatalf("list: %s", out)
	}
	artifact := filepath.Join(t.TempDir(), "evidence.txt")
	writeSafetyFile(t, artifact, "fixture evidence")
	if out := strings.TrimSpace(run(t, root, "store", "RUN-1", artifact)); out != "tasks/storage/RUN-1/evidence.txt" {
		t.Fatalf("store: %s", out)
	}

	var checkoutPath string
	out, err := invokePi(context.Background(), root, func(_ context.Context, cmd process.Command) process.Result {
		checkoutPath = piWorktree(t, cmd)
		if !strings.HasPrefix(filepath.Base(checkoutPath), "run-1-") || !strings.HasPrefix(cmd.Args[len(cmd.Args)-1], "Read RUN-1 at ") {
			t.Fatalf("checkout %s, args %v", checkoutPath, cmd.Args)
		}
		if branch := strings.TrimPrefix(fixtureHead(t, openFixtureRepo(t, checkoutPath)).Branch, "refs/heads/"); !strings.HasPrefix(branch, "task/run-1-") {
			t.Fatalf("task branch: %s", branch)
		}
		commitImplementation(t, checkoutPath, "feature.txt", "fixture")
		return process.Result{}
	}, "run", "run-1", "--no-merge")
	if err != nil || !strings.Contains(out, "RUN-1 (Configured prefix) ready for review.\n") {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if out := strings.TrimSpace(run(t, root, "worktree", "1")); filepath.Join(root, out) != checkoutPath {
		t.Fatalf("worktree: %s, want %s", out, checkoutPath)
	}
	if out := run(t, checkoutPath, "merge"); !strings.Contains(out, "RUN-1 (Configured prefix) merged into local main.\n") {
		t.Fatalf("merge: %s", out)
	}
	if out := run(t, root, "check"); !strings.Contains(out, "Checked 1 task(s)") {
		t.Fatalf("check: %s", out)
	}
	writeSafetyFile(t, filepath.Join(root, "tasks", "EXAMPLE-9000 Wrong prefix.md"), "Status: open\n")
	if path := strings.TrimSpace(run(t, root, "create", "Next task", "--body", "fixture")); filepath.Base(path) != "RUN-2 Next task.md" {
		t.Fatalf("wrong-prefix file affected allocation: %s", path)
	}
	if _, _, err := invoke(root, "", "check"); err == nil || !strings.Contains(err.Error(), "expected 'RUN-") {
		t.Fatalf("check wrong filename: %v", err)
	}
}

func TestTaskConfigRequiresExactLocalMain(t *testing.T) {
	repo, root := fixture(t)
	main, err := repo.Reference(mainRef)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureLooseRef(t, repo, "refs/tags/refs/heads/main", main.ID)
	setRef(t, repo, mainRef, git.ID{}, main.ID)
	if _, _, err := invoke(root, "", "init"); err == nil || !strings.Contains(err.Error(), "read local main for task configuration") {
		t.Fatalf("missing main with lookalike tag: %v", err)
	}
	setRef(t, repo, "refs/heads/other", main.ID, git.ID{})
	systemGit(t, root, "symbolic-ref", mainRef, "refs/heads/other")
	if _, _, err := invoke(root, "", "init"); err == nil || !strings.Contains(err.Error(), "local main must be a direct branch") {
		t.Fatalf("symbolic main: %v", err)
	}
}

func TestTaskConfigIsRequiredOnMain(t *testing.T) {
	for _, tt := range []struct {
		name, config, want string
	}{
		{"missing", "", "missing from local main"},
		{"empty", "# comment only\n", "missing prefix"},
		{"duplicates", "prefix: EXAMPLE\nprefix: WEB\n", "invalid .tasks.yml"},
		{"list", "prefix: [EXAMPLE, WEB]\n", "invalid .tasks.yml"},
		{"invalid characters", "prefix: EXAMPLE-\n", "invalid .tasks.yml"},
		{"unknown key", "prefix: EXAMPLE\nextra: ignored\n", "invalid .tasks.yml"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo, root := fixture(t)
			commitMainConfig(t, repo, tt.config)
			// A valid but uncommitted file does not rescue the setting on main.
			writeSafetyFile(t, filepath.Join(root, ".tasks.yml"), "prefix: EXAMPLE\n")
			for _, args := range [][]string{{"init"}, {"create", "No config", "--body", "fixture"}, {"show", "1"}, {"cleanup"}} {
				out, _, err := invoke(root, "", args...)
				if out != "" || err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "commit a root .tasks.yml to local main") {
					t.Fatalf("%v: out=%q error=%v", args, out, err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "tasks")); !os.IsNotExist(err) {
				t.Fatalf("created task checkout: %v", err)
			}
			if out := run(t, t.TempDir(), "show", "--help"); !strings.Contains(out, "Usage: tasks show") {
				t.Fatalf("help: %s", out)
			}
		})
	}
}
