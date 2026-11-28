package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/testutil"
)

func ptyCapture(t *testing.T) (*os.File, func() string) {
	t.Helper()
	master, slave, err := testutil.OpenPTYWithWidth(120)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.ClosePTY(master, slave) })
	const sentinel = "<<TASKS_TEST_PTY_EOF>>"
	if err := master.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		tmp := make([]byte, 4096)
		for {
			n, err := master.Read(tmp)
			if n > 0 {
				buf.Write(tmp[:n])
				if idx := bytes.Index(buf.Bytes(), []byte(sentinel)); idx >= 0 {
					done <- strings.ReplaceAll(string(buf.Bytes()[:idx]), "\r\n", "\n")
					return
				}
			}
			if err != nil {
				done <- strings.ReplaceAll(buf.String(), "\r\n", "\n")
				return
			}
		}
	}()
	return slave, func() string {
		// Drain up to a sentinel before closing the last slave descriptor;
		// Darwin PTYs can drop unread trailing bytes when the slave closes.
		_, _ = io.WriteString(slave, sentinel)
		out := <-done
		slave.Close()
		return out
	}
}

var tmuxOSC7501Pattern = regexp.MustCompile(`\x1bPtmux;\x1b\x1b\]7501;([^\x1b]+)\x1b\x1b\\\x1b\\`)

type osc7501Record struct {
	state string
	id    string
	app   string
}

var osc7501IDPattern = regexp.MustCompile(`^tasks/[0-9a-f]{32}$`)

func parseTmuxOSC7501(t *testing.T, raw string) []osc7501Record {
	t.Helper()
	matches := tmuxOSC7501Pattern.FindAllStringSubmatch(raw, -1)
	var records []osc7501Record
	for _, match := range matches {
		fields := map[string]string{}
		for _, pair := range strings.Split(match[1], ":") {
			k, v, ok := strings.Cut(pair, "=")
			if !ok {
				t.Fatalf("malformed OSC 7501 field %q in %q", pair, match[0])
			}
			fields[k] = v
		}
		if len(fields) != 3 {
			t.Fatalf("unexpected OSC 7501 fields %v in %q", fields, match[0])
		}
		rec := osc7501Record{
			state: fields["state"],
			id:    fields["id"],
			app:   fields["app"],
		}
		if !osc7501IDPattern.MatchString(rec.id) {
			t.Fatalf("unexpected OSC 7501 id %q in %q", rec.id, match[0])
		}
		if rec.app != "tasks" {
			t.Fatalf("unexpected OSC 7501 app %q in %q", rec.app, match[0])
		}
		records = append(records, rec)
	}
	return records
}

func assertOSC7501States(t *testing.T, raw string, want ...string) {
	t.Helper()
	records := parseTmuxOSC7501(t, raw)
	var got []string
	var id string
	for i, rec := range records {
		got = append(got, rec.state)
		if i == 0 {
			id = rec.id
		} else if rec.id != id {
			t.Fatalf("OSC 7501 id changed mid-run: %q -> %q (%+v)", id, rec.id, records)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("OSC 7501 states = %q, want %q\nraw output: %q", got, want, raw)
	}
	// Ensure no unparsed 7501 sequences remain.
	stripped := tmuxOSC7501Pattern.ReplaceAllString(raw, "")
	if strings.Contains(stripped, "7501;") {
		t.Fatalf("found unparsed OSC 7501 sequence in %q", stripped)
	}
}

func TestProgramStatusRunAndGoSuccess(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		args      []string
		setupTask bool
		commit    bool
	}{
		{name: "run merged", args: []string{"run", "1"}, setupTask: true, commit: true},
		{name: "run no-merge", args: []string{"run", "1", "--no-merge"}, setupTask: true, commit: true},
		{name: "run closed without changes", args: []string{"run", "1"}, setupTask: true, commit: false},
		{name: "go merged", args: []string{"go", "Ship status"}, setupTask: false, commit: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wf := taskWorkflowFixture(t)
			if tc.setupTask {
				run(t, wf.root, "create", "Status test", "--body", "requirements")
			}

			out, readOut := ptyCapture(t)
			var diag bytes.Buffer
			env := withEnv(wf.env, "TMUX=/tmp/tmux-1000/default,123,0", "TERM=tmux-256color")
			err := Run(context.Background(), wf.root, tc.args, IO{
				Out: out,
				Err: &diag,
				Env: env,
				Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
					if tc.commit {
						commitImplementation(t, piWorktree(t, child), "status.txt", "ok\n")
					}
					return process.Result{}
				}),
			})
			gotOut := readOut()
			if err != nil {
				t.Fatalf("%v failed: %v\nstderr: %s", tc.args, err, diag.String())
			}
			assertOSC7501States(t, gotOut, "working", "done")
			if strings.Contains(diag.String(), "7501;") {
				t.Fatalf("unexpected OSC 7501 sequence on stderr: %q", diag.String())
			}
		})
	}
}

