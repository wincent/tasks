package query

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type updated struct {
	comparator string
	start, end time.Time // [start, end); for an instant, both are the cutoff
	instant    bool
}

func (n updated) matches(r Record) bool {
	t := r.Updated
	switch n.comparator {
	case "<":
		return t.Before(n.start)
	case ">=":
		return !t.Before(n.start)
	case "<=":
		return t.Before(n.end) || (n.instant && t.Equal(n.end))
	case ">":
		if n.instant {
			return t.After(n.end)
		}
		return !t.Before(n.end)
	default:
		return !t.Before(n.start) && t.Before(n.end)
	}
}

func parseUpdated(value string, now time.Time) (updated, error) {
	value = strings.TrimSpace(value)
	var n updated
	for _, op := range []string{"<=", ">=", "<", ">"} {
		if rest, ok := strings.CutPrefix(value, op); ok {
			n.comparator, value = op, strings.TrimSpace(rest)
			break
		}
	}

	switch value {
	case "today", "yesterday":
		n.start = now.Truncate(24 * time.Hour)
		if value == "yesterday" {
			n.start = n.start.AddDate(0, 0, -1)
		}
		n.end = n.start.AddDate(0, 0, 1)
		return n, nil
	}

	for _, period := range []struct {
		layout              string
		years, months, days int
	}{
		{"2006", 1, 0, 0}, {"2006-01", 0, 1, 0}, {"2006-01-02", 0, 0, 1},
	} {
		if len(value) == len(period.layout) {
			if start, err := time.Parse(period.layout, value); err == nil {
				n.start, n.end = start, start.AddDate(period.years, period.months, period.days)
				return n, nil
			}
		}
	}

	words := strings.Fields(value)
	if strings.Contains(value, ".") {
		words = strings.Split(value, ".")
	}
	if len(words) != 3 || words[2] != "ago" {
		return n, fmt.Errorf("invalid updated value %q: expected YYYY, YYYY-MM, YYYY-MM-DD, today, yesterday, or a relative time such as 30.days.ago", value)
	}
	day := 24 * time.Hour
	var unit, bucket time.Duration
	switch strings.TrimSuffix(words[1], "s") {
	case "minute":
		unit, bucket = time.Minute, time.Minute
	case "hour":
		unit, bucket = time.Hour, time.Hour
	case "day":
		unit, bucket = day, day
	case "week":
		unit, bucket = 7*day, day
	case "month":
		unit, bucket = 30*day, day
	default:
		return n, fmt.Errorf("invalid relative updated unit %q", words[1])
	}
	count, err := strconv.ParseUint(words[0], 10, 64)
	if err != nil || count > uint64(math.MaxInt64)/uint64(unit) {
		return n, fmt.Errorf("invalid or overflowing relative updated count %q", words[0])
	}
	cutoff := now.Add(-time.Duration(count) * unit)
	n.start, n.end = cutoff, cutoff
	if n.comparator == "" {
		n.start = cutoff.Truncate(bucket)
		n.end = n.start.Add(bucket)
	} else {
		n.instant = true
	}
	return n, nil
}
