package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/testutil"
)

func TestQueryPreservesCommandFilters(t *testing.T) {
	repo, root := fixture(t)
	run(t, root, "init")
	metadata := filepath.Join(root, "tasks")
	commitTaskText(t, repo, filepath.Join(metadata, "EXAMPLE-1 Export Report.md"), "Status: merged\n")
	commitTaskText(t, repo, filepath.Join(metadata, "EXAMPLE-2 Export draft.md"), "Status: open\n")
	commitTaskText(t, repo, filepath.Join(metadata, "EXAMPLE-3 Literal STATUS:MERGED.md"), "Status: open\n")
	run(t, root, "archive", "1")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"list", "-q", `status:merged OR title:draft`}, "EXAMPLE-2\topen\tExport draft\n"},
		{[]string{"list", "--include-archived", "-q", "status:merged"}, ""},
		{[]string{"list", "all", "-q", "status:merged"}, ""},
		{[]string{"list", "all", "--include-archived", "-q", `status:merged AND title:"export report"`}, "EXAMPLE-1\tmerged\tExport Report (archived)\n"},
		{[]string{"list", "--status", "open", "--include-archived", "-q", "status:merged"}, ""},
		{[]string{"search", "export", "--status", "open", "--query", "NOT status:open"}, ""},
		{[]string{"search", "report", "--query", "status:merged"}, ""},
		{[]string{"search", "report", "--query", "title:export"}, ""},
		{[]string{"search", "draft", "--query", "title:export"}, "EXAMPLE-2\topen\tExport draft\n"},
		{[]string{"search", "status:merged"}, "EXAMPLE-3\topen\tLiteral STATUS:MERGED\n"},
		{[]string{"search", "status:merged", "--query", "title:export"}, ""},
	} {
		if got := run(t, root, tc.args...); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.args, got, tc.want)
		}
	}
	fails(t, root, "mutually exclusive", "archive", "1", "--query", "status:merged")
	fails(t, root, "may only be supplied once", "archive", "-q", "title:x", "--query", "title:y")
	fails(t, root, "query at byte 1", "list", "--query=")
	fails(t, root, "literal title pattern", "search", "--query", "status:merged")
}

