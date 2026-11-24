package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/testutil"
)

type piFixture func(context.Context, process.Command) process.Result

func (f piFixture) Run(ctx context.Context, cmd process.Command) process.Result { return f(ctx, cmd) }

func taskWorkflowFixture(t *testing.T) (*git.Repository, string) {
	t.Helper()
	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, "pi"), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", path+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("HOME", t.TempDir())
	repo, root := fixture(t)
	// Keep task worktrees out of main's status without relying on a global ignore.
	info := filepath.Join(root, ".git", "info")
	if err := os.MkdirAll(info, 0700); err != nil {
		t.Fatal(err)
	}
	writeSafetyFile(t, filepath.Join(info, "exclude"), "/.agent-worktrees/\n")
	return repo, root
}

func invokePi(ctx context.Context, root string, f piFixture, args ...string) (string, error) {
	var out bytes.Buffer
	err := Run(ctx, root, args, IO{Out: &out, Err: &out, Runner: f})
	return out.String(), err
}

// Like Pi, the fixture gets its work directory from the selected transcript,
// not from the launch directory used by the sandbox wrapper.
func piWorktree(t *testing.T, child process.Command) string {
	t.Helper()
	if len(child.Args) < 3 || child.Args[1] != "--session" {
		t.Fatalf("expected an explicit Pi session: %v", child.Args)
	}
	f, err := os.Open(child.Args[2])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var header struct {
		Cwd string `json:"cwd"`
	}
	if err := json.NewDecoder(f).Decode(&header); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(header.Cwd) || !inside(header.Cwd, child.Directory) || header.Cwd == child.Directory {
		t.Fatalf("Pi worktree %q should be inside launch directory %q", header.Cwd, child.Directory)
	}
	return header.Cwd
}

func systemGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitImplementation(t *testing.T, dir, name, text string) string {
	t.Helper()
	writeSafetyFile(t, filepath.Join(dir, name), text)
	systemGit(t, dir, "add", "--", name)
	systemGit(t, dir, "commit", "-m", "implement "+name)
	return systemGit(t, dir, "rev-parse", "HEAD")
}

func assertStatus(t *testing.T, root, status string) {
	t.Helper()
	actual, err := store.Status(run(t, root, "show", "1", "--raw"))
	if err != nil || actual != status {
		t.Fatalf("status %q, want %q: %v", actual, status, err)
	}
}

type cancelOnOutput struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelOnOutput) Write(p []byte) (int, error) {
	w.cancel()
	return w.Buffer.Write(p)
}

func assertTail(t *testing.T, root, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	transcript := &cancelOnOutput{cancel: cancel}
	err := Run(ctx, root, []string{"tail", "1"}, IO{Out: transcript})
	if !errors.Is(err, context.Canceled) || !strings.Contains(transcript.String(), want) {
		t.Fatalf("expected to tail %q: %v\n%s", want, err, transcript)
	}
}

