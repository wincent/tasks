package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/testutil"
)

func TestLaunchModelOverride(t *testing.T) {
	t.Parallel()
	const model = "openai-codex/gpt-6.2-sol:high"
	for _, command := range []string{"run", "go", "pi"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			wf := taskWorkflowFixture(t)
			root := wf.root
			models := []string{model, "openai-codex/gpt-6.2-sol:low", ""}
			if command == "go" {
				models = []string{model, ""} // Each launch creates a new task.
			} else {
				run(t, root, "create", "Model override", "--body", "requirements")
			}
			streams := IO{Env: wf.env}
			if command == "pi" {
				master, slave, err := testutil.OpenPTY()
				if err != nil {
					t.Fatal(err)
				}
				defer testutil.ClosePTY(master, slave)
				go io.Copy(io.Discard, master)
				streams.In, streams.Out = slave, slave
			}
			var session string
			for i, override := range models {
				target := "1"
				if command == "go" {
					target = fmt.Sprintf("Model override %d", i+1)
				}
				args := []string{command, target}
				if override != "" {
					args = append(args, "--model", override)
				}
				streams.Runner = piFixture(func(_ context.Context, child process.Command) process.Result {
					piWorktree(t, child)
					if i == 0 || command == "go" {
						session = child.Args[2]
					}
					want := []string{"--session", session}
					if command != "pi" {
						want = append(want, "--mode", "json")
					}
					if override != "" {
						want = append(want, "--model", override)
					}
					want = append(want, "--")
					if got := child.Args[1 : len(child.Args)-1]; !slices.Equal(got, want) {
						t.Fatalf("%q: Pi options = %q, want %q", args, got, want)
					}
					return process.Result{Code: 17}
				})
				if err := Run(context.Background(), root, args, streams); err == nil || ExitCode(err) != 17 {
					t.Fatalf("%q: expected Pi exit status 17, got %v", args, err)
				}
			}
		})
	}
}

func TestLaunchModelAliases(t *testing.T) {
	t.Parallel()
	const configYAML = `models:
  aliases:
    sol: openai-codex/gpt-6.2-sol
    fast: anthropic/claude-haiku-4-5:low
    local: ollama/llama3:8b
    chain: sol
`
	cases := []struct {
		flag string
		want string
	}{
		{"sol", "openai-codex/gpt-6.2-sol"},
		{"sol:high", "openai-codex/gpt-6.2-sol:high"},
		{"fast", "anthropic/claude-haiku-4-5:low"},
		{"fast:high", "anthropic/claude-haiku-4-5:high"},
		{"local:high", "ollama/llama3:8b:high"},
		{"chain", "sol"},
		{"chain:high", "sol:high"},
		{"openai-codex/gpt-6.2-sol:medium", "openai-codex/gpt-6.2-sol:medium"},
	}

	for _, command := range []string{"run", "pi", "go"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			wf := taskWorkflowFixture(t)
			root := wf.root
			xdg := t.TempDir()
			cfgPath := filepath.Join(xdg, "tasks", "config.yml")
			if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
				t.Fatal(err)
			}
			writeSafetyFile(t, cfgPath, configYAML)
			env := withEnv(wf.env, "XDG_CONFIG_HOME="+xdg)

			if command != "go" {
				run(t, root, "create", "Model aliases", "--body", "requirements")
			}

			streams := IO{Env: env}
			if command == "pi" {
				master, slave, err := testutil.OpenPTY()
				if err != nil {
					t.Fatal(err)
				}
				defer testutil.ClosePTY(master, slave)
				go io.Copy(io.Discard, master)
				streams.In, streams.Out = slave, slave
			}

			for i, tc := range cases {
				target := "1"
				if command == "go" {
					target = fmt.Sprintf("Model alias %d", i+1)
				}
				args := []string{command, target, "--model", tc.flag}
				var gotModel string
				streams.Runner = piFixture(func(_ context.Context, child process.Command) process.Result {
					piWorktree(t, child)
					idx := slices.Index(child.Args, "--model")
					if idx < 0 || idx+1 >= len(child.Args) {
						t.Fatalf("%q: missing --model in Pi args %q", args, child.Args)
					}
					gotModel = child.Args[idx+1]
					return process.Result{Code: 17}
				})
				if err := Run(context.Background(), root, args, streams); err == nil || ExitCode(err) != 17 {
					t.Fatalf("%q: expected Pi exit status 17, got %v", args, err)
				}
				if gotModel != tc.want {
					t.Fatalf("%q: Pi received --model %q, want %q", args, gotModel, tc.want)
				}
			}
		})
	}
}