func TestQueryUpdatedUsesCommittedRecordHistory(t *testing.T) {
	_, root := fixture(t)
	metadata := filepath.Join(root, "tasks")
	run(t, root, "init")
	first, second := "EXAMPLE-1 Export draft.md", "EXAMPLE-1 Export report.md"
	commit := func(date, message string) {
		t.Helper()
		t.Setenv("GIT_COMMITTER_DATE", date+"T22:00:00-04:00") // next UTC day
		t.Setenv("GIT_AUTHOR_DATE", "2020-01-01T00:00:00Z")
		systemGit(t, metadata, "add", "-A")
		systemGit(t, metadata, "commit", "-m", message)
	}
	wantDay := func(day string) {
		t.Helper()
		if got := run(t, root, "list", "all", "--include-archived", "-q", "updated:"+day); !strings.Contains(got, "EXAMPLE-1\t") {
			t.Fatalf("not updated on %s: %q", day, got)
		}
	}
	writeSafetyFile(t, filepath.Join(metadata, first), "Status: open\n")
	commit("2024-01-01", "create")
	wantDay("2024-01-02")
	if err := os.Rename(filepath.Join(metadata, first), filepath.Join(metadata, second)); err != nil {
		t.Fatal(err)
	}
	commit("2024-02-01", "title only")
	wantDay("2024-02-02")
	writeSafetyFile(t, filepath.Join(metadata, second), "Status: open\n\nCommitted edit\n")
	commit("2024-03-01", "contents only")
	wantDay("2024-03-02")
	writeSafetyFile(t, filepath.Join(metadata, second), "Status: open\n\nLocal edit\n")
	wantDay("2024-03-02")
	systemGit(t, metadata, "restore", second)
	t.Setenv("GIT_COMMITTER_DATE", "2024-04-01T00:00:00Z")
	run(t, root, "archive", "1")
	wantDay("2024-03-02")
	systemGit(t, root, "worktree", "remove", metadata)
	wantDay("2024-03-02")
	run(t, root, "unarchive", "1")
	wantDay("2024-03-02")
	if got := run(t, root, "search", "export", "--query", "updated:2024-04"); got != "" {
		t.Fatalf("an archive move counted as an update: %q", got)
	}

	writeSafetyFile(t, filepath.Join(metadata, "EXAMPLE-2 Uncommitted.md"), "Status: open\n")
	if got := run(t, root, "search", "uncommitted", "--query", "status:open"); !strings.Contains(got, "EXAMPLE-2\t") {
		t.Fatalf("non-history query lost uncommitted ticket: %q", got)
	}
	for _, tc := range []struct {
		query string
		match bool
	}{
		{"title:uncommitted OR updated:today", true},
		{"title:draft AND updated:today", false},
	} {
		out := run(t, root, "search", "uncommitted", "--query", tc.query)
		if strings.Contains(out, "EXAMPLE-2\t") != tc.match {
			t.Fatalf("%q: unexpected result %q", tc.query, out)
		}
	}
	for _, expr := range []string{
		"title:draft OR updated:today",
		"title:uncommitted AND updated:today",
		"updated:today OR title:uncommitted",
	} {
		fails(t, root, "EXAMPLE-2: updated history unavailable", "search", "uncommitted", "--query", expr)
	}
	// A short-circuited history predicate does not turn an uncommitted
	// candidate into a valid archive source.
	fails(t, root, "cannot preview an uncommitted or missing archive source", "archive", "-q", "title:uncommitted OR (title:absent AND updated:today)")
}

type archivePromptOutput struct {
	bytes.Buffer
	fd     uintptr
	prompt func()
}

func (out *archivePromptOutput) Fd() uintptr { return out.fd }
func (out *archivePromptOutput) Write(p []byte) (int, error) {
	n, err := out.Buffer.Write(p)
	if strings.Contains(string(p), "Type yes to confirm") {
		out.prompt()
	}
	return n, err
}

func archiveAtPrompt(t *testing.T, root string, onPrompt func(context.CancelFunc, io.Writer), args ...string) (string, error) {
	t.Helper()
	master, slave, err := testutil.OpenPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := &archivePromptOutput{fd: slave.Fd()}
	out.prompt = func() { onPrompt(cancel, master) }
	err = Run(ctx, root, args, IO{In: slave, Out: out})
	return out.String(), err
}

func TestQueryArchivePreviewMatchesList(t *testing.T) {
	repo, root := fixture(t)
	run(t, root, "init")
	metadata := filepath.Join(root, "tasks")
	first := filepath.Join(metadata, "EXAMPLE-1 First task.md")
	commitTaskText(t, repo, first, "Status: active\n")
	commitTaskText(t, repo, filepath.Join(metadata, "EXAMPLE-10 Second task.md"), "Status: done\n")
	writeSafetyFile(t, first, "Status: active\n\nLocal edit\n")

	// The recorded status matches the query even though the stopped task
	// appears as blocked. The preview also shows the local edit marker.
	const query = "status:active OR status:done"
	for _, tc := range []struct{ name, term, noColor string }{
		{"styled", "xterm", ""},
		{"no color", "xterm", "1"},
		{"dumb terminal", "dumb", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TERM", tc.term)
			t.Setenv("NO_COLOR", tc.noColor)
			rows, diagnostic, err := outputTerminal(t, root, "list", "all", "-q", query)
			if err != nil || diagnostic != "" || !strings.Contains(rows, "blocked") || !strings.Contains(rows, "EXAMPLE-10") {
				t.Fatalf("list = %q, %q, %v", rows, diagnostic, err)
			}
			out, diagnostic, err := outputTerminal(t, root, "archive", "-q", query)
			want := "Tasks to archive (2):\n" + rows + "No changes made; confirmation requires a terminal.\n"
			if err != nil || diagnostic != "" || out != want {
				t.Fatalf("archive = %q, %q, %v; want %q", out, diagnostic, err, want)
			}
		})
	}
}

