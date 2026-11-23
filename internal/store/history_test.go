package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/git"
)

type taskHistoryFixture struct {
	t testing.TB
	*Store
}

func newTaskHistoryFixture(t testing.TB) taskHistoryFixture {
	t.Helper()
	repo, err := git.Init(t.TempDir(), "", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repo.Close)
	return taskHistoryFixture{t, &Store{Repository: repo, Prefix: "EXAMPLE"}}
}

func (f taskHistoryFixture) blob(text string) git.ID {
	f.t.Helper()
	id, err := f.Repository.WriteBlob([]byte(text))
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f taskHistoryFixture) tree(entries ...git.TreeEntry) git.ID {
	f.t.Helper()
	id, err := f.Repository.WriteTree(entries)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f taskHistoryFixture) commit(tree git.ID, age int, parents ...git.ID) git.ID {
	f.t.Helper()
	who := signature
	who.When = who.When.Add(time.Duration(age) * time.Second)
	id, err := f.Repository.WriteCommit(tree, parents, signature, who, "ticket history\n")
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f taskHistoryFixture) archive(bucket string, entries ...git.TreeEntry) git.TreeEntry {
	return git.TreeEntry{Name: "archive", Mode: 0040000, ID: f.tree(
		git.TreeEntry{Name: bucket, Mode: 0040000, ID: f.tree(entries...)},
	)}
}

func (f taskHistoryFixture) check(h *TaskHistory, number int, revision git.ID, age int) {
	f.t.Helper()
	want := TaskUpdate{revision, signature.When.Add(time.Duration(age) * time.Second)}
	if got, err := h.LastUpdate(context.Background(), number); err != nil || got != want {
		f.t.Fatalf("ticket %d: got %+v, %v; want %+v", number, got, err, want)
	}
}

func TestTaskHistoryRecordIdentity(t *testing.T) {
	f := newTaskHistoryFixture(t)
	entry := git.TreeEntry{Name: "EXAMPLE-1 First.md", ID: f.blob("record"), Mode: 0100644}
	created := f.commit(f.tree(entry), 0)
	entry.Name = "EXAMPLE-1 Renamed.md"
	renamed := f.commit(f.tree(entry), 1, created)
	entry.ID = f.blob("edited")
	edited := f.commit(f.tree(entry), 2, renamed)
	archived := f.commit(f.tree(f.archive("0", entry)), 3, edited)
	entry.Mode = 0100755
	mode := f.commit(f.tree(f.archive("0", entry)), 4, archived)
	// Same number in a noncanonical bucket is irrelevant.
	unarchived := f.commit(f.tree(entry, f.archive("1000", entry)), 5, mode)
	for _, tc := range []struct {
		head, update git.ID
		age          int
	}{{created, created, 0}, {renamed, renamed, 1}, {unarchived, edited, 2}} {
		f.check(f.TaskHistory(tc.head), 1, tc.update, tc.age)
	}
}

func TestTaskHistoryMergeParentOrder(t *testing.T) {
	f := newTaskHistoryFixture(t)
	first := git.TreeEntry{Name: "EXAMPLE-1 First.md", ID: f.blob("record"), Mode: 0100644}
	second := git.TreeEntry{Name: "EXAMPLE-2 Second.md", ID: first.ID, Mode: first.Mode}
	empty := f.commit(f.tree(), 0)
	leftTree := f.tree(first)
	left := f.commit(leftTree, 1, empty)
	right := f.commit(f.tree(f.archive("0", first, second)), 2, empty)
	merged := f.commit(f.tree(first, second), 3, left, right)
	h := f.TaskHistory(merged)
	f.check(h, 1, left, 1)  // Both match; parent order wins, not recency.
	f.check(h, 2, right, 2) // Only the second parent matches.

	second.ID = f.blob("merge edit")
	edited := f.commit(f.tree(first, second), 4, left, right)
	f.check(f.TaskHistory(edited), 2, edited, 4)
}