func TestTaskRunStartsAtMainAndMergesWithoutChecks(t *testing.T) {
	repo, root := taskWorkflowFixture(t)
	main := commitImplementation(t, root, "base.txt", "from main\n")
	systemGit(t, root, "switch", "-c", "other")
	other := commitImplementation(t, root, "other.txt", "unrelated\n")
	writeSafetyFile(t, filepath.Join(root, "unsaved.txt"), "preserve\n")
	path := strings.TrimSpace(run(t, root, "create", "Ship something", "--body", "requirements"))
	run(t, root, "comment", "1", "--body", "Also cover this.\n\nStatus: example in a note")
	var checkoutPath, sessionPath, delivered string
	out, err := invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		checkoutPath, sessionPath = piWorktree(t, child), child.Args[2]
		if child.Directory != root || filepath.Dir(sessionPath) != filepath.Join(os.Getenv("HOME"), ".pi", "agent", "sessions", "dev.wincent.tasks") {
			t.Fatalf("unexpected Pi launch directory %q or session path %q", child.Directory, sessionPath)
		}
		if head := systemGit(t, checkoutPath, "rev-parse", "HEAD"); head != main {
			t.Fatalf("started at %s, main %s", head, main)
		}
		if prompt := child.Args[len(child.Args)-1]; !strings.Contains(prompt, path) || strings.Contains(prompt, "task complete") {
			t.Fatal(prompt)
		}
		if out := run(t, root, "status", "--json"); !strings.Contains(out, `"status": "active"`) {
			t.Fatal(out)
		}
		fmt.Fprintln(child.Out, `{"type":"tool_execution_start","toolName":"bash"}`)
		delivered = commitImplementation(t, checkoutPath, ".gitignore", "ignored.log\n")
		writeSafetyFile(t, filepath.Join(checkoutPath, "ignored.log"), "discard only ignored content")
		f, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(f, `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}`)
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		assertTail(t, root, "Done.") // The task runner still holds the run lock.
		return process.Result{}
	}, "run", "1")
	if err != nil || !strings.Contains(out, "Running bash") ||
		!strings.Contains(out, "Merging EXAMPLE-1 (Ship something) into local main...\n") ||
		!strings.Contains(out, "EXAMPLE-1 (Ship something) merged into local main.\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	assertStatus(t, root, "merged")
	if head, _ := repo.Head(); head.ID.String() != other {
		t.Fatal("unrelated checkout moved")
	}
	if ref, _ := repo.Reference(mainRef); ref.ID.String() != delivered {
		t.Fatal("implementation not in main")
	}
	if _, err := os.Stat(checkoutPath); !os.IsNotExist(err) {
		t.Fatalf("checkout retained: %v", err)
	}
	if got := string(uiBytes(t, filepath.Join(root, "unsaved.txt"))); got != "preserve\n" {
		t.Fatal(got)
	}
	directory := filepath.Join(root, ".git", "tasks")
	if record, err := readTaskSession(directory, 1); err != nil || record == nil || record.SessionFile != sessionPath || record.SessionID == nil || filepath.Base(sessionPath) != *record.SessionID+".jsonl" || record.Worktree != checkoutPath || record.Branch == "" {
		t.Fatalf("lost session association: %+v, %v", record, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("unexpected local transcript store: %v", err)
	}
	assertTail(t, root, "Done.")
	// Reconcile via the retained branch, without a checkout or a progress line.
	if out, diagnostic, err := invoke(root, "", "merge", "1"); err != nil || diagnostic != "" ||
		!strings.HasPrefix(out, "EXAMPLE-1 (Ship something) merged into local main.\n") {
		t.Fatalf("merge retry = %q, %q, %v", out, diagnostic, err)
	}
}

func TestRunReviewThenMergeAmendedBranch(t *testing.T) {
	_, root := taskWorkflowFixture(t)
	run(t, root, "create", "Review", "--body-file", "-", "--help") // Help is side-effect free.
	run(t, root, "create", "Review", "--body", "requirements")
	base := systemGit(t, root, "rev-parse", "main")
	var checkoutPath string
	out, err := invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		checkoutPath = piWorktree(t, child)
		commitImplementation(t, checkoutPath, "one.txt", "first\n")
		return process.Result{}
	}, "run", "1", "--no-merge")
	if err != nil || !strings.Contains(out, "ready for review") {
		t.Fatalf("%v\n%s", err, out)
	}
	assertStatus(t, root, "done")
	if got := systemGit(t, root, "rev-parse", "main"); got != base {
		t.Fatal("review run merged")
	}
	if got := strings.TrimSpace(run(t, root, "worktree", "1")); filepath.Join(root, got) != checkoutPath {
		t.Fatal(got)
	}
	commitImplementation(t, checkoutPath, "two.txt", "amendment\n")
	commitImplementation(t, root, "meanwhile.txt", "main advanced\n")
	run(t, checkoutPath, "merge") // Infer the task from the checked-out branch.
	assertStatus(t, root, "merged")
	for _, name := range []string{"one.txt", "two.txt", "meanwhile.txt"} {
		uiBytes(t, filepath.Join(root, name))
	}
	branch := systemGit(t, checkoutPath, "branch", "--show-current")
	writeSafetyFile(t, filepath.Join(checkoutPath, "unfinished.txt"), "keep\n")
	if out := run(t, root, "cleanup", "1", "--apply"); !strings.Contains(out, "Keep branch "+branch+": still checked out") {
		t.Fatal(out)
	}
	uiBytes(t, filepath.Join(checkoutPath, "unfinished.txt"))
	if err := os.Remove(filepath.Join(checkoutPath, "unfinished.txt")); err != nil {
		t.Fatal(err)
	}
	if out := run(t, checkoutPath, "cleanup", "1"); !strings.Contains(out, "current directory is inside") || !strings.Contains(out, "Keep branch "+branch+": still checked out") {
		t.Fatal(out)
	}
	if out := run(t, root, "cleanup", "1"); !strings.Contains(out, "Would remove "+checkoutPath) || !strings.Contains(out, "Would remove branch "+branch) {
		t.Fatal(out)
	}
	if out := run(t, root, "cleanup", "1", "--apply"); !strings.Contains(out, "Removed "+checkoutPath) || !strings.Contains(out, "Removed branch "+branch) {
		t.Fatal(out)
	}
	if _, err := os.Stat(checkoutPath); !os.IsNotExist(err) {
		t.Fatalf("checkout not removed: %v", err)
	}
	if out := systemGit(t, root, "branch", "--list", branch); out != "" {
		t.Fatalf("branch not removed: %s", out)
	}
}