func TestQueryArchivePreviewAndApply(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, mode := range []string{"noninteractive", "empty", "decline", "cancel", "new match", "changed", "dirty", "busy", "incomplete", "missing", "archived"} {
		t.Run(mode, func(t *testing.T) {
			repo, root := fixture(t)
			run(t, root, "init")
			metadata := filepath.Join(root, "tasks")
			names := []string{"EXAMPLE-1 First.md", "EXAMPLE-2 Second.md", "EXAMPLE-3 Already archived.md"}
			for _, name := range names {
				commitTaskText(t, repo, filepath.Join(metadata, name), "Status: merged\n")
			}
			run(t, root, "archive", "3")
			readHead := func() string { return systemGit(t, root, "rev-parse", "tasks") }
			before := readHead()
			var out string
			var err error
			checkPreview := true
			switch mode {
			case "noninteractive":
				systemGit(t, root, "worktree", "remove", metadata)
				out, _, err = invoke(root, "yes\n", "archive", "--query", "status:merged")
				if _, err := os.Stat(metadata); !os.IsNotExist(err) {
					t.Fatalf("preview bootstrapped checkout: %v", err)
				}
			case "empty":
				checkPreview = false
				out = run(t, root, "archive", "--query", "status:open")
				if !strings.Contains(out, "No unarchived tasks match") {
					t.Fatal(out)
				}
			case "incomplete", "missing":
				checkPreview = false
				path := filepath.Join(metadata, names[1])
				if mode == "incomplete" {
					writeSafetyFile(t, path, "Status: invalid\n")
				} else if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				out, _, err = invoke(root, "yes\n", "archive", "-q", "status:merged")
				if err == nil || !strings.Contains(err.Error(), "incomplete archive selection") || out != "" {
					t.Fatalf("expected selection failure without preview: %q %v", out, err)
				}
				err = nil
			default:
				if mode == "new match" {
					systemGit(t, root, "worktree", "remove", metadata)
				}
				out, err = archiveAtPrompt(t, root, func(cancel context.CancelFunc, input io.Writer) {
					answer := "yes\n"
					switch mode {
					case "decline":
						answer = "no\n"
					case "cancel":
						time.AfterFunc(20*time.Millisecond, cancel)
						return
					case "new match":
						run(t, root, "init")
						commitTaskText(t, repo, filepath.Join(metadata, "EXAMPLE-4 New match.md"), "Status: merged\n")
					case "changed":
						commitTaskText(t, repo, filepath.Join(metadata, names[1]), "Status: merged\n\nChanged\n")
					case "dirty":
						writeSafetyFile(t, filepath.Join(metadata, names[1]), "Status: merged\n\nDirty\n")
					case "busy":
						directory, err := operationState(repo)
						if err != nil {
							t.Fatal(err)
						}
						lock, err := taskLock(directory, "EXAMPLE", 2)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { lock.Close() })
					case "archived":
						run(t, root, "archive", "2")
					}
					before = readHead()
					if _, err := io.WriteString(input, answer); err != nil {
						t.Fatal(err)
					}
				}, "archive", "-q", "status:merged")
			}
			if checkPreview {
				rows := "EXAMPLE-1  merged  First\nEXAMPLE-2  merged  Second\n"
				if mode == "noninteractive" {
					rows = "EXAMPLE-1\tmerged\tFirst\nEXAMPLE-2\tmerged\tSecond\n"
				}
				want := "Tasks to archive (2):\n" + rows
				if mode == "noninteractive" {
					want += "No changes made; confirmation requires a terminal.\n"
				} else {
					want += "Archive these tasks? Type yes to confirm: "
				}
				if !strings.HasPrefix(out, want) || strings.Contains(out, "EXAMPLE-3") || strings.Contains(out, "EXAMPLE-4") {
					t.Fatalf("bad preview: %q %v; want prefix %q", out, err, want)
				}
			}
			switch mode {
			case "changed", "dirty", "busy", "archived":
				want := map[string]string{"changed": "changed since archive selection", "dirty": "changed since archive selection", "busy": "running agent or task operation", "archived": "is archived"}[mode]
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("expected %q: %q %v", want, out, err)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("expected context cancellation: %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "new match" {
				if got := systemGit(t, root, "show", "-s", "--format=%P", store.Branch); got != before {
					t.Fatalf("expected one archive commit after %s: %s", before, got)
				}
				if got := run(t, root, "list", "all", "-q", "status:merged"); got != "EXAMPLE-4\tmerged\tNew match\n" {
					t.Fatalf("changed preview set: %q", got)
				}
				if got := systemGit(t, metadata, "status", "--porcelain"); got != "" {
					t.Fatalf("dirty metadata after batch: %q", got)
				}
			} else if got := readHead(); got != before {
				t.Fatalf("archive changed metadata: %s != %s", got, before)
			}
		})
	}
}

