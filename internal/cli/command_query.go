package cli

import (
	"context"

	"github.com/wincent/tasks/internal/query"
	"github.com/wincent/tasks/internal/store"
)

const queryHelp = `Queries use recorded status (status:merged), a case-insensitive title
substring (title:"export report"), or the last committed ticket edit
(updated:<30.days.ago). Combine predicates with NOT, AND, OR (in that order
of precedence) and parentheses. Use quotes around the whole expression
at the shell. Dates use UTC: YYYY, YYYY-MM, YYYY-MM-DD, today, or yesterday
match the whole period. < and >= compare to its start; <= and > compare
to its end. Relative values accept minutes, hours, days, weeks, and 30-day
months (for example, 30.days.ago or "30 days ago"). Comparators use the
exact cutoff; without one, match its UTC day, hour, or minute.

`

type queryMatcher struct {
	expr    *query.Expression
	history *store.TaskHistory
}

func (m queryMatcher) match(ctx context.Context, task store.Task) (bool, store.TaskUpdate, error) {
	var update store.TaskUpdate
	if m.history != nil {
		var err error
		update, err = m.history.LastUpdate(ctx, task.Number)
		if err != nil {
			return false, update, err
		}
	}
	return m.expr.Matches(query.Record{Status: task.Status, Title: task.Title, Updated: update.Time}), update, nil
}
