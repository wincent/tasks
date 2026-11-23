package store

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/wincent/tasks/internal/git"
)

// ScanArchiveCandidates requires a complete working set. Scan alone cannot
// distinguish missing checked-out files from tickets that never existed.
func (store *Store) ScanArchiveCandidates(head git.ID) ([]Task, error) {
	tasks, err := store.Scan(false)
	if err != nil || store.Prospective() {
		return tasks, err
	}
	present := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		present[store.RelativePath(task)] = true
	}
	entries, err := store.Repository.Tree(head)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if store.matchTicket(entry.Name) != nil && !present[entry.Name] {
			return nil, fmt.Errorf("%s is missing from the shared checkout; restore it before retrying", entry.Name)
		}
	}
	return tasks, nil
}

// ArchivePlan is an in-memory snapshot of only the tickets shown to the user.
// Its entries retain both the working record and the committed source identity.
type ArchivePlan struct {
	prefix  Prefix
	tickets []archiveTicket
}

type archiveTicket struct {
	task   Task
	entry  git.TreeEntry
	update TaskUpdate
}

func (store *Store) PlanArchive(head git.ID, selected []Task, updates map[int]TaskUpdate) (ArchivePlan, error) {
	plan := ArchivePlan{prefix: store.Prefix}
	for _, task := range selected {
		entry, err := store.Repository.Entry(head, store.RelativePath(task))
		if err != nil {
			return ArchivePlan{}, fmt.Errorf("%s: cannot preview an uncommitted or missing archive source: %w", task.ID(), err)
		}
		current, err := store.ReadWorking(task.Number)
		if err != nil {
			return ArchivePlan{}, err
		}
		// Prospective scans read committed blobs, while ReadWorking provides
		// the smudged bytes that will be installed in a bootstrapped checkout.
		if current.Path != task.Path || (!store.Prospective() && current.Content != task.Content) {
			return ArchivePlan{}, archiveChanged(task)
		}
		plan.tickets = append(plan.tickets, archiveTicket{current, entry, updates[task.Number]})
	}
	current, err := store.Head()
	if err != nil {
		return ArchivePlan{}, err
	}
	if current.ID != head || current.Branch != Branch {
		return ArchivePlan{}, errors.New("tasks changed during archive preview; retry")
	}
	return plan, nil
}

func archiveChanged(task Task) error {
	return fmt.Errorf("%s changed since archive selection; retry", task.ID())
}

// ArchiveTasks requires the task-lifetime locks for the whole previewed set,
// acquired before the session lock. It revalidates the set without expanding
// it and uses a single metadata transaction for all moves.
func (s *Session) ArchiveTasks(plan ArchivePlan) ([]Task, error) {
	if len(plan.tickets) == 0 {
		return nil, nil
	}
	tasks, err := s.Preview(plan.prefix)
	if err != nil {
		return nil, err
	}
	defer tasks.Close()
	head, err := tasks.Head()
	if err != nil {
		return nil, err
	}
	var history *TaskHistory
	selected := make([]Task, 0, len(plan.tickets))
	for _, ticket := range plan.tickets {
		task, err := tasks.ReadWorking(ticket.task.Number)
		if err != nil {
			return nil, err
		}
		if task.Path != ticket.task.Path || task.Content != ticket.task.Content {
			return nil, archiveChanged(ticket.task)
		}
		entry, err := tasks.Repository.Entry(head.ID, filepath.Base(task.Path))
		if err != nil {
			return nil, err
		}
		if entry != ticket.entry {
			return nil, archiveChanged(ticket.task)
		}
		if ticket.update != (TaskUpdate{}) {
			if history == nil {
				history = tasks.TaskHistory(head.ID)
			}
			update, err := history.LastUpdate(s.ctx, task.Number)
			if err != nil {
				return nil, err
			}
			if update != ticket.update {
				return nil, archiveChanged(ticket.task)
			}
		}
		selected = append(selected, task)
	}
	return s.moveTasks(tasks, head.ID, selected, true, fmt.Sprintf("chore: archive %d selected tasks", len(selected)))
}
