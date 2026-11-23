package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeKeepsCheckoutUsedAsRecordedDestination(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "dirty"}[dirty], func(t *testing.T) {
			_, root := fixture(t)
			const branch = "task/example-1"
			run(t, root, "create", "Parent", "--body", "Merge into main")
			path := strings.TrimSpace(run(t, root, "create", "Child", "--body", "Uses a legacy destination"))
			content := string(uiBytes(t, path))
			writeSafetyFile(t, path, strings.Replace(content, "Status: open\n", "Status: open\nTopic: "+branch+"\n", 1))
			metadata := filepath.Dir(path)
			systemGit(t, metadata, "add", "--", filepath.Base(path))
			systemGit(t, metadata, "commit", "-m", "record a dependent topic")
			if dirty {
				writeSafetyFile(t, path, content)
			}

			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			checkout := filepath.Join(dir, "task")
			systemGit(t, root, "worktree", "add", "-b", branch, checkout, "main")
			commitImplementation(t, checkout, ".gitignore", "ignored.txt\n")
			ignored := filepath.Join(checkout, "ignored.txt")
			writeSafetyFile(t, ignored, "preserve me\n")
			out, diagnostic, err := invoke(root, "", "merge", "1")
			if err != nil || !strings.Contains(out, "merged into local main") ||
				!strings.Contains(diagnostic, "recorded destination for EXAMPLE-2") {
				t.Fatalf("merge: %v\n%s\n%s", err, out, diagnostic)
			}
			if got := string(uiBytes(t, ignored)); got != "preserve me\n" {
				t.Fatal(got)
			}
		})
	}
}

func TestCleanupProtectsWorkingOnlyDestination(t *testing.T) {
	_, root := fixture(t)
	const branch = "task/example-2"
	path := strings.TrimSpace(run(t, root, "create", "Child", "--body", "Will use a legacy destination"))
	content := string(uiBytes(t, path))
	writeSafetyFile(t, path, strings.Replace(content, "Status: open\n", "Status: open\nTopic: "+branch+"\n", 1))
	systemGit(t, root, "branch", branch)
	if out := run(t, root, "cleanup", "--apply"); !strings.Contains(out, "recorded destination for EXAMPLE-1") {
		t.Fatal(out)
	}
}