func TestRunKeepsWorkOnFailureOrInterruption(t *testing.T) {
	for _, mode := range []string{"dirty", "nonzero", "interrupt", "question", "pi-error"} {
		t.Run(mode, func(t *testing.T) {
			_, root := taskWorkflowFixture(t)
			base := systemGit(t, root, "rev-parse", "main")
			run(t, root, "create", "Continuation", "--body", "requirements")
			var worktree, session string
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			out, err := invokePi(ctx, root, func(_ context.Context, child process.Command) process.Result {
				worktree, session = piWorktree(t, child), child.Args[2]
				fails(t, root, "already has a running agent", "run", "1")
				if mode == "dirty" {
					writeSafetyFile(t, filepath.Join(worktree, "partial"), "keep\n")
					return process.Result{}
				}
				commitImplementation(t, worktree, "partial", "keep\n")
				switch mode {
				case "nonzero":
					return process.Result{Code: 7}
				case "interrupt":
					cancel()
				case "question":
					fmt.Fprintln(child.Out, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Needs attention: a question"}]}}`)
				case "pi-error":
					fmt.Fprintln(child.Out, `{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"provider failed"}}`)
				}
				return process.Result{}
			}, "run", "1")
			if err == nil || !strings.Contains(err.Error(), "needs attention") {
				t.Fatalf("%v\n%s", err, out)
			}
			assertStatus(t, root, "blocked")
			if got := systemGit(t, root, "rev-parse", "main"); got != base {
				t.Fatal("failed run merged")
			}
			uiBytes(t, filepath.Join(worktree, "partial"))
			out, err = invokePi(context.Background(), worktree, func(_ context.Context, child process.Command) process.Result {
				if child.Directory != root || piWorktree(t, child) != worktree || child.Args[2] != session {
					t.Fatalf("did not continue %s %s: %+v", worktree, session, child)
				}
				commitImplementation(t, worktree, "partial", "finish\n")
				return process.Result{}
			}, "run", "1")
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			assertStatus(t, root, "merged")
		})
	}
}

func TestRunRejectsMissingPromptBeforeStartingPi(t *testing.T) {
	_, root := taskWorkflowFixture(t)
	run(t, root, "create", "Missing prompt", "--body", "requirements")

	out, err := invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		t.Fatalf("Pi started with invalid arguments: %v", child.Args)
		return process.Result{}
	}, "run", "1", "--prompt", "--no-merge")
	if err == nil || err.Error() != "--prompt requires a value" || ExitCode(err) != 2 || out != "" {
		t.Fatalf("expected a usage error with no launch output, got %v\n%s", err, out)
	}
	assertStatus(t, root, "open")
}

