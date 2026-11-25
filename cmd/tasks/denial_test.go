package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/git"
)

func TestNativeLifecycleWithOSExecutionDenied(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repository")
	repo, err := git.Init(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	seedMain(t, repo)
	seedLocalTaskHistory(t, repo)
	for key, value := range map[string]string{"user.name": "Fixture", "user.email": "fixture@example.invalid"} {
		if err := repo.SetLocalConfig(key, value); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"init"},
		{"create", "No subprocess", "--body", "native execution"},
		{"list", "all", "--json"}, {"show", "1", "--raw"},
		{"comment", "1", "--body", "no interpreter"},
		{"status", "--json"},
		{"close", "1", "--body", "fixture closed"},
		{"check"},
	}
	for _, args := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		command := exec.CommandContext(ctx, executable, args...)
		command.Dir = root
		command.Env = append(os.Environ(), "TASK_TEST_MAIN_HELPER=1", "TASK_TEST_DENY_EXEC=1")
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			// This check is required, not a silent unsupported-host skip.
			t.Fatalf("OS-denied %v: %v\n%s", args, err, output)
		}
		if strings.Contains(string(output), "positive control failed") {
			t.Fatal(string(output))
		}
	}
}