func TestTaskHistoryParentErrors(t *testing.T) {
	f := newTaskHistoryFixture(t)
	entry := git.TreeEntry{Name: "EXAMPLE-1 Record.md", ID: f.blob("record"), Mode: 0100644}
	other := git.TreeEntry{Name: "EXAMPLE-1001 Other.md", ID: entry.ID, Mode: entry.Mode}
	duplicate := entry
	duplicate.Name = "EXAMPLE-1 Duplicate.md"
	symlink := entry
	symlink.Mode = 0120000
	obstruction := git.TreeEntry{Name: "archive", ID: entry.ID, Mode: entry.Mode}
	badBucket := git.TreeEntry{Name: "archive", Mode: 0040000, ID: f.tree(
		git.TreeEntry{Name: "1000", Mode: 0100644, ID: entry.ID},
	)}
	current := f.tree(entry, other)
	for _, tc := range []struct {
		name string
		tree git.ID
		want string
	}{
		{"duplicate in root", f.tree(entry, duplicate, other), "ambiguous committed record"},
		{"duplicate across archive and root", f.tree(entry, other, f.archive("0", entry)), "ambiguous committed record"},
		{"nonregular record", f.tree(symlink, other), "not a regular committed record"},
		{"obstructed archive", f.tree(entry, other, obstruction), "archive directory is obstructed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := f.commit(tc.tree, 0)
			valid := f.commit(current, 1)
			// A bad parent before a match is still an error.
			head := f.commit(current, 2, parent, valid)
			if _, err := f.TaskHistory(head).LastUpdate(context.Background(), 1); err == nil ||
				!strings.Contains(err.Error(), "EXAMPLE-1: updated history unavailable") ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want history error containing %q", err, tc.want)
			}
			// A bad parent after a match is not inspected for this ticket.
			head = f.commit(current, 2, valid, parent)
			f.check(f.TaskHistory(head), 1, valid, 1)
		})
	}
	t.Run("unrelated obstructed bucket", func(t *testing.T) {
		parent := f.commit(f.tree(entry, other, badBucket), 0)
		h := f.TaskHistory(f.commit(current, 1, parent))
		f.check(h, 1, parent, 0)
		if _, err := h.LastUpdate(context.Background(), 1001); err == nil ||
			!strings.Contains(err.Error(), "expected a directory") {
			t.Fatalf("got %v, want obstructed bucket error", err)
		}
	})
	t.Run("absent record and cancellation", func(t *testing.T) {
		h := f.TaskHistory(f.commit(current, 0))
		if _, err := h.LastUpdate(context.Background(), 2); err == nil ||
			!strings.Contains(err.Error(), "no committed task record") {
			t.Fatalf("got %v, want missing record error", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := h.LastUpdate(ctx, 1); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context cancellation", err)
		}
	})
}

func TestTaskHistoryDenseFallback(t *testing.T) {
	f := newTaskHistoryFixture(t)
	var entries []git.TreeEntry
	blob := f.blob("record")
	for i := 1; i <= historyDeltaLimit+1; i++ {
		entries = append(entries, git.TreeEntry{Name: fmt.Sprintf("EXAMPLE-%d Record.md", i), ID: blob, Mode: 0100644})
	}
	base := f.commit(f.tree(entries...), 0)
	for i := range entries {
		entries[i].Mode = 0100755
	}
	mode := f.commit(f.tree(entries...), 1, base)
	h := f.TaskHistory(mode)
	for i := range entries {
		f.check(h, i+1, base, 0)
	}
	delta := h.deltas[historyEdge{mode, base}]
	if !delta.fallback || len(delta.changes) != 0 {
		t.Fatalf("dense edge should use bounded point lookups, got %+v", delta)
	}
}

func TestTaskHistoryPointLookupKeepsLiteralRecordRules(t *testing.T) {
	f := newTaskHistoryFixture(t)
	blob := f.blob("record")
	record := git.TreeEntry{Name: "EXAMPLE-1 Record.md", ID: blob, Mode: 0100644}
	malformed := git.TreeEntry{Name: "EXAMPLE-1 Line\nbreak.md", ID: blob, Mode: 0100644}
	dir := git.TreeEntry{Name: "EXAMPLE-1", ID: f.tree(), Mode: 0040000}
	other := git.TreeEntry{Name: "EXAMPLE-10 Other.md", ID: blob, Mode: 0100644}
	root := f.tree(record, malformed, dir, other)
	archived := f.tree(f.archive("0", record, malformed, dir, other))
	initial := f.commit(archived, 0)
	moved := f.commit(root, 1, initial)
	for _, tree := range []git.ID{root, archived} {
		got, err := f.Repository.HistoryRecord(tree, "archive/0", "EXAMPLE-1 ")
		if err != nil || got != record {
			t.Fatalf("lookup: %+v, %v", got, err)
		}
	}
	// A dense edge taking the fallback must still follow archive moves.
	var fillers []git.TreeEntry
	for i := 0; i <= historyDeltaLimit; i++ {
		fillers = append(fillers, git.TreeEntry{Name: fmt.Sprintf("file-%d", i), ID: blob, Mode: 0100644})
	}
	head := f.commit(f.tree(append(fillers, record)...), 2, moved)
	f.check(f.TaskHistory(head), 1, initial, 0)
}