func TestRunPromptIsPerInvocation(t *testing.T) {
	_, root := taskWorkflowFixture(t)
	path := strings.TrimSpace(run(t, root, "create", "Per-run instructions", "--body", "requirements"))
	base := systemGit(t, root, "rev-parse", "main")
	const first = "Run the unit tests first."
	const retry = "- Don't run Chrome tests.\n- Run the worker suite."
	var worktree, session, standard string
	for i, hint := range []string{first, retry, ""} {
		args := []string{"run", "1", "--no-merge"}
		if hint != "" {
			args = append(args, "--prompt", hint)
		}
		out, err := invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
			checkout := piWorktree(t, child)
			if i == 0 {
				worktree, session = checkout, child.Args[2]
			} else if checkout != worktree || child.Args[2] != session {
				t.Fatalf("did not resume checkout %q and session %q: %v", worktree, session, child.Args)
			}
			if len(child.Args) != 7 || child.Args[5] != "--" {
				t.Fatalf("expected a single prompt argument after --: %v", child.Args)
			}
			prompt := child.Args[6]
			if hint != "" {
				var ok bool
				prompt, ok = strings.CutSuffix(prompt, "\n\nAdditional instructions for this run:\n"+hint)
				if !ok {
					t.Fatalf("expected hint appended verbatim, got %q", child.Args[6])
				}
			}
			if i == 0 {
				standard = prompt
				if !strings.HasPrefix(standard, fmt.Sprintf("Read EXAMPLE-1 at %q.", path)) ||
					!strings.Contains(standard, "Needs attention:") || !strings.Contains(standard, "Do not merge;") {
					t.Fatalf("missing standard instructions: %q", standard)
				}
			} else if prompt != standard {
				t.Fatalf("standard prompt changed: got %q, want %q", prompt, standard)
			}
			if i == 0 {
				fmt.Fprintln(child.Out, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Needs attention: which checks should I run?"}]}}`)
			} else if i == 1 {
				commitImplementation(t, worktree, "implementation", "keep\n")
			}
			return process.Result{}
		}, args...)
		if i == 0 {
			if err == nil || !strings.Contains(err.Error(), "needs attention") {
				t.Fatalf("%v\n%s", err, out)
			}
			assertStatus(t, root, "blocked")
		} else {
			if err != nil || !strings.Contains(out, "ready for review") {
				t.Fatalf("%v\n%s", err, out)
			}
			assertStatus(t, root, "done")
		}
		if task := run(t, root, "show", "1", "--raw"); strings.Contains(task, first) || strings.Contains(task, retry) {
			t.Fatalf("hint leaked into task file: %q", task)
		}
	}
	if got := systemGit(t, root, "rev-parse", "main"); got != base {
		t.Fatal("review run merged")
	}
}

func TestPiPromptIsPerInvocation(t *testing.T) {
	_, root := taskWorkflowFixture(t)
	path := strings.TrimSpace(run(t, root, "create", "Interactive instructions", "--body", "requirements"))
	base := systemGit(t, root, "rev-parse", "main")
	const first = "Skip Chrome tests."
	const retry = "- Skip the browser suite.\n- Still run the worker suite."
	fails(t, root, "`tasks pi` requires a terminal", "pi", "1", "--prompt", first)

	master, slave, err := testutil.OpenPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.ClosePTY(master, slave)
	go io.Copy(io.Discard, master) // Keep repeated launches from filling the terminal buffer.

	var worktree, session, standard string
	for i, hint := range []string{first, retry, ""} {
		args := []string{"pi", "1"}
		if hint != "" {
			args = append(args, "--prompt", hint)
		}
		runner := piFixture(func(_ context.Context, child process.Command) process.Result {
			if child.In != slave || child.Out != slave || len(child.Args) != 5 || child.Args[3] != "--" {
				t.Fatalf("expected an interactive launch with one prompt argument: %+v", child)
			}
			checkout, prompt := piWorktree(t, child), child.Args[4]
			if hint != "" {
				var ok bool
				prompt, ok = strings.CutSuffix(prompt, "\n\nAdditional instructions for this run:\n"+hint)
				if !ok {
					t.Fatalf("expected hint appended verbatim, got %q", child.Args[4])
				}
			}
			if i == 0 {
				worktree, session, standard = checkout, child.Args[2], prompt
				if !strings.HasPrefix(standard, fmt.Sprintf("Read EXAMPLE-1 at %q.", path)) ||
					!strings.Contains(standard, "Needs attention:") || !strings.Contains(standard, "Do not merge;") {
					t.Fatalf("missing standard instructions: %q", standard)
				}
				commitImplementation(t, checkout, "implementation", "keep\n")
			} else if checkout != worktree || child.Args[2] != session || prompt != standard {
				t.Fatalf("expected checkout %q, session %q and standard prompt %q, got %q: %v", worktree, session, standard, checkout, child.Args)
			}
			return process.Result{}
		})
		if err := Run(context.Background(), root, args, IO{In: slave, Out: slave, Runner: runner}); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, root, "done")
		if task := run(t, root, "show", "1", "--raw"); strings.Contains(task, first) || strings.Contains(task, retry) {
			t.Fatalf("hint leaked into task file: %q", task)
		}
		if got := systemGit(t, root, "rev-parse", "main"); got != base {
			t.Fatal("interactive launch merged")
		}
	}
}

