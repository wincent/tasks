package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
)

func TestSearchTitleSubstringsAndFilters(t *testing.T) {
	repo, root := fixture(t)
	run(t, root, "init")
	statuses := []string{"open", "active", "blocked", "done", "merged", "closed"}
	for i, status := range statuses {
		commitTaskText(t, repo, filepath.Join(root, "tasks", fmt.Sprintf("EXAMPLE-%d A FooBAR title %d.md", i+1, i+1)), "Status: "+status+"\n")
	}
	commitTaskText(t, repo, filepath.Join(root, "tasks", "EXAMPLE-7 Other title.md"), "Status: open\n\nfoobar only in the description\n")
	commitTaskText(t, repo, filepath.Join(root, "tasks", "EXAMPLE-8 Literal [x].* -dash 雪 ÄPFEL.md"), "Status: open\n")
	runLock, err := state.TryLock(runLockPath(filepath.Join(root, ".git", "tasks"), 2))
	if err != nil {
		t.Fatal(err)
	}
	defer runLock.Close()
	for _, query := range []string{"fooBAR", "FOOBAR", "foobar title"} {
		var entries []store.Task
		if err := json.Unmarshal([]byte(run(t, root, "search", query, "--json")), &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != 6 {
			t.Fatalf("%q: %+v", query, entries)
		}
		for i, entry := range entries {
			if entry.Number != i+1 || entry.Status != statuses[i] {
				t.Fatalf("wrong order/status: %+v", entries)
			}
		}
	}
	for _, status := range statuses {
		got := run(t, root, "search", "foobar", "--status", status)
		want := run(t, root, "list", "--status", status)
		if status == "open" {
			want = strings.Split(want, "\n")[0] + "\n"
		}
		if got != want {
			t.Fatalf("%s: %q != %q", status, got, want)
		}
	}
	for _, query := range []string{"[x].*", "-dash", "雪", "äpfel"} {
		if got := run(t, root, "search", "--", query); got != "EXAMPLE-8\topen\tLiteral [x].* -dash 雪 ÄPFEL\n" {
			t.Fatalf("literal query %q: %q", query, got)
		}
	}
	for _, jsonOutput := range []bool{false, true} {
		args := []string{"search", "no such title"}
		want := ""
		if jsonOutput {
			args = append(args, "--json")
			want = "[]\n"
		}
		out, diagnostic, err := invoke(root, "", args...)
		if err != nil || out != want || (jsonOutput && diagnostic != "") || (!jsonOutput && !strings.Contains(diagnostic, "No tasks match")) {
			t.Fatalf("empty search: %q %q %v", out, diagnostic, err)
		}
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	linked, err := createCheckout(repo, "search-", head.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := run(t, linked.Paths.WorkDir, "search", "foobar", "--json"), run(t, root, "search", "foobar", "--json"); got != want {
		t.Fatalf("linked query differs: %s != %s", got, want)
	}
}

func TestSearchPreservesListMarkersAndOwnership(t *testing.T) {
	repo, root := fixture(t)
	path := strings.TrimSpace(run(t, root, "create", "Matching title", "--body", "description"))
	writeSafetyFile(t, path, string(uiBytes(t, path))+"\nManual change\n")
	writeSafetyFile(t, filepath.Join(filepath.Dir(path), "EXAMPLE-2 Another matching title.md"), "Status: open\n")
	tasks, err := store.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer tasks.Close()
	before, err := tasks.Repository.Head()
	if err != nil {
		t.Fatal(err)
	}
	for _, options := range [][]string{nil, {"--verbose"}, {"--json", "-v"}} {
		got := run(t, root, append([]string{"search", "matching"}, options...)...)
		want := run(t, root, append([]string{"list", "all"}, options...)...)
		if got != want {
			t.Fatalf("%v: %q != %q", options, got, want)
		}
	}
	after, err := tasks.Repository.Head()
	if err != nil || after != before {
		t.Fatalf("search changed metadata: %+v %v", after, err)
	}
	writeSafetyFile(t, filepath.Join(filepath.Dir(path), "EXAMPLE-3 Unrelated broken.md"), "Status: invalid\n")
	out, _, err := invoke(root, "", "search", "matching")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "EXAMPLE-3 Unrelated broken.md") || !strings.Contains(out, "EXAMPLE-1\topen") || !strings.Contains(out, "EXAMPLE-2\topen") {
		t.Fatalf("search must retain matches and report malformed nonmatching tasks: %q %v", out, err)
	}
}

func TestSearchArguments(t *testing.T) {
	for _, args := range [][]string{{"search"}, {"search", ""}, {"search", "one", "two"}, {"search", "query", "--status", "invalid"}, {"search", "query", "--raw"}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	for _, args := range [][]string{{"search", "-1"}, {"search", "--", "--literal"}, {"search", "two words", "--status", "all", "--json", "-v"}} {
		if _, err := parse(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	var out bytes.Buffer
	if err := help("search", &out); err != nil || !strings.Contains(out.String(), "Matches part of a title, ignoring case.") {
		t.Fatalf("search help: %v %s", err, out.String())
	}
}
