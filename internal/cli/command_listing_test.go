package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
)

func TestListingPartialResultsAndStatusWarnings(t *testing.T) {
	repo, root := fixture(t)
	path := strings.TrimSpace(run(t, root, "create", "Matching open", "--body", "requirements"))
	run(t, root, "create", "Matching active", "--body", "requirements")
	paths, _ := repo.Paths()
	live, err := state.TryLock(runLockPath(stateDirectory(paths), 2))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	conflicted := strings.TrimSpace(run(t, root, "create", "Unrelated conflict", "--body", "requirements"))
	conflict := "<<<<<<< HEAD\nStatus: open\n=======\nStatus: blocked\n>>>>>>> incoming\n"
	commitTaskText(t, repo, conflicted, conflict)
	broken := filepath.Join(filepath.Dir(path), "EXAMPLE-4 Unrelated malformed.md")
	writeSafetyFile(t, broken, "Status: invalid\n")
	tasks, err := store.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer tasks.Close()
	head, _ := tasks.Repository.Head()
	index := uiBytes(t, filepath.Join(tasks.Paths.GitDir, "index"))
	contents := map[string][]byte{}
	entries, err := os.ReadDir(tasks.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != ".git" {
			name := filepath.Join(tasks.Path, entry.Name())
			contents[name] = uiBytes(t, name)
		}
	}
	for _, args := range [][]string{{"list"}, {"list", "--json"}, {"list", "active", "--verbose"}, {"search", "matching"}, {"search", "matching", "--json"}, nil, {"status"}, {"status", "--json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, diagnostic, err := invoke(root, "", args...)
			status := len(args) > 0 && args[0] == "status"
			if err == nil || ExitCode(err) != 1 {
				t.Fatalf("missing incomplete-result failure: %q %v", out, err)
			}
			diagnostic += err.Error()
			for _, want := range []string{conflicted, "unresolved merge conflict", broken, "expected exactly one Status:"} {
				if !strings.Contains(diagnostic, want) {
					t.Fatalf("missing diagnostic %q: %q", want, diagnostic)
				}
			}
			activeOnly := status || (len(args) > 1 && args[1] == "active")
			want := []int{1, 2}
			if activeOnly {
				want = []int{2}
			}
			if len(args) > 0 && args[len(args)-1] == "--json" {
				var selected []store.Task
				if status {
					var snapshot struct {
						Tasks []store.Task `json:"tasks"`
					}
					if err := json.Unmarshal([]byte(out), &snapshot); err != nil {
						t.Fatalf("%s %v", out, err)
					}
					selected = snapshot.Tasks
				} else if err := json.Unmarshal([]byte(out), &selected); err != nil {
					t.Fatalf("invalid partial JSON: %s %v", out, err)
				}
				numbers := []int{}
				for _, task := range selected {
					numbers = append(numbers, task.Number)
				}
				if !reflect.DeepEqual(numbers, want) {
					t.Fatalf("%v != %v", numbers, want)
				}
			} else {
				for _, number := range want {
					if !strings.Contains(out, []string{"", "EXAMPLE-1\topen\tMatching open", "EXAMPLE-2\tactive\tMatching active"}[number]) && !(status && strings.Contains(out, "EXAMPLE-2  active  Matching active")) {
						t.Fatal(out)
					}
				}
				if strings.Contains(out, "EXAMPLE-3\t") || strings.Contains(out, "EXAMPLE-4\t") {
					t.Fatalf("invalid records displayed: %s", out)
				}
			}
			if len(args) == 0 && (!strings.HasPrefix(out, "Recent tasks:\n") || !strings.HasSuffix(out, run(t, root, "--help"))) {
				t.Fatal(out)
			}
		})
	}
	for _, args := range [][]string{{"list", "closed"}, {"search", "no match"}, {"search", "no match", "--json"}} {
		out, diagnostic, err := invoke(root, "", args...)
		if err == nil || diagnostic != "" || (out != "" && out != "[]\n") {
			t.Fatalf("incomplete scan claimed no matches: %q %q %v", out, diagnostic, err)
		}
	}
	if got := run(t, root, "show", "3", "--raw"); got != conflict {
		t.Fatalf("diagnostic read unavailable: %q", got)
	}
	fails(t, root, "unresolved merge conflict", "check")
	fails(t, root, "unresolved merge conflict", "close", "3", "--body", "never")
	fails(t, root, "unresolved merge conflict", "close", "3", "--body", "must not change status")
	after, _ := tasks.Repository.Head()
	if after != head || !bytes.Equal(index, uiBytes(t, filepath.Join(tasks.Paths.GitDir, "index"))) {
		t.Fatal("queries or rejected mutations changed task HEAD/index")
	}
	for name, before := range contents {
		if !bytes.Equal(before, uiBytes(t, name)) {
			t.Fatalf("changed %s", name)
		}
	}
	// Queries remain available during shared-store synchronization, but a
	// mutation must still refuse the in-progress Git operation.
	marker := filepath.Join(tasks.Paths.GitDir, "MERGE_HEAD")
	writeSafetyFile(t, marker, head.ID.String()+"\n")
	out, _, err := invoke(root, "", "list", "--json")
	if err == nil || !strings.Contains(out, `"number": 1`) || !strings.Contains(out, `"number": 2`) {
		t.Fatalf("merge hid unrelated records: %s %v", out, err)
	}
	fails(t, root, "Git operation in progress", "comment", "1", "--body", "never")
	if string(uiBytes(t, marker)) != head.ID.String()+"\n" {
		t.Fatal("query or rejected mutation changed merge state")
	}
}