func TestExistingTaskSessionAndDeletedTranscript(t *testing.T) {
	repo, root := taskWorkflowFixture(t)
	t.Setenv("PI_CODING_AGENT_DIR", "~/custom-agent")
	run(t, root, "create", "Keep the old transcript", "--body", "requirements")
	base, err := repo.ResolveCommit(mainRef)
	if err != nil {
		t.Fatal(err)
	}
	source, err := createCheckout(repo, "example-1-", base, "")
	if err != nil {
		t.Fatal(err)
	}
	worktree := source.Paths.WorkDir
	revision := commitImplementation(t, worktree, "implementation", "keep\n")
	directory := filepath.Join(root, ".git", "tasks")
	oldDir := filepath.Join(directory, "sessions")
	if err := os.MkdirAll(oldDir, 0700); err != nil {
		t.Fatal(err)
	}
	id := "35efceb4-a9ae-4ebb-ac22-711c23b7aa01"
	oldPath := filepath.Join(oldDir, id+".jsonl")
	validHeader := fmt.Sprintf("{\"type\":\"session\",\"version\":3,\"id\":%q,\"cwd\":%q,\"timestamp\":\"2026-11-17T10:00:00Z\"}\n", id, worktree)
	writeSafetyFile(t, oldPath, validHeader+`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"Original transcript."}]}}`+"\n")
	// A different transcript for this checkout must never replace the saved path.
	decoy := "9667cee4-f531-425e-a1e2-ce643020500e"
	writeSafetyFile(t, filepath.Join(oldDir, decoy+".jsonl"), strings.Replace(validHeader, id, decoy, 1))
	if err := writeTaskSession(directory, 1, taskSession{Worktree: worktree, Branch: source.Head.Branch, SessionID: &id, SessionFile: oldPath}); err != nil {
		t.Fatal(err)
	}

	master, slave, err := testutil.OpenPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.ClosePTY(master, slave)
	interactive := func(f piFixture) error {
		return Run(context.Background(), root, []string{"pi", "1"}, IO{In: slave, Out: slave, Runner: f})
	}
	assertTail(t, root, "Original transcript.")
	if err := interactive(func(_ context.Context, child process.Command) process.Result {
		if child.Directory != root || piWorktree(t, child) != worktree || child.Args[2] != oldPath {
			t.Fatalf("did not resume the exact saved session: %+v", child)
		}
		return process.Result{}
	}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []struct{ header, message string }{
		{strings.Replace(validHeader, id, "different-id", 1), "does not match the active Pi session ID"},
		{strings.Replace(validHeader, fmt.Sprintf("%q", worktree), fmt.Sprintf("%q", root), 1), "belongs to another worktree"},
	} {
		writeSafetyFile(t, oldPath, invalid.header)
		unexpectedLaunch := piFixture(func(_ context.Context, child process.Command) process.Result {
			t.Fatalf("launched Pi with an invalid transcript: %+v", child)
			return process.Result{}
		})
		_, runErr := invokePi(context.Background(), root, unexpectedLaunch, "run", "1")
		piErr := interactive(unexpectedLaunch)
		for _, err := range []error{runErr, piErr} {
			if err == nil || !strings.Contains(err.Error(), invalid.message) {
				t.Fatalf("expected %q, got %v", invalid.message, err)
			}
		}
	}
	if err := os.Remove(oldPath); err != nil {
		t.Fatal(err)
	}
	var newPath string
	before := run(t, root, "show", "1", "--raw")
	out, err := invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		newPath = child.Args[2]
		if piWorktree(t, child) != worktree || child.Directory != root {
			t.Fatalf("did not keep the checkout: %+v", child)
		}
		wantDir := filepath.Join(os.Getenv("HOME"), "custom-agent", "sessions", "dev.wincent.tasks")
		if filepath.Dir(newPath) != wantDir || newPath == oldPath {
			t.Fatalf("expected new transcript under %s, got %s", wantDir, newPath)
		}
		if got := systemGit(t, worktree, "rev-parse", "HEAD"); got != revision {
			t.Fatalf("lost implementation commit: %s, want %s", got, revision)
		}
		return process.Result{}
	}, "run", "1", "--no-merge")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	record, err := readTaskSession(directory, 1)
	if err != nil || record == nil || record.SessionFile != newPath || record.SessionID == nil || *record.SessionID == id {
		t.Fatalf("did not save the replacement session: %+v, %v", record, err)
	}
	if after := run(t, root, "show", "1", "--raw"); after != before {
		t.Fatalf("replacement changed task metadata:\nbefore: %s\nafter: %s", before, after)
	}
}

