package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"testing"

	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/testutil"
)

func TestLaunchModelOverride(t *testing.T) {
	const model = "openai-codex/gpt-6.2-sol:high"
	for _, command := range []string{"run", "go", "pi"} {
		t.Run(command, func(t *testing.T) {
			_, root := taskWorkflowFixture(t)
			models := []string{model, "openai-codex/gpt-6.2-sol:low", ""}
			if command == "go" {
				models = []string{model, ""} // Each launch creates a new task.
			} else {
				run(t, root, "create", "Model override", "--body", "requirements")
			}
			var streams IO
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
