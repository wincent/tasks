package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/store"
)

func TestStoreArtifacts(t *testing.T) {
	t.Parallel()
	repo, root := fixture(t)
	ticket := strings.TrimSpace(run(t, root, "create", "Benchmark", "--body", "Evidence"))
	run(t, root, "create", "Other task", "--body", "No evidence")
	if out := run(t, root, "store", "1"); out != "" {
		t.Fatalf("expected an empty listing, got %q", out)
	}

	checkout := filepath.Join(root, ".agent-worktrees", "fixture")
	main, err := repo.ResolveCommit("refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AddDetachedWorktree("artifact-fixture", checkout, main, func() error {
		return os.Mkdir(filepath.Dir(checkout), 0755)
	}); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(checkout, "z result.json")
	second := filepath.Join(root, "a.bin")
	writeSafetyFile(t, first, "{}\n")
	writeSafetyFile(t, second, "\x00\xff\x01")

	tasks, err := store.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer tasks.Close()
	content, err := os.ReadFile(ticket)
	if err != nil {
		t.Fatal(err)
	}
	modified := string(content) + "\nunrelated unstaged changes\n"
	writeSafetyFile(t, ticket, modified)
	staged := git.TreeEdit{Path: "unrelated.txt", Mode: 0100644}
	staged.ID, err = tasks.Repository.WriteBlob([]byte("staged"))
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(tasks.Paths.GitDir, "index")
	pending := filepath.Join(tasks.Paths.GitDir, "pending-index")
	original, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pending, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Repository.EditIndexFile(pending, git.ID{}, []git.TreeEdit{staged}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(pending, index); err != nil {
		t.Fatal(err)
	}
	ref := func() git.ID {
		t.Helper()
		value, err := repo.Reference(store.Branch)
		if err != nil {
			t.Fatal(err)
		}
		return value.ID
	}
	before := ref()
	const prefix = "tasks/storage/EXAMPLE-1/"
	const listing = prefix + "a.bin\n" + prefix + "z result.json\n"
	if out := run(t, checkout, "store", "example-1", "z result.json", second); out != "../../"+prefix+"z result.json\n../../"+prefix+"a.bin\n" {
		t.Fatalf("unexpected publication output: %q", out)
	}
	after := ref()
	parent, err := repo.ResolveCommit(after.String() + "^")
	if err != nil || parent != before {
		t.Fatalf("not one artifact commit: parent %s, %v", parent, err)
	}
	for name, data := range map[string]string{"a.bin": "\x00\xff\x01", "z result.json": "{}\n"} {
		entry, err := repo.Entry(after, "storage/EXAMPLE-1/"+name)
		if err != nil {
			t.Fatal(err)
		}
		if blob, err := repo.Blob(entry.ID); err != nil || string(blob) != data {
			t.Fatalf("%s: %q, %v", name, blob, err)
		}
	}
	if _, err := repo.Entry(after, staged.Path); !git.HasCode(err, git.NotFound) {
		t.Fatalf("unrelated edit made it into the commit: %v", err)
	}
	if entries, err := tasks.Repository.IndexEntries(); err != nil {
		t.Fatal(err)
	} else {
		found := false
		for _, entry := range entries {
			found = found || (entry.Path == staged.Path && entry.ID == staged.ID)
		}
		if !found {
			t.Fatal("lost unrelated staged entry")
		}
	}
	if got, err := os.ReadFile(ticket); err != nil || string(got) != modified {
		t.Fatalf("lost unrelated unstaged changes: %q, %v", got, err)
	}
	if out := run(t, root, "store", "1"); out != listing {
		t.Fatalf("unexpected listing: %q", out)
	}
	if out := run(t, root, "store", "2"); out != "" {
		t.Fatalf("another task listed these artifacts: %q", out)
	}
	if out := run(t, root, "list", "all"); !strings.Contains(out, "Benchmark") {
		t.Fatalf("storage disrupted task listing: %q", out)
	}

	// Collisions are checked for the entire batch before opening any inputs.
	out, stderr, err := invoke(checkout, "", "store", "1", "old/a.bin", "one/same", "two/same", "not-conflicting", "old/z result.json")
	if out != "" || err == nil || ref() != after {
		t.Fatalf("collision published something: stdout %q, stderr %q, error %v", out, stderr, err)
	}
	for _, want := range []string{`"old/a.bin"`, `"one/same"`, `"two/same"`, `"old/z result.json"`, "../../" + prefix + "a.bin\n", "../../" + prefix + "z result.json\n"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in stderr: %q", want, stderr)
		}
	}
	if strings.Contains(stderr, "not-conflicting") {
		t.Errorf("reported a non-colliding input: %q", stderr)
	}
	if out, _, err := invoke(root, "", "store", "2", second, "missing-input"); out != "" || err == nil || ref() != after {
		t.Fatalf("unreadable input did not abort the batch: %q, %v", out, err)
	}
}

func TestStoreArtifactsRefusesObstructions(t *testing.T) {
	t.Parallel()
	repo, root := fixture(t)
	ticket := strings.TrimSpace(run(t, root, "create", "Benchmark", "--body", "Evidence"))
	const input = "new.json"
	writeSafetyFile(t, filepath.Join(root, input), "{}\n")
	storage := filepath.Join(filepath.Dir(ticket), "storage")
	if err := os.Symlink(t.TempDir(), storage); err != nil {
		t.Fatal(err)
	}
	before, err := repo.Reference(store.Branch)
	if err != nil {
		t.Fatal(err)
	}
	fails(t, root, "artifact directory is obstructed", "store", "1", input)
	after, err := repo.Reference(store.Branch)
	if err != nil || after != before {
		t.Fatalf("task branch moved: %v, %v", after, err)
	}
}