func TestMergeConflictRetainsTaskAndRetryUsesCurrentTip(t *testing.T) {
	_, root := taskWorkflowFixture(t)
	commitImplementation(t, root, "conflict", "base\n")
	run(t, root, "create", "Conflict", "--body", "requirements")
	out, err := invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		commitImplementation(t, piWorktree(t, child), "conflict", "task\n")
		commitImplementation(t, root, "conflict", "main\n")
		return process.Result{}
	}, "run", "1")
	if err == nil || !strings.Contains(err.Error(), "merge conflict; main is unchanged") ||
		!strings.Contains(err.Error(), "\nRetry with `tasks merge 1`\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	assertStatus(t, root, "blocked")
	path := filepath.Join(root, strings.TrimSpace(run(t, root, "worktree", "1")))
	if got := string(uiBytes(t, filepath.Join(root, "conflict"))); got != "main\n" {
		t.Fatal(got)
	}
	if got := string(uiBytes(t, filepath.Join(path, "conflict"))); got != "task\n" {
		t.Fatal(got)
	}
	cmd := exec.Command("git", "merge", "--no-edit", "main")
	cmd.Dir = path
	if err := cmd.Run(); err == nil {
		t.Fatal("expected a conflict on the task branch")
	}
	commitImplementation(t, path, "conflict", "resolved\n")
	run(t, root, "merge", "1")
	assertStatus(t, root, "merged")
	if got := string(uiBytes(t, filepath.Join(root, "conflict"))); got != "resolved\n" {
		t.Fatal(got)
	}
}

func TestMergeRetryFinishesAnInterruptedCheckoutUpdate(t *testing.T) {
	repo, root := taskWorkflowFixture(t)
	base, err := repo.ResolveCommit(mainRef)
	if err != nil {
		t.Fatal(err)
	}
	run(t, root, "create", "Recover merge", "--body", "requirements")
	var tip git.ID
	_, err = invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		revision := commitImplementation(t, piWorktree(t, child), "result", "task content\n")
		tip, err = git.ParseID(revision)
		if err != nil {
			t.Fatal(err)
		}
		return process.Result{}
	}, "run", "1", "--no-merge")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate termination between updating the checkout and publishing main.
	if err := repo.Checkout(base, tip); err != nil {
		t.Fatal(err)
	}
	writeSafetyFile(t, filepath.Join(root, "result"), "unrelated staged edit\n")
	systemGit(t, root, "add", "result")
	fails(t, root, "unrelated staged changes", "merge", "1")
	if got := string(uiBytes(t, filepath.Join(root, "result"))); got != "unrelated staged edit\n" {
		t.Fatal(got)
	}
	if actual, _ := repo.ResolveCommit(mainRef); actual != base {
		t.Fatal(actual)
	}

	writeSafetyFile(t, filepath.Join(root, "result"), "task content\n")
	systemGit(t, root, "add", "result")
	run(t, root, "merge", "1")
	run(t, root, "merge", "1")
	if actual, _ := repo.ResolveCommit(mainRef); actual != tip {
		t.Fatal(actual)
	}
	assertStatus(t, root, "merged")
}