func TestNonLaunchCommandsIgnoreInvalidConfig(t *testing.T) {
	t.Parallel()
	const invalidYAML = "models:\n\taliases:\n    sol: bad\n"

	wf := taskWorkflowFixture(t)
	root := wf.root
	cfgPath := filepath.Join(wf.home, ".config", "tasks", "config.yml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	writeSafetyFile(t, cfgPath, invalidYAML)

	run(t, root, "create", "Non-launch config", "--body", "requirements")
	env := withEnv(wf.env, "TASKS_TERMINAL_STATUS=bogus")

	for _, cmd := range [][]string{
		{"list"},
		{"check"},
		{"show", "1"},
		{"comment", "1", "--body", "note"},
	} {
		if err := Run(context.Background(), root, cmd, IO{Env: env}); err != nil {
			t.Fatalf("%q failed with invalid config.yml / TASKS_TERMINAL_STATUS: %v", cmd, err)
		}
	}
}

func TestLaunchInvalidConfigFailsBeforeMutatingState(t *testing.T) {
	t.Parallel()
	const invalidYAML = "models:\n  unknown: bad\n"

	for _, command := range []string{"run", "pi", "go"} {
		for _, withModel := range []bool{false, true} {
			name := command
			if withModel {
				name += "-with-model"
			} else {
				name += "-without-model"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				wf := taskWorkflowFixture(t)
				repo, root := wf.repo, wf.root
				cfgPath := filepath.Join(wf.home, ".config", "tasks", "config.yml")
				if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
					t.Fatal(err)
				}
				writeSafetyFile(t, cfgPath, invalidYAML)

				if command != "go" {
					run(t, root, "create", "No mutation", "--body", "requirements")
				}
				beforeTasks := fixtureRevision(t, repo, store.Branch)

				streams := IO{
					Env: wf.env,
					Runner: piFixture(func(context.Context, process.Command) process.Result {
						t.Fatal("Pi should not be launched when config.yml is invalid")
						return process.Result{}
					}),
				}
				if command == "pi" {
					master, slave, err := testutil.OpenPTY()
					if err != nil {
						t.Fatal(err)
					}
					defer testutil.ClosePTY(master, slave)
					go io.Copy(io.Discard, master)
					streams.In, streams.Out = slave, slave
				}

				target := "1"
				if command == "go" {
					target = "Should not be created"
				}
				args := []string{command, target}
				if withModel {
					args = append(args, "--model", "sol")
				}
				err := Run(context.Background(), root, args, streams)
				if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "invalid config") || !strings.Contains(err.Error(), "line 2:") {
					t.Fatalf("%v with invalid config: got error %v, want invalid config line 2 error", args, err)
				}

				if afterTasks := fixtureRevision(t, repo, store.Branch); afterTasks != beforeTasks {
					t.Fatalf("%s mutated task history despite invalid config.yml", command)
				}
				if command != "go" {
					assertStatus(t, root, "open")
				}
				if _, err := os.Stat(filepath.Join(root, ".agent-worktrees")); !os.IsNotExist(err) {
					t.Fatalf("%s created worktree directory despite invalid config.yml: %v", command, err)
				}
				if command == "go" {
					if _, err := os.Stat(filepath.Join(root, ".git", "tasks")); !os.IsNotExist(err) {
						t.Fatalf("%s created task state directory despite invalid config.yml: %v", command, err)
					}
				} else {
					if record, err := readTaskSession(filepath.Join(root, ".git", "tasks"), 1); err != nil || record != nil {
						t.Fatalf("%s wrote task session despite invalid config.yml: %+v, %v", command, record, err)
					}
				}
				if _, err := os.Stat(filepath.Join(wf.home, ".pi")); !os.IsNotExist(err) {
					t.Fatalf("%s created Pi session directory despite invalid config.yml: %v", command, err)
				}
			})
		}
	}
}

func TestLaunchInvalidTerminalStatusEnvFailsBeforeMutatingState(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"run", "pi", "go"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			wf := taskWorkflowFixture(t)
			repo, root := wf.repo, wf.root
			if command != "go" {
				run(t, root, "create", "No mutation", "--body", "requirements")
			}
			beforeTasks := fixtureRevision(t, repo, store.Branch)

			streams := IO{
				Env: withEnv(wf.env, "TASKS_TERMINAL_STATUS=flase"),
				Runner: piFixture(func(context.Context, process.Command) process.Result {
					t.Fatal("Pi should not be launched when TASKS_TERMINAL_STATUS is invalid")
					return process.Result{}
				}),
			}
			if command == "pi" {
				master, slave, err := testutil.OpenPTY()
				if err != nil {
					t.Fatal(err)
				}
				defer testutil.ClosePTY(master, slave)
				go io.Copy(io.Discard, master)
				streams.In, streams.Out = slave, slave
			}

			target := "1"
			if command == "go" {
				target = "Should not be created"
			}
			err := Run(context.Background(), root, []string{command, target}, streams)
			if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "TASKS_TERMINAL_STATUS") || !strings.Contains(err.Error(), `"flase"`) {
				t.Fatalf("%s with invalid TASKS_TERMINAL_STATUS: got error %v, want TASKS_TERMINAL_STATUS error", command, err)
			}

			if afterTasks := fixtureRevision(t, repo, store.Branch); afterTasks != beforeTasks {
				t.Fatalf("%s mutated task history despite invalid TASKS_TERMINAL_STATUS", command)
			}
			if command != "go" {
				assertStatus(t, root, "open")
			}
			if _, err := os.Stat(filepath.Join(root, ".agent-worktrees")); !os.IsNotExist(err) {
				t.Fatalf("%s created worktree directory despite invalid TASKS_TERMINAL_STATUS: %v", command, err)
			}
			if command == "go" {
				if _, err := os.Stat(filepath.Join(root, ".git", "tasks")); !os.IsNotExist(err) {
					t.Fatalf("%s created task state directory despite invalid TASKS_TERMINAL_STATUS: %v", command, err)
				}
			} else {
				if record, err := readTaskSession(filepath.Join(root, ".git", "tasks"), 1); err != nil || record != nil {
					t.Fatalf("%s wrote task session despite invalid TASKS_TERMINAL_STATUS: %+v, %v", command, record, err)
				}
			}
			if _, err := os.Stat(filepath.Join(wf.home, ".pi")); !os.IsNotExist(err) {
				t.Fatalf("%s created Pi session directory despite invalid TASKS_TERMINAL_STATUS: %v", command, err)
			}
		})
	}
}