func TestProgramStatusFailuresEmitError(t *testing.T) {
	t.Parallel()
	t.Run("pi exit non-zero", func(t *testing.T) {
		t.Parallel()
		wf := taskWorkflowFixture(t)
		run(t, wf.root, "create", "Failing run", "--body", "requirements")
		out, readOut := ptyCapture(t)
		var diag bytes.Buffer
		env := withEnv(wf.env, "TMUX=/tmp/tmux-1000/default,123,0", "TERM=tmux-256color")
		err := Run(context.Background(), wf.root, []string{"run", "1"}, IO{
			Out: out,
			Err: &diag,
			Env: env,
			Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
				piWorktree(t, child)
				return process.Result{Code: 17}
			}),
		})
		gotOut := readOut()
		if err == nil || ExitCode(err) != 17 {
			t.Fatalf("got error %v, want exit 17", err)
		}
		assertOSC7501States(t, gotOut, "working", "error")
	})

	t.Run("pi needs attention", func(t *testing.T) {
		t.Parallel()
		wf := taskWorkflowFixture(t)
		run(t, wf.root, "create", "Attention run", "--body", "requirements")
		out, readOut := ptyCapture(t)
		var diag bytes.Buffer
		env := withEnv(wf.env, "TMUX=/tmp/tmux-1000/default,123,0", "TERM=tmux-256color")
		err := Run(context.Background(), wf.root, []string{"run", "1"}, IO{
			Out: out,
			Err: &diag,
			Env: env,
			Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
				piWorktree(t, child)
				_, _ = fmt.Fprintln(child.Out, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Needs attention: clarify requirement."}]}}`)
				return process.Result{}
			}),
		})
		gotOut := readOut()
		if err == nil {
			t.Fatal("expected error for Needs attention response")
		}
		assertOSC7501States(t, gotOut, "working", "error")
	})

	t.Run("merge conflict", func(t *testing.T) {
		t.Parallel()
		wf := taskWorkflowFixture(t)
		commitImplementation(t, wf.root, "conflict.txt", "base\n")
		run(t, wf.root, "create", "Merge conflict", "--body", "requirements")
		out, readOut := ptyCapture(t)
		var diag bytes.Buffer
		env := withEnv(wf.env, "TMUX=/tmp/tmux-1000/default,123,0", "TERM=tmux-256color")
		err := Run(context.Background(), wf.root, []string{"run", "1"}, IO{
			Out: out,
			Err: &diag,
			Env: env,
			Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
				commitImplementation(t, piWorktree(t, child), "conflict.txt", "task\n")
				commitImplementation(t, wf.root, "conflict.txt", "main\n")
				return process.Result{}
			}),
		})
		gotOut := readOut()
		if err == nil {
			t.Fatal("expected merge conflict error")
		}
		assertOSC7501States(t, gotOut, "working", "error")
	})

	t.Run("post-checkout hook failure", func(t *testing.T) {
		t.Parallel()
		wf := taskWorkflowFixture(t)
		run(t, wf.root, "create", "Hook failure", "--body", "requirements")
		hookPath := filepath.Join(wf.root, ".git", "hooks", "post-checkout")
		if err := os.MkdirAll(filepath.Dir(hookPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}

		out, readOut := ptyCapture(t)
		var diag bytes.Buffer
		env := withEnv(wf.env, "TMUX=/tmp/tmux-1000/default,123,0", "TERM=tmux-256color")
		err := Run(context.Background(), wf.root, []string{"run", "1"}, IO{
			Out: out,
			Err: &diag,
			Env: env,
			Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
				if filepath.Base(child.Args[0]) == "post-checkout" {
					return process.Result{Code: 1}
				}
				t.Fatalf("unexpected command after hook failure: %v", child.Args)
				return process.Result{}
			}),
		})
		gotOut := readOut()
		if err == nil {
			t.Fatal("expected post-checkout hook error")
		}
		assertOSC7501States(t, gotOut, "working", "error")
	})

	t.Run("go failure emits error", func(t *testing.T) {
		t.Parallel()
		wf := taskWorkflowFixture(t)
		out, readOut := ptyCapture(t)
		var diag bytes.Buffer
		env := withEnv(wf.env, "TMUX=/tmp/tmux-1000/default,123,0", "TERM=tmux-256color")
		err := Run(context.Background(), wf.root, []string{"go", "Failing go"}, IO{
			Out: out,
			Err: &diag,
			Env: env,
			Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
				piWorktree(t, child)
				return process.Result{Code: 17}
			}),
		})
		gotOut := readOut()
		if err == nil || ExitCode(err) != 17 {
			t.Fatalf("got error %v, want exit 17", err)
		}
		assertOSC7501States(t, gotOut, "working", "error")
	})

	t.Run("resumed checkout emits working and done with distinct id", func(t *testing.T) {
		t.Parallel()
		wf := taskWorkflowFixture(t)
		run(t, wf.root, "create", "Resume status", "--body", "requirements")
		env := withEnv(wf.env, "TMUX=/tmp/tmux-1000/default,123,0", "TERM=tmux-256color")

		out1, readOut1 := ptyCapture(t)
		var diag1 bytes.Buffer
		err := Run(context.Background(), wf.root, []string{"run", "1"}, IO{
			Out: out1,
			Err: &diag1,
			Env: env,
			Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
				piWorktree(t, child)
				return process.Result{Code: 17}
			}),
		})
		got1 := readOut1()
		if err == nil || ExitCode(err) != 17 {
			t.Fatalf("first run: got error %v, want exit 17", err)
		}
		assertOSC7501States(t, got1, "working", "error")

		out2, readOut2 := ptyCapture(t)
		var diag2 bytes.Buffer
		err = Run(context.Background(), wf.root, []string{"run", "1"}, IO{
			Out: out2,
			Err: &diag2,
			Env: env,
			Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
				commitImplementation(t, piWorktree(t, child), "resumed.txt", "ok\n")
				return process.Result{}
			}),
		})
		got2 := readOut2()
		if err != nil {
			t.Fatalf("second run failed: %v\nstderr: %s", err, diag2.String())
		}
		assertOSC7501States(t, got2, "working", "done")
		id1 := parseTmuxOSC7501(t, got1)[0].id
		id2 := parseTmuxOSC7501(t, got2)[0].id
		if id1 == id2 {
			t.Fatalf("expected distinct OSC 7501 ids across runs, both were %q", id1)
		}
	})
}

