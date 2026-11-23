package query

import (
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 10, 1, 20, 7, 43, 17, time.FixedZone("west", -7*3600))

func statusValid(s string) bool { return s == "open" || s == "merged" || s == "closed" }

func parseTest(t *testing.T, expr string) *Expression {
	t.Helper()
	e, err := Parse(expr, fixedNow, statusValid)
	if err != nil {
		t.Fatalf("%q: %v", expr, err)
	}
	return e
}

func TestLogic(t *testing.T) {
	r := Record{Status: "merged", Title: "Export REPORT (literally status:merged)"}
	for _, tc := range []struct {
		expr string
		want bool
	}{
		{`status:merged OR status:open AND title:no`, true},
		{`(status:merged OR status:open) AND title:no`, false},
		{`NOT status:open AND title:no`, false},
		{`NOT (status:open AND title:no)`, true},
		{`NOT NOT (status:"merged" AND title:"export report")`, true},
		{"\t title:status:merged\n AND (title:\"(literally\" OR status:closed) ", true},
		{`title:EXPORT AND NOT title:export`, false},
	} {
		e := parseTest(t, tc.expr)
		if e.NeedsUpdated() || e.Matches(r) != tc.want {
			t.Errorf("%q: wanted %t", tc.expr, tc.want)
		}
	}
	for _, expr := range []string{`title:can't`, `title:'ship'`, `title:"can't"`} {
		e := parseTest(t, expr)
		if !e.Matches(Record{Title: "Can't 'ship'"}) || e.Matches(Record{Title: "Can ship"}) {
			t.Errorf("%q: apostrophes must be matched literally", expr)
		}
	}
	if !parseTest(t, `status:merged OR NOT updated:today`).NeedsUpdated() {
		t.Fatal("updated must be detected anywhere in the tree")
	}
}

func TestUpdatedPeriods(t *testing.T) {
	for _, tc := range []struct{ value, start, end string }{
		{"2026", "2026-01-01", "2027-01-01"},
		{"2026-10", "2026-10-01", "2026-11-01"},
		{"2024-02", "2024-02-01", "2024-03-01"},
		{"2024-02-29", "2024-02-29", "2024-03-01"},
		{"2026-12", "2026-12-01", "2027-01-01"},
		{"today", "2026-10-02", "2026-10-03"},
		{"yesterday", "2026-10-01", "2026-10-02"},
	} {
		start, _ := time.Parse(time.DateOnly, tc.start)
		end, _ := time.Parse(time.DateOnly, tc.end)
		for _, cmp := range []struct {
			op   string
			want [4]bool
		}{
			{"", [4]bool{false, true, true, false}},
			{"<", [4]bool{true, false, false, false}},
			{"<=", [4]bool{true, true, true, false}},
			{">", [4]bool{false, false, false, true}},
			{">=", [4]bool{false, true, true, true}},
		} {
			forms := []string{
				cmp.op + tc.value,
				" " + cmp.op + " " + tc.value,
				`"` + cmp.op + tc.value + `"`,
				"\" \t" + cmp.op + " \t" + tc.value + " \"",
			}
			for _, quote := range []string{`"`, "'"} {
				forms = append(forms, cmp.op+quote+tc.value+quote, cmp.op+" \t"+quote+" "+tc.value+" "+quote)
			}
			for _, form := range forms {
				e := parseTest(t, "updated:"+form)
				for i, at := range []time.Time{start.Add(-1), start, end.Add(-1), end} {
					if got := e.Matches(Record{Updated: at}); got != cmp.want[i] {
						t.Errorf("updated:%s at %v: got %t", form, at, got)
					}
				}
			}
		}
	}
}

func TestUpdatedRelative(t *testing.T) {
	day := 24 * time.Hour
	for _, tc := range []struct {
		unit         string
		step, bucket time.Duration
	}{
		{"minute", time.Minute, time.Minute},
		{"hour", time.Hour, time.Hour},
		{"day", day, day},
		{"week", 7 * day, day},
		{"month", 30 * day, day},
	} {
		for _, n := range []struct {
			count, unit string
			steps       time.Duration
		}{{"1", tc.unit, 1}, {"2", tc.unit + "s", 2}} {
			cutoff := fixedNow.Add(-n.steps * tc.step)
			start := cutoff.Truncate(tc.bucket)
			end := start.Add(tc.bucket)
			forms := []string{
				n.count + "." + n.unit + ".ago",
				"\t" + n.count + " \t " + n.unit + "   ago",
				`"` + n.count + "." + n.unit + `.ago"`,
				"\"" + n.count + " \t " + n.unit + "   ago\"",
			}
			for _, form := range forms {
				for _, cmp := range []struct {
					op   string
					want [3]bool
				}{
					{"<", [3]bool{true, false, false}},
					{"<=", [3]bool{true, true, false}},
					{">", [3]bool{false, false, true}},
					{">=", [3]bool{false, true, true}},
				} {
					value := cmp.op + form
					if strings.HasPrefix(form, `"`) {
						value = `" ` + cmp.op + " " + form[1:]
					}
					e := parseTest(t, "updated:"+value)
					for i, at := range []time.Time{cutoff.Add(-1), cutoff, cutoff.Add(1)} {
						if got := e.Matches(Record{Updated: at}); got != cmp.want[i] {
							t.Errorf("updated:%s at %v: got %t", value, at, got)
						}
					}
				}
				e := parseTest(t, "updated:"+form)
				if e.Matches(Record{Updated: start.Add(-1)}) || !e.Matches(Record{Updated: start}) ||
					!e.Matches(Record{Updated: end.Add(-1)}) || e.Matches(Record{Updated: end}) {
					t.Errorf("incorrect relative period for %q", form)
				}
			}
		}
	}
}