// Sparse edits across a wide root, with one bucket of archived tickets. Half
// the commits edit the root and half edit the archive. Every edit is distinct
// so caching by root identity alone cannot fix this workload.
func wideTaskHistory(t testing.TB, tickets, commits int) (taskHistoryFixture, git.ID, []git.ID) {
	t.Helper()
	f := newTaskHistoryFixture(t)
	blob1, blob2 := f.blob("original"), f.blob("edited")
	root := make([]git.TreeEntry, tickets-999, tickets-998)
	bucket := make([]git.TreeEntry, 999) // Canonical bucket 0: tickets 1 through 999.
	for number := 1; number <= tickets; number++ {
		entry := git.TreeEntry{Name: fmt.Sprintf("EXAMPLE-%d Record.md", number), ID: blob1, Mode: 0100644}
		if number < 1000 {
			bucket[number-1] = entry
		} else {
			root[number-1000] = entry
		}
	}
	root = append(root, f.archive("0", bucket...))
	head := f.commit(f.tree(root...), 0)
	tips := make([]git.ID, tickets+1)
	for i := range tips {
		tips[i] = head
	}
	for i := 1; i <= commits; i++ {
		var number int
		if i%2 == 0 {
			number = i / 2
			bucket[number-1].ID = blob2
			root[len(root)-1] = f.archive("0", bucket...)
		} else {
			number = 1000 + i/2
			root[number-1000].ID = blob2
		}
		head = f.commit(f.tree(root...), i, head)
		tips[number] = head
	}
	return f, head, tips
}

func checkWideTaskHistory(t testing.TB, h *TaskHistory, tips []git.ID) {
	t.Helper()
	for number := 1; number < len(tips); number++ {
		update, err := h.LastUpdate(context.Background(), number)
		if err != nil || update.Commit != tips[number] {
			t.Fatalf("ticket %d: got %+v, %v; want commit %s", number, update, err, tips[number])
		}
	}
}

func TestTaskHistoryScale(t *testing.T) {
	const tickets, commits = 1300, 80
	f, head, tips := wideTaskHistory(t, tickets, commits)
	h := f.TaskHistory(head)
	checkWideTaskHistory(t, h, tips)
	if len(h.deltas) != commits {
		t.Fatalf("got %d deltas, want %d", len(h.deltas), commits)
	}
	for edge, delta := range h.deltas {
		if len(delta.changes) != 1 || delta.fallback {
			t.Fatalf("edge %+v should retain one record change, got %+v", edge, delta)
		}
	}
}

func denseTaskHistory(t testing.TB) (taskHistoryFixture, git.ID, []git.ID) {
	t.Helper()
	const tickets, commits, changed = 300, 12, 150
	f := newTaskHistoryFixture(t)
	blob := f.blob("record")
	entries := make([]git.TreeEntry, tickets)
	for i := range entries {
		entries[i] = git.TreeEntry{Name: fmt.Sprintf("EXAMPLE-%d Record.md", i+1), ID: blob, Mode: 0100644}
	}
	head := f.commit(f.tree(entries...), 0)
	tips := make([]git.ID, tickets+1)
	for i := range tips {
		tips[i] = head
	}
	for i := 1; i <= commits; i++ {
		for j := 0; j < changed; j++ {
			// Each edge has 150 mode changes, with distinct snapshots.
			entries[(i*7+j)%tickets].Mode ^= 0111
		}
		head = f.commit(f.tree(entries...), i, head)
	}
	return f, head, tips
}

func BenchmarkTaskHistoryDense(b *testing.B) {
	f, head, tips := denseTaskHistory(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		checkWideTaskHistory(b, f.TaskHistory(head), tips)
	}
}

func BenchmarkTaskHistoryScale(b *testing.B) {
	f, head, tips := wideTaskHistory(b, 3000, 240)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		checkWideTaskHistory(b, f.TaskHistory(head), tips)
	}
}