func TestProgramStatusCancellationClearsRecord(t *testing.T) {
	t.Parallel()
	wf := taskWorkflowFixture(t)
	run(t, wf.root, "create", "Cancelled run", "--body", "requirements")

	ctx, cancel := context.WithCancel(context.Background())
	out, readOut := ptyCapture(t)
	var diag bytes.Buffer
	env := withEnv(wf.env, "TMUX=/tmp/tmux-1000/default,123,0", "TERM=tmux-256color")
	err := Run(ctx, wf.root, []string{"run", "1"}, IO{
		Out: out,
		Err: &diag,
		Env: env,
		Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
			piWorktree(t, child)
			cancel()
			return process.Result{Code: -2}
		}),
	})
	gotOut := readOut()
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("got error %v, want context.Canceled", err)
	}
	assertOSC7501States(t, gotOut, "working", "clear")
}

func TestProgramStatusGatingConditions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		args       []string
		tty        bool
		envUpdates []string
		wantStates []string
	}{
		{
			name:       "outside tmux",
			args:       []string{"run", "1"},
			tty:        true,
			envUpdates: []string{"TMUX", "TERM=xterm-256color"},
			wantStates: nil,
		},
		{
			name:       "empty tmux",
			args:       []string{"run", "1"},
			tty:        true,
			envUpdates: []string{"TMUX=", "TERM=xterm-256color"},
			wantStates: nil,
		},
		{
			name:       "non-terminal stdout",
			args:       []string{"run", "1"},
			tty:        false,
			envUpdates: []string{"TMUX=/tmp/tmux-1000/default,123,0", "TERM=tmux-256color"},
			wantStates: nil,
		},
		{
			name:       "dumb terminal",
			args:       []string{"run", "1"},
			tty:        true,
			envUpdates: []string{"TMUX=/tmp/tmux-1000/default,123,0", "TERM=dumb"},
			wantStates: nil,
		},
		{
			name:       "no color still emits program status",
			args:       []string{"run", "1"},
			tty:        true,
			envUpdates: []string{"TMUX=/tmp/tmux-1000/default,123,0", "TERM=tmux-256color", "NO_COLOR=1"},
			wantStates: []string{"working", "done"},
		},
		{
			name:       "tasks pi does not emit",
			args:       []string{"pi", "1"},
			tty:        true,
			envUpdates: []string{"TMUX=/tmp/tmux-1000/default,123,0", "TERM=tmux-256color"},
			wantStates: nil,
		},
		{
			name:       "preflight error emits nothing",
			args:       []string{"run", "999"},
			tty:        true,
			envUpdates: []string{"TMUX=/tmp/tmux-1000/default,123,0", "TERM=tmux-256color"},
			wantStates: nil,
		},
		{
			name:       "non-launch command emits nothing",
			args:       []string{"list"},
			tty:        true,
			envUpdates: []string{"TMUX=/tmp/tmux-1000/default,123,0", "TERM=tmux-256color"},
			wantStates: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wf := taskWorkflowFixture(t)
			run(t, wf.root, "create", "Gating test", "--body", "requirements")

			env := withEnv(wf.env, tc.envUpdates...)
			var diag bytes.Buffer
			streams := IO{
				Err: &diag,
				Env: env,
				Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
					commitImplementation(t, piWorktree(t, child), "ok.txt", "done\n")
					return process.Result{}
				}),
			}
			var readOut func() string
			if tc.tty {
				slave, reader := ptyCapture(t)
				streams.In, streams.Out = slave, slave
				readOut = reader
			} else {
				var buf bytes.Buffer
				streams.Out = &buf
				readOut = buf.String
			}

			err := Run(context.Background(), wf.root, tc.args, streams)
			gotOut := readOut()
			if tc.name == "preflight error emits nothing" {
				if err == nil {
					t.Fatal("expected preflight error for nonexistent task")
				}
			} else if err != nil {
				t.Fatalf("%v failed: %v\nstderr: %s", tc.args, err, diag.String())
			}
			assertOSC7501States(t, gotOut, tc.wantStates...)
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestProgramStatusHelpers(t *testing.T) {
	t.Parallel()

	t.Run("deduplicates identical state writes", func(t *testing.T) {
		var buf bytes.Buffer
		s := &programStatus{out: &buf, id: "tasks/0123456789abcdef0123456789abcdef", enabled: true}
		s.working()
		s.working()
		s.finish(context.Background(), nil)
		s.clearIfActive() // no-op after finish
		assertOSC7501States(t, buf.String(), "working", "done")
	})

	t.Run("clearIfActive clears active working state", func(t *testing.T) {
		var buf bytes.Buffer
		s := &programStatus{out: &buf, id: "tasks/0123456789abcdef0123456789abcdef", enabled: true}
		s.working()
		s.clearIfActive()
		s.clearIfActive() // second call is a no-op
		assertOSC7501States(t, buf.String(), "working", "clear")
	})

	t.Run("write errors are ignored", func(t *testing.T) {
		s := &programStatus{out: failingWriter{}, id: "tasks/0123456789abcdef0123456789abcdef", enabled: true}
		s.working()
		s.finish(context.Background(), errors.New("boom"))
		if s.last != "" {
			t.Fatalf("last = %q, want empty after failed writes", s.last)
		}
	})
}