func TestMergePreservesMainEditsAndDoesNotBypassSigning(t *testing.T) {
	repo, root := taskWorkflowFixture(t)
	base := systemGit(t, root, "rev-parse", "main")
	run(t, root, "create", "Merge safely", "--body", "requirements")
	_, err := invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		commitImplementation(t, piWorktree(t, child), "delivered", "new file\n")
		return process.Result{}
	}, "run", "1", "--no-merge")
	if err != nil {
		t.Fatal(err)
	}

	collision := filepath.Join(root, "delivered")
	writeSafetyFile(t, collision, "user edit\n")
	fails(t, root, "worktree is dirty", "merge", "1")
	if got := string(uiBytes(t, collision)); got != "user edit\n" {
		t.Fatal(got)
	}
	if got := systemGit(t, root, "rev-parse", "main"); got != base {
		t.Fatal(got)
	}
	if err := os.Remove(collision); err != nil {
		t.Fatal(err)
	}

	// Force a merge commit even on a fast-forward so signing must run.
	signer := filepath.Join(t.TempDir(), "signer")
	if err := os.WriteFile(signer, []byte("#!/bin/sh\necho 'fixture signer refused' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"merge.ff": "false", "commit.gpgsign": "true", "gpg.format": "openpgp", "gpg.openpgp.program": signer} {
		if err := repo.SetLocalConfig(key, value); err != nil {
			t.Fatal(err)
		}
	}
	_, diagnostic, err := invoke(root, "", "merge", "1")
	if err == nil || !strings.Contains(diagnostic+err.Error(), "fixture signer refused") {
		t.Fatalf("%s %v", diagnostic, err)
	}
	if got := systemGit(t, root, "rev-parse", "main"); got != base {
		t.Fatal("unsigned merge published", got)
	}
	assertStatus(t, root, "blocked")
}

func TestRunWithoutChangesClosesWithExplanation(t *testing.T) {
	_, root := taskWorkflowFixture(t)
	run(t, root, "create", "No changes", "--body", "requirements")
	base := systemGit(t, root, "rev-parse", "main")
	out, err := invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		fmt.Fprintln(child.Out, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"The feature already works."}]}}`)
		return process.Result{}
	}, "run", "1")
	if err != nil || !strings.Contains(out, "EXAMPLE-1 (No changes) closed without changes.\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := run(t, root, "show", "1"); !strings.Contains(got, "The feature already works.") {
		t.Fatal(got)
	}
	assertStatus(t, root, "closed")
	if got := systemGit(t, root, "rev-parse", "main"); got != base {
		t.Fatal("no-change run touched main")
	}
}

func TestCreateNumbersSharedAcrossWorktrees(t *testing.T) {
	repo, root := fixture(t)
	run(t, root, "init")
	head, _ := repo.Head()
	second, err := createCheckout(repo, "numbers-", head.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := repo.Paths()
	writeSafetyFile(t, filepath.Join(stateDirectory(paths), "ledger.json"), `{"high_water":41}`)
	results := make(chan error, 8)
	for i := range 8 {
		dir := root
		if i%2 == 0 {
			dir = second.Paths.WorkDir
		}
		go func() {
			_, _, err := invoke(dir, "stdin description", "create", fmt.Sprintf("Work %d", i), "--body-file", "-")
			results <- err
		}()
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	for n := 42; n <= 49; n++ {
		if output := run(t, root, "show", fmt.Sprint(n)); !strings.Contains(output, "stdin description") {
			t.Fatal(output)
		}
	}
}

func TestRemovedCommandsAndFlagsAreNotAliases(t *testing.T) {
	for _, args := range [][]string{{"queue"}, {"admission"}, {"start", "1"}, {"stop", "1"}, {"complete", "1"}, {"block", "1"}, {"unblock", "1"}, {"reopen", "1"}, {"handoff", "1"}, {"integrate", "1"}, {"submit", "1"}, {"mark-merged", "1"}, {"reservations"}, {"reserve-ticket-number"}, {"release-ticket-number", "1"}, {"validation-plan"}, {"run", "1", "--reuse"}, {"run", "1", "--queue-bound"}, {"pi", "1", "--in-place"}, {"pi", "1", "--resume"}, {"tail", "1", "--no-follow"}, {"tail", "1", "--all"}, {"tail", "1", "--full"}, {"merge", "1", "--no-test"}, {"create", "Title", "--number", "1"}, {"close", "1", "--commit", "HEAD"}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("accepted removed invocation: %v", args)
		}
	}
}