func TestListingOnlyInvalidRecordsDoesNotClaimEmptyStore(t *testing.T) {
	_, root := fixture(t)
	path := strings.TrimSpace(run(t, root, "create", "Broken", "--body", "requirements"))
	writeSafetyFile(t, path, "Status: invalid\n")
	for _, args := range [][]string{nil, {"list"}, {"search", "broken"}, {"list", "--json"}, {"search", "broken", "--json"}} {
		out, diagnostic, err := invoke(root, "", args...)
		if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), path) || diagnostic != "" {
			t.Fatalf("%v: %q %q %v", args, out, diagnostic, err)
		}
		for _, incorrect := range []string{"No tasks yet", "no uncompleted", "No tasks match"} {
			if strings.Contains(out, incorrect) {
				t.Fatalf("incomplete scan claimed empty store: %q", out)
			}
		}
		if len(args) > 1 && args[len(args)-1] == "--json" && out != "[]\n" {
			t.Fatalf("partial JSON must remain an empty array: %q", out)
		}
	}
}

func TestListingDuplicatesExcludeEveryCopy(t *testing.T) {
	_, root := fixture(t)
	path := strings.TrimSpace(run(t, root, "create", "Original", "--body", "requirements"))
	run(t, root, "create", "Unrelated", "--body", "requirements")
	writeSafetyFile(t, filepath.Join(filepath.Dir(path), "EXAMPLE-1 A duplicate.md"), "Status: active\n")
	writeSafetyFile(t, filepath.Join(filepath.Dir(path), "EXAMPLE-1 Z duplicate.md"), "Status: invalid\n")
	for _, args := range [][]string{{"list", "all", "--json"}, {"search", "original", "--json"}, {"status", "--json"}} {
		out, _, err := invoke(root, "", args...)
		if err == nil || !strings.Contains(err.Error(), "duplicate EXAMPLE-1") {
			t.Fatal(err)
		}
		if strings.Contains(out, `"number": 1`) || (args[0] == "list" && !strings.Contains(out, `"number": 2`)) {
			t.Fatalf("ambiguous task was authoritative or unrelated task lost: %s", out)
		}
	}
	fails(t, root, "found 3", "show", "1", "--raw")
	fails(t, root, "found 3", "comment", "1", "--body", "must not edit")
}

func TestListingProcessPartialJSONAndExitStatus(t *testing.T) {
	_, root := fixture(t)
	path := strings.TrimSpace(run(t, root, "create", "Matching", "--body", "requirements"))
	writeSafetyFile(t, filepath.Join(filepath.Dir(path), "EXAMPLE-2 Broken.md"), "Status: invalid\n")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"list", "--json"}, {"search", "matching", "--json"}, nil} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "TASK_TEST_COMMAND_HELPER=task")
		var out, diagnostic bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &diagnostic
		err := cmd.Run()
		cancel()
		if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 || !strings.Contains(diagnostic.String(), "incomplete task results") || !strings.Contains(diagnostic.String(), "EXAMPLE-2 Broken.md") {
			t.Fatalf("%v: %q %q %v", args, out.String(), diagnostic.String(), err)
		}
		if len(args) > 0 {
			var selected []store.Task
			if err := json.Unmarshal(out.Bytes(), &selected); err != nil || len(selected) != 1 || selected[0].Number != 1 {
				t.Fatalf("stdout JSON corrupted: %s %v", out.String(), err)
			}
		} else if !strings.Contains(out.String(), "EXAMPLE-1\topen\tMatching") {
			t.Fatal(out.String())
		}
	}
}
