package tail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func record(role, text string) string {
	data, _ := json.Marshal(map[string]any{"type": "message", "message": map[string]any{"role": role, "content": text}})
	return string(data) + "\n"
}
func session(t *testing.T, body string) (*os.File, string) {
	t.Helper()
	directory := t.TempDir()
	header, _ := json.Marshal(map[string]any{"type": "session", "id": "exact-session", "cwd": directory})
	path := filepath.Join(directory, "session.jsonl")
	if err := os.WriteFile(path, append(append(header, '\n'), []byte(body)...), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file, directory
}

func readWindow(file *os.File, root string, out, diagnostic io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := Follow(ctx, file, root, "exact-session", false, out, diagnostic)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

type callbackWriter struct {
	buffer   bytes.Buffer
	callback func([]byte)
}

func (w *callbackWriter) Write(data []byte) (int, error) {
	n, err := w.buffer.Write(data)
	w.callback(data)
	return n, err
}

func TestFollowDetectsReplacementTruncationAndCancellation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"replacement", "truncation", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			file, root := session(t, record("assistant", "first"))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			writer := &callbackWriter{callback: func([]byte) {
				switch mode {
				case "replacement":
					if err := os.Rename(file.Name(), file.Name()+".old"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(file.Name(), []byte("new"), 0600); err != nil {
						t.Fatal(err)
					}
				case "truncation":
					if err := os.Truncate(file.Name(), 0); err != nil {
						t.Fatal(err)
					}
				case "cancel":
					cancel()
				}
			}}
			var diagnostic bytes.Buffer
			err := Follow(ctx, file, root, "exact-session", false, writer, &diagnostic)
			if err == nil {
				t.Fatal("follow did not stop")
			}
			want := map[string]string{"replacement": "replaced", "truncation": "truncated", "cancel": "context canceled"}[mode]
			if !strings.Contains(err.Error(), want) {
				t.Fatal(err)
			}
		})
	}
}

func TestFollowCompletesPartialRecordWithoutSwitchingSession(t *testing.T) {
	t.Parallel()
	const first = "first"
	const final = "late final output"
	partial := strings.TrimSuffix(record("assistant", final), "\n")
	file, root := session(t, record("assistant", first)+partial)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	writer := &callbackWriter{callback: func(data []byte) {
		switch {
		case bytes.Contains(data, []byte(final)):
			cancel()
		case bytes.Contains(data, []byte(first)):
			f, err := os.OpenFile(file.Name(), os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString("\n"); err != nil {
				t.Fatal(err)
			}
		}
	}}
	var diagnostic bytes.Buffer
	err := Follow(ctx, file, root, "exact-session", false, writer, &diagnostic)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected follow to stop after writing %q, got %v", final, err)
	}
	if strings.Count(writer.buffer.String(), final) != 1 {
		t.Fatal(writer.buffer.String())
	}
}

func TestHeaderRequiresExactOwnerAndID(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	other := t.TempDir()
	for _, header := range []map[string]any{{"type": "session", "id": "wrong", "cwd": root}, {"type": "session", "id": "exact-session", "cwd": other}, {"type": "session", "id": "exact-session", "cwd": "relative"}, {"type": "other", "id": "exact-session", "cwd": root}} {
		data, _ := json.Marshal(header)
		if err := ValidateHeader(data, root, "exact-session"); err == nil {
			t.Fatal(header)
		}
	}
}

func TestRendererSafetyVisibilityAndPreview(t *testing.T) {
	t.Parallel()
	renderer := Renderer{}
	for _, value := range []any{nil, []any{}, map[string]any{"type": "message", "message": map[string]any{"role": "system", "content": "secret"}}, map[string]any{"type": "custom_message", "display": false, "content": "secret"}} {
		if got := renderer.Entry(value); got != "" {
			t.Fatal(got)
		}
	}
	entry := map[string]any{"type": "message", "timestamp": "now", "message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "thinking", "thinking": "visible thought"},
		map[string]any{"type": "text", "text": "\x1b[2J**bold**\x00\u009b"},
		map[string]any{"type": "toolCall", "name": "edit", "arguments": map[string]any{"path": "a\nb", "edits": []any{1, 2}}},
		map[string]any{"type": "image"},
	}}}
	got := renderer.Entry(entry)
	for _, want := range []string{"[assistant] now", "[thinking]", "visible thought", "**bold**", "tool edit → a b (2 edits)", "[image]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.ContainsAny(got, "\x1b\x00\u009b") {
		t.Fatal("session controls escaped sanitizer")
	}
	long := strings.Repeat("雪", 400) + "\nfour\nfive\nsix"
	result := map[string]any{"type": "message", "message": map[string]any{"role": "toolResult", "toolName": "bash", "toolCallId": "id", "content": long}}
	got = renderer.Entry(result)
	if !strings.Contains(got, "characters omitted") || strings.Contains(got, "six") {
		t.Fatal(got)
	}
	result["message"].(map[string]any)["isError"] = true
	got = renderer.Entry(result)
	if !strings.Contains(got, "six") || strings.Contains(got, "omitted") || !strings.Contains(got, "ERROR") {
		t.Fatal(got)
	}
	color := Renderer{Color: true}
	if got := color.Entry(entry); !strings.Contains(got, "\x1b[1m**bold**\x1b[0m") {
		t.Fatal(got)
	}
}

