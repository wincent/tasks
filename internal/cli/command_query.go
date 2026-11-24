package cli

import (
	"context"
	"time"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/query"
	"github.com/wincent/tasks/internal/store"
)

func queryHelp(example string) string {
	return `Queries use recorded status (status:merged), a case-insensitive title
substring (title:"export report"), or the last committed ticket edit
(updated:<30.days.ago). Combine predicates with NOT, AND, OR (in that order
of precedence) and parentheses. Use quotes around the whole expression
at the shell. Dates use UTC: YYYY, YYYY-MM, YYYY-MM-DD, today, or yesterday
match the whole period. < and >= compare to its start; <= and > compare
to its end. Relative values accept minutes, hours, days, weeks, and 30-day
months (for example, 30 days ago or 30.days.ago). Comparators use the
exact cutoff; without one, match its UTC day, hour, or minute.

Example:
  ` + example + "\n\n"
}

type queryMatcher struct {
	expr    *query.Expression
	tasks   *store.Store
	head    git.ID
	history *store.TaskHistory
}

func (m *queryMatcher) match(ctx context.Context, task store.Task) (bool, store.TaskUpdate, error) {
	var update store.TaskUpdate
	loaded := false
	match, err := m.expr.Evaluate(query.Record{Status: task.Status, Title: task.Title}, func() (time.Time, error) {
		if loaded {
			return update.Time, nil
		}
		if m.history == nil {
			if m.head == (git.ID{}) {
				head, err := m.tasks.Head()
				if err != nil {
					return time.Time{}, err
				}
				m.head = head.ID
			}
			m.history = m.tasks.TaskHistory(m.head)
		}
		var err error
		update, err = m.history.LastUpdate(ctx, task.Number)
		loaded = err == nil
		return update.Time, err
	})
	return match, update, err
}