func TestUpdatedNaturalDateForms(t *testing.T) {
	cutoff := fixedNow.Add(-3 * 24 * time.Hour)
	for _, expr := range []string{
		`updated: < 3 days ago`,
		`updated:< 3 days ago`,
		`updated:'< 3 days ago'`,
		`updated:'< 3.days.ago'`,
		`updated:'<3.days.ago'`,
		`updated:<3.days.ago`,
		`updated:"< 3 days ago"`,
		`updated:<"3 days ago"`,
		`updated:< "3 days ago"`,
		`updated:<'3 days ago'`,
		`updated:< '3 days ago'`,
		`(updated: < 3 days ago AND status:merged)`,
		`(updated: < 3 days ago) AND status:merged`,
		`(updated: < 3 days ago OR status:open)`,
	} {
		e := parseTest(t, expr)
		for _, delta := range []time.Duration{-1, 0, 1} {
			if got := e.Matches(Record{Status: "merged", Updated: cutoff.Add(delta)}); got != (delta < 0) {
				t.Errorf("%q: got %t at cutoff + %v", expr, got, delta)
			}
		}
	}
	if parseTest(t, `(updated: < 3 days ago AND status:merged)`).Matches(Record{Status: "open", Updated: cutoff.Add(-1)}) {
		t.Error("compound query matched an open record")
	}

	// A four-digit count is a year only when it is not part of a phrase.
	yearCutoff := fixedNow.Add(-2026 * 24 * time.Hour)
	e := parseTest(t, `updated:>= 2026 days ago AND status:merged`)
	if !e.Matches(Record{Status: "merged", Updated: yearCutoff}) || e.Matches(Record{Status: "merged", Updated: yearCutoff.Add(-1)}) {
		t.Error("four-digit relative count was not evaluated as a relative date")
	}
}

func TestUpdatedQuotedDateBooleanBoundaries(t *testing.T) {
	e := parseTest(t, `(updated:<"3 days ago" AND status:merged) OR (NOT status:open AND updated:>= '3 days ago')`)
	if !e.NeedsUpdated() {
		t.Fatal("quoted dates must be detected in compound queries")
	}
	cutoff := fixedNow.Add(-3 * 24 * time.Hour)
	for _, tc := range []struct {
		status string
		delta  time.Duration
		want   bool
	}{
		{"merged", -1, true},
		{"closed", -1, false},
		{"open", 0, false},
		{"closed", 0, true},
	} {
		if got := e.Matches(Record{Status: tc.status, Updated: cutoff.Add(tc.delta)}); got != tc.want {
			t.Errorf("%s at cutoff + %v: got %t, want %t", tc.status, tc.delta, got, tc.want)
		}
	}
}

func TestInvalid(t *testing.T) {
	for _, tc := range []struct{ expr, errorPart string }{
		{"", "byte 1"},
		{"status:", "byte 8"},
		{"status: merged", "byte 9"},
		{"status:all", "invalid status"},
		{"status:MERGED", "invalid status"},
		{"title:\"\"", "nonempty value"},
		{"Title:word", "unknown field"},
		{"title:\"no close", "unterminated quoted value"},
		{"title:no status:merged", "byte 10"},
		{"title:word AND", "expected a key:value"},
		{"(title:word", "closing parenthesis"},
		{"title:word)", "end of query"},
		{"title:\"word\"suffix", "byte 13"},
		{"updated:2026-10-1", "invalid updated value"},
		{"updated:2026-02-29", "invalid updated value"},
		{"updated:2026-13", "invalid updated value"},
		{"updated:>>2026", "invalid updated value"},
		{"updated:<", "expected a date after '<'"},
		{"updated: <= AND status:merged", "expected a date after '<='"},
		{"updated:> OR status:merged", "byte 9: expected a date after '>'"},
		{"updated:> NOT status:merged", "byte 9: expected a date after '>'"},
		{"(updated: >)", "expected a date after '>'"},
		{"updated:'>= '", "expected a date after '>='"},
		{"updated:'< 3 days ago", "unterminated quoted value"},
		{`updated:>=""`, "byte 11: expected a date after '>='"},
		{`updated:< '  '`, "byte 11: expected a date after '<'"},
		{`updated:>="3 days ago`, "byte 11: unterminated quoted value"},
		{`updated:< '3 days ago`, "byte 11: unterminated quoted value"},
		{`updated:>='2026-02-29'`, "byte 11: invalid updated value"},
		{`updated:<"AND" OR status:merged`, `byte 10: invalid updated value "AND"`},
		{`updated:>='2026' days ago`, "byte 18: expected AND, OR"},
		{"updated: < 3 days", "invalid updated value"},
		{"(updated: < 3 days AND status:merged)", "invalid updated value"},
		{"updated: < 2026 days", "expected AND, OR"},
		{"updated: < 3 days ago ago", "expected AND, OR"},
		{"updated:1.year.ago", "invalid relative updated unit"},
		{"updated:-1.day.ago", "invalid or overflowing"},
		{"updated:1.5.hours.ago", "invalid updated value"},
		{"updated:18446744073709551616.days.ago", "invalid or overflowing"},
		{"updated:1000000000000.months.ago", "invalid or overflowing"},
		{strings.Repeat("NOT ", 257) + "title:x", "nesting is too deep"},
	} {
		_, err := Parse(tc.expr, fixedNow, statusValid)
		if err == nil || !strings.Contains(err.Error(), "query at byte ") || !strings.Contains(err.Error(), tc.errorPart) {
			t.Errorf("%q: wanted error containing %q, got %v", tc.expr, tc.errorPart, err)
		}
	}
}