func TestFollowHandlesLinesLargerThanScanBuffer(t *testing.T) {
	t.Parallel()
	// Exercise lines that span multiple 64 KiB bufio reads in the skipped
	// prefix, inside the retained window, and as a trailing partial line of
	// exact and non-exact buffer multiples.
	const oversize = 70 * 1024
	bigPayload := strings.Repeat("x", oversize)

	for _, tc := range []struct {
		name     string
		trailing string
	}{
		{"none", ""},
		{"exact-buffer", strings.Repeat("p", scanBufferSize)}, // exact buffer-sized partial line without '\n'
		{"oversize", strings.Repeat("q", oversize)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body strings.Builder
			// 5 skipped lines, including an oversized line.
			body.WriteString(record("assistant", "skip-0"))
			body.WriteString(record("assistant", "skip-big "+bigPayload))
			for range 3 {
				body.WriteString(record("assistant", "skip-more"))
			}
			// 40 retained lines: include a malformed line and an oversized line.
			body.WriteString(record("assistant", "keep-0"))
			body.WriteString("malformed\n")
			body.WriteString(record("assistant", "keep-big "+bigPayload))
			for i := 3; i < HistoryLines-1; i++ {
				body.WriteString(record("assistant", "keep-mid"))
			}
			body.WriteString(record("assistant", "keep-last"))
			body.WriteString(tc.trailing)

			file, root := session(t, body.String())
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			writer := &callbackWriter{callback: func(data []byte) {
				if bytes.Contains(data, []byte("keep-last")) {
					cancel()
				}
			}}
			var diagnostic bytes.Buffer
			err := Follow(ctx, file, root, "exact-session", false, writer, &diagnostic)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Follow: %v", err)
			}
			got := writer.buffer.String()
			if !strings.Contains(got, "[showing recent session activity; skipped 5 lines]") {
				t.Fatalf("missing skipped banner:\n%s", got)
			}
			if strings.Contains(got, "skip-0") || strings.Contains(got, "skip-big") {
				t.Fatalf("emitted skipped line")
			}
			if !strings.Contains(got, "keep-0") || !strings.Contains(got, "keep-big") || !strings.Contains(got, "keep-last") {
				t.Fatalf("missing retained lines")
			}
			// Line 1 = header, lines 2..6 = 5 skipped, line 7 = keep-0, line 8 = malformed.
			if diagnostic.String() != "tasks tail: skipping malformed session record at line 8\n" {
				t.Fatalf("diagnostic: %q", diagnostic.String())
			}
		})
	}
}

func BenchmarkFollowInitialScan(b *testing.B) {
	var body strings.Builder
	payload := strings.Repeat("a", 200)
	for range 5000 {
		body.WriteString(record("assistant", payload))
	}
	body.WriteString(record("assistant", "END-MARKER"))
	directory := b.TempDir()
	header, _ := json.Marshal(map[string]any{"type": "session", "id": "exact-session", "cwd": directory})
	path := filepath.Join(directory, "session.jsonl")
	if err := os.WriteFile(path, append(append(header, '\n'), []byte(body.String())...), 0600); err != nil {
		b.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()

	b.ResetTimer()
	for b.Loop() {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			b.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		writer := &callbackWriter{callback: func(data []byte) {
			if bytes.Contains(data, []byte("END-MARKER")) {
				cancel()
			}
		}}
		err := Follow(ctx, file, directory, "exact-session", false, writer, io.Discard)
		cancel()
		if !errors.Is(err, context.Canceled) {
			b.Fatalf("Follow: %v", err)
		}
	}
}
