package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/cli"
	"github.com/wincent/tasks/internal/git"
)

func TestExecutableTailClosedPipeExitsNormally(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repository")
	repo, err := git.Init(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	seedMainConfig(t, repo)
	seedLocalTaskHistory(t, repo)
	for key, value := range map[string]string{"user.name": "Fixture", "user.email": "fixture@example.invalid"} {
		if err := repo.SetLocalConfig(key, value); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	root = paths.WorkDir
	file := filepath.Join(t.TempDir(), "session.jsonl")
	header, _ := json.Marshal(map[string]any{"type": "session", "id": "pipe-session", "cwd": root})
	body := `{"type":"message","message":{"role":"assistant","content":"pipeline output"}}` + "\n"
	if err := os.WriteFile(file, append(append(header, '\n'), []byte(body)...), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := cli.Run(context.Background(), root, []string{"create", "Pipe", "--body", "fixture"}, cli.IO{In: strings.NewReader(""), Out: &output, Err: &output}); err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(map[string]string{"worktree": root, "session_file": file, "session_id": "pipe-session"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.CommonDir, "tasks", "claim-1.json"), record, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "tail", "1")
	command.Env = append(os.Environ(), "TASK_TEST_MAIN_HELPER=1")
	command.Dir, command.Stdout, command.Stderr = root, writer, &output
	if err := command.Run(); err != nil {
		t.Fatalf("closed pipeline: %v\n%s", err, output.String())
	}
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("viewer changed session: %v", err)
	}
}
