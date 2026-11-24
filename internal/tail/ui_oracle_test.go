package tail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func uiEntry(t *testing.T, entry any) any {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	result, err := decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func uiRole(role string, content any, extra ...map[string]any) map[string]any {
	message := map[string]any{"role": role, "content": content}
	for _, fields := range extra {
		for key, value := range fields {
			message[key] = value
		}
	}
	return map[string]any{"type": "message", "message": message}
}
func TestTailOracleToolsPreviewsAndConversation(t *testing.T) {
	t.Parallel()
	output := "one\ntwo\nthree\nfour\nfive"
	diagnostic := strings.Repeat("failure context\n", 30) + "LAST DIAGNOSTIC"
	entries := []any{
		uiRole("assistant", []any{
			map[string]any{"type": "thinking", "thinking": "Always visible"},
			map[string]any{"type": "toolCall", "name": "bash", "id": "raw-id", "arguments": map[string]any{"command": "npm run check", "timeout": 60}},
			map[string]any{"type": "toolCall", "name": "read", "arguments": map[string]any{"path": "src/main.ts", "offset": 42}},
			map[string]any{"type": "toolCall", "name": "edit", "arguments": map[string]any{"path": "src/main.ts", "edits": []any{map[string]any{"oldText": "old", "newText": "new"}}}},
			map[string]any{"type": "toolCall", "name": "unknown", "arguments": map[string]any{"custom": "payload"}},
		}),
		uiRole("toolResult", output, map[string]any{"toolName": "bash", "toolCallId": "raw-id"}),
		uiRole("toolResult", strings.Repeat("x", 350), map[string]any{"toolName": "read"}),
		uiRole("toolResult", diagnostic, map[string]any{"toolName": "bash", "isError": true}),
		uiRole("bashExecution", nil, map[string]any{"command": "false", "exitCode": 1, "output": diagnostic}),
	}
	render := func() string {
		var out strings.Builder
		for _, entry := range entries {
			out.WriteString((Renderer{}).Entry(uiEntry(t, entry)) + "\n")
		}
		return out.String()
	}
	compact := render()
	for _, want := range []string{"[thinking]\nAlways visible", "[tool bash → npm run check]", "[tool read → src/main.ts]", "[tool edit → src/main.ts (1 edits)]", "[tool unknown → custom]", "one\ntwo\nthree\n[… 10 characters omitted]", "[… 50 characters omitted]", "[result bash: ERROR]", "[bash: ERROR, exit 1] false", diagnostic} {
		if !strings.Contains(compact, want) {
			t.Fatalf("missing %q: %s", want, compact)
		}
	}
	for _, hidden := range []string{"raw-id", "\"timeout\"", "four", "\"oldText\""} {
		if strings.Contains(compact, hidden) {
			t.Fatalf("compact leaked %s", hidden)
		}
	}
	entries = []any{
		uiRole("system", "HIDDEN SYSTEM PROMPT"),
		uiRole("user", "Please implement"),
		uiRole("assistant", []any{map[string]any{"type": "thinking", "thinking": "Inspect first"}, map[string]any{"type": "text", "text": "Working on it"}, map[string]any{"type": "toolCall", "name": "bash", "id": "call-1", "arguments": map[string]any{"command": "echo hello"}}}),
		uiRole("toolResult", "hello", map[string]any{"toolName": "bash", "toolCallId": "call-1"}),
		uiRole("toolResult", "failed", map[string]any{"toolName": "read", "toolCallId": "call-2", "isError": true}),
		uiRole("assistant", []any{}, map[string]any{"stopReason": "error", "errorMessage": "Provider unavailable"}),
		uiRole("user", []any{map[string]any{"type": "image", "data": "HIDDEN BASE64"}}),
		map[string]any{"type": "model_change", "provider": "provider", "modelId": "model"},
		map[string]any{"type": "thinking_level_change", "thinkingLevel": "high"},
		map[string]any{"type": "compaction", "summary": "Compacted context"},
		map[string]any{"type": "branch_summary", "summary": "Previous branch"},
		map[string]any{"type": "custom", "data": "HIDDEN STATE"},
		map[string]any{"type": "custom_message", "display": false, "content": "HIDDEN EXTENSION"},
		map[string]any{"type": "custom_message", "display": true, "customType": "notice", "content": "Visible extension"},
		uiRole("bashExecution", nil, map[string]any{"command": "pwd", "output": "/checkout", "exitCode": 0}),
		uiRole("custom", "Custom message", map[string]any{"customType": "notice", "display": true}),
	}
	conversation := render()
	for _, want := range []string{"[user]\nPlease implement", "[thinking]\nInspect first", "Working on it", "[tool bash → echo hello]", "[result bash: ok]\nhello", "[result read: ERROR]\nfailed", "[ERROR] Provider unavailable", "[image]", "[model] provider/model", "[thinking level] high", "Compacted context", "Previous branch", "Visible extension", "[bash: exit 0] pwd\n/checkout", "Custom message"} {
		if !strings.Contains(conversation, want) {
			t.Fatalf("conversation missing %q: %s", want, conversation)
		}
	}
	if strings.Contains(conversation, "HIDDEN") {
		t.Fatal(conversation)
	}
}
func TestTailOraclePhysicalHiddenAndCorruptHistory(t *testing.T) {
	t.Parallel()
	body := record("assistant", "OMITTED") + "bad omitted json\n" + record("system", "HIDDEN") + "{\"type\":\"unknown\"}\n" + "bad visible json\n"
	for i := 0; i < 37; i++ {
		body += record("assistant", fmt.Sprintf("Visible %d", i))
	}
	file, root := session(t, body)
	var out, diagnostic bytes.Buffer
	if err := readWindow(file, root, &out, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[showing recent session activity; skipped 2 lines]") || strings.Contains(out.String(), "OMITTED") || strings.Contains(out.String(), "HIDDEN") || !strings.Contains(diagnostic.String(), "malformed session record at line 6") || strings.Contains(diagnostic.String(), "at line 3") {
		t.Fatalf("%s\n%s", out.String(), diagnostic.String())
	}
}
func TestTailOracleCorruptControlsAndIncompleteSnapshot(t *testing.T) {
	t.Parallel()
	file, root := session(t, "not json\nnull\n{\"type\":\"future_type\"}\n"+record("assistant", "safe\x1b]52;c;unsafe\a\u009b31m\rsafe")+"{\"type\":")
	var out, diagnostic bytes.Buffer
	if err := readWindow(file, root, &out, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "safe") || strings.Contains(out.String(), "{\"type\":") || !strings.Contains(diagnostic.String(), "skipping malformed session record at line 2") {
		t.Fatalf("%q %q", out.String(), diagnostic.String())
	}
	for _, r := range out.String() {
		if r < 32 && r != '\n' && r != '\t' || r >= 127 && r <= 159 {
			t.Fatalf("unsafe rune %U", r)
		}
	}
}