func TestQueryArchiveProspectiveBatch(t *testing.T) {
	_, root := fixture(t)
	metadata := filepath.Join(root, "tasks")
	for _, title := range []string{"One", "Two"} {
		path := strings.TrimSpace(run(t, root, "create", title, "--body", "Details"))
		writeSafetyFile(t, path, "Status: merged\n")
	}
	t.Setenv("GIT_COMMITTER_DATE", "2024-03-01T00:00:00Z")
	systemGit(t, metadata, "add", "-A")
	systemGit(t, metadata, "commit", "-m", "mark merged")
	systemGit(t, root, "worktree", "remove", metadata)
	out, err := archiveAtPrompt(t, root, func(_ context.CancelFunc, input io.Writer) {
		if _, err := io.WriteString(input, "yes\n"); err != nil {
			t.Fatal(err)
		}
	}, "archive", "-q", "status:merged AND updated:2024-03")
	if err != nil || !strings.Contains(out, "Archived 2 task(s)") {
		t.Fatalf("%q %v", out, err)
	}
	if got := run(t, root, "list", "all", "--include-archived", "-q", "status:merged"); strings.Count(got, "(archived)") != 2 {
		t.Fatalf("missing archived tasks: %q", got)
	}
}

func TestQueryUpdatedReportsShallowHistory(t *testing.T) {
	repo, root := fixture(t)
	metadata := filepath.Join(root, "tasks")
	run(t, root, "create", "Target", "--body", "Details")
	systemGit(t, root, "worktree", "remove", metadata)
	// In a depth-one clone the boundary still records a parent, but the
	// parent object is missing. Create just such a boundary in the fixture.
	head, err := repo.Reference(store.Branch)
	if err != nil {
		t.Fatal(err)
	}
	info, err := repo.CommitInfo(head.ID)
	if err != nil || len(info.Parents) != 1 {
		t.Fatalf("expected a parent: %+v %v", info, err)
	}
	parent := info.Parents[0].String()
	writeSafetyFile(t, filepath.Join(root, ".git", "shallow"), head.ID.String()+"\n")
	if err := os.Remove(filepath.Join(root, ".git", "objects", parent[:2], parent[2:])); err != nil {
		t.Fatal(err)
	}
	if got := run(t, root, "list", "-q", "status:open"); !strings.Contains(got, "EXAMPLE-1\t") {
		t.Fatalf("non-history query needed a missing ancestor: %q", got)
	}
	fails(t, root, "EXAMPLE-1: updated history unavailable", "list", "-q", "updated:today")
	fails(t, root, "EXAMPLE-1: updated history unavailable", "archive", "-q", "updated:today")
}
