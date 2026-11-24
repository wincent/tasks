package tail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestHistoryBoundaryMatrix(t *testing.T) {
	t.Parallel()
	for _, count := range []int{0, 39, 40, 41, 83} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var body strings.Builder
			for i := 0; i < count; i++ {
				body.WriteString(record("assistant", fmt.Sprintf("Activity %d\nsecond line", i)))
			}
			body.WriteString(strings.TrimSuffix(record("assistant", "PARTIAL"), "\n"))
			file, root := session(t, body.String())
			var out, diagnostic bytes.Buffer
			if err := readWindow(file, root, &out, &diagnostic); err != nil {
				t.Fatal(err)
			}
			skipped := 0
			if count > HistoryLines {
				skipped = count - HistoryLines
			}
			if strings.Contains(out.String(), "PARTIAL") || diagnostic.Len() != 0 {
				t.Fatalf("%s %s", out.String(), diagnostic.String())
			}
			if strings.Contains(out.String(), "skipped") != (skipped > 0) {
				t.Fatal(out.String())
			}
			if skipped > 0 && !strings.Contains(out.String(), fmt.Sprintf("[showing recent session activity; skipped %d lines]", skipped)) {
				t.Fatal(out.String())
			}
			for i := 0; i < count; i++ {
				present := strings.Contains(out.String(), fmt.Sprintf("Activity %d\n", i))
				if present != (i >= skipped) {
					t.Fatalf("record %d present=%v skipped=%d", i, present, skipped)
				}
			}
		})
	}
}

func TestDecodeRejectsCorruptionWithoutDiscardingNextRecord(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"{", "{} {}", "null true", string([]byte{0xff}), ""} {
		t.Run(fmt.Sprintf("%q", bad), func(t *testing.T) {
			file, root := session(t, bad+"\n"+record("assistant", "survives"))
			var out, diagnostic bytes.Buffer
			if err := readWindow(file, root, &out, &diagnostic); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "survives") || diagnostic.String() != "tasks tail: skipping malformed session record at line 2\n" {
				t.Fatalf("%q %q", out.String(), diagnostic.String())
			}
		})
	}
}

func TestRendererRecordMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct{ input, want string }{
		{`{"type":"message","message":{"role":"user","content":"literal **text**"}}`, "[user]\nliteral **text**"},
		{`{"type":"message","message":{"role":"assistant","content":"partial","stopReason":"error"}}`, "[assistant]\npartial\n[ERROR]"},
		{`{"type":"message","message":{"role":"assistant","content":"partial","stopReason":"aborted"}}`, "[assistant]\npartial\n[ABORTED]"},
		{`{"type":"message","message":{"role":"assistant","errorMessage":"provider failed"}}`, "[assistant]\n\n[ERROR] provider failed"},
		{`{"type":"message","message":{"role":"custom","customType":"notice","content":"shown"}}`, "[extension notice]\nshown"},
		{`{"type":"compaction","summary":"compact"}`, "[compaction]\ncompact"},
		{`{"type":"branch_summary","summary":"branch"}`, "[branch_summary]\nbranch"},
		{`{"type":"model_change","provider":"vendor","modelId":"model"}`, "[model] vendor/model"},
		{`{"type":"thinking_level_change","thinkingLevel":"high"}`, "[thinking level] high"},
		{`{"type":"message","message":{"role":"bashExecution","command":"true","exitCode":0.0,"output":"ok"}}`, "[bash: exit 0.0] true\nok"},
		{`{"type":"message","message":{"role":"bashExecution","command":"false","exitCode":1,"output":"failure"}}`, "[bash: ERROR, exit 1] false\nfailure"},
		{`{"type":"message","message":{"role":"bashExecution","command":"sleep","exitCode":0,"cancelled":true,"output":"cancelled"}}`, "[bash: ERROR, exit 0] sleep\ncancelled"},
		{`{"type":"message","timestamp":"outer","message":{"role":"assistant","timestamp":"inner","content":"text"}}`, "[assistant] outer\ntext"},
		{`{"type":"message","message":{"role":"assistant","timestamp":"inner","content":"text"}}`, "[assistant] inner\ntext"},
	}
	for _, test := range cases {
		entry, err := decode([]byte(test.input))
		if err != nil {
			t.Fatal(err)
		}
		if got := (Renderer{}).Entry(entry); got != test.want {
			t.Fatalf("%s: %q != %q", test.input, got, test.want)
		}
	}
}

func TestFalseDisplayValuesRemainHidden(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"false", "null", `""`, "0", "0.0", "-0", "0e5", "[]", "{}"} {
		for _, kind := range []string{"custom_message", "message"} {
			input := fmt.Sprintf(`{"type":"custom_message","display":%s,"content":"secret"}`, value)
			if kind == "message" {
				input = fmt.Sprintf(`{"type":"message","message":{"role":"custom","display":%s,"content":"secret"}}`, value)
			}
			entry, err := decode([]byte(input))
			if err != nil {
				t.Fatal(err)
			}
			if got := (Renderer{}).Entry(entry); got != "" {
				t.Fatalf("%s: %q", input, got)
			}
		}
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }
func TestFollowPropagatesWriterFailures(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("consumer gone")
	for _, diagnostic := range []bool{false, true} {
		body := record("assistant", "text")
		if diagnostic {
			body = "broken\n"
		}
		file, root := session(t, body)
		var out, errout io.Writer = io.Discard, io.Discard
		if diagnostic {
			errout = failingWriter{sentinel}
		} else {
			out = failingWriter{sentinel}
		}
		if err := Follow(context.Background(), file, root, "exact-session", false, out, errout); !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
	}
}

func TestHeaderShapeMatrix(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, input := range []string{"null", "[]", "{}", `{"type":"session","id":1,"cwd":"relative"}`, `{"type":"session","id":"","cwd":"/"}`, "{} {}"} {
		if err := ValidateHeader([]byte(input), root, ""); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	data, _ := json.Marshal(map[string]any{"type": "session", "id": "saved", "cwd": root})
	if err := ValidateHeader(data, root, "saved"); err != nil {
		t.Fatal(err)
	}
}
