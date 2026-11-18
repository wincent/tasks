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
	"sync"
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
	once     sync.Once
	callback func()
}

func (w *callbackWriter) Write(data []byte) (int, error) {
	n, err := w.buffer.Write(data)
	w.once.Do(w.callback)
	return n, err
}

func TestFollowDetectsReplacementTruncationAndCancellation(t *testing.T) {
	for _, mode := range []string{"replacement", "truncation", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			file, root := session(t, record("assistant", "first"))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			writer := &callbackWriter{callback: func() {
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
	partial := strings.TrimSuffix(record("assistant", "late final output"), "\n")
	file, root := session(t, record("assistant", "first")+partial)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	writer := &callbackWriter{callback: func() {
		f, err := os.OpenFile(file.Name(), os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString("\n"); err != nil {
			t.Fatal(err)
		}
	}}
	var diagnostic bytes.Buffer
	err := Follow(ctx, file, root, "exact-session", false, writer, &diagnostic)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if strings.Count(writer.buffer.String(), "late final output") != 1 {
		t.Fatal(writer.buffer.String())
	}
}

func TestHeaderRequiresExactOwnerAndID(t *testing.T) {
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
