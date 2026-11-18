// Package tail renders persisted Pi activity without starting or resuming Pi.
package tail

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const HistoryLines = 40

type Renderer struct{ Color bool }

func Safe(value any) string {
	text := fmt.Sprint(value)
	if value == nil {
		text = ""
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (r >= 32 && (r < 127 || r > 159)) {
			return r
		}
		return -1
	}, text)
}
func (r Renderer) paint(value any, code string) string {
	text := Safe(value)
	if r.Color {
		return "\x1b[" + code + "m" + text + "\x1b[0m"
	}
	return text
}
func (r Renderer) heading(label, code string) string { return r.paint("["+label+"]", code) }

var markdownHeading = regexp.MustCompile(`^#{1,6} `)
var markdownInline = regexp.MustCompile("(`[^`\n]+`|\\*\\*[^*\n]+\\*\\*)")

func (r Renderer) markdown(value any) string {
	text := Safe(value)
	if !r.Color {
		return text
	}
	lines := strings.Split(text, "\n")
	fenced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "```") || strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "~~~") {
			fenced = !fenced
			lines[i] = r.paint(line, "2")
		} else if fenced {
			lines[i] = r.paint(line, "36")
		} else if markdownHeading.MatchString(line) {
			lines[i] = r.paint(line, "1;36")
		} else {
			lines[i] = markdownInline.ReplaceAllStringFunc(line, func(match string) string {
				code := "1"
				if strings.HasPrefix(match, "`") {
					code = "36"
				}
				return r.paint(match, code)
			})
		}
	}
	return strings.Join(lines, "\n")
}
func (r Renderer) preview(value any) string {
	text := Safe(value)
	lines := strings.Split(text, "\n")
	if len(lines) > 3 {
		lines = lines[:3]
	}
	preview := []rune(strings.Join(lines, "\n"))
	if len(preview) > 300 {
		preview = preview[:300]
	}
	omitted := len([]rune(text)) - len(preview)
	if omitted > 0 {
		return string(preview) + r.paint(fmt.Sprintf("\n[… %d characters omitted]", omitted), "2")
	}
	return string(preview)
}
func value(object map[string]any, key, fallback string) string {
	if v, ok := object[key]; ok {
		return Safe(v)
	}
	return fallback
}
func (r Renderer) tool(block map[string]any) string {
	name := value(block, "name", "?")
	arguments := block["arguments"]
	if arguments == nil {
		arguments = map[string]any{}
	}
	detail := ""
	if args, ok := arguments.(map[string]any); ok {
		for _, key := range []string{"command", "path", "file_path", "query", "url", "task", "description", "code"} {
			if text := Safe(args[key]); text != "" {
				detail = strings.Join(strings.Fields(text), " ")
				break
			}
		}
		if edits, ok := args["edits"].([]any); ok {
			detail += fmt.Sprintf(" (%d edits)", len(edits))
		}
		if detail == "" && len(args) > 0 {
			keys := []string{}
			for key := range args {
				keys = append(keys, Safe(key))
			}
			sort.Strings(keys)
			detail = strings.Join(keys, ", ")
		}
	} else {
		detail = strings.Join(strings.Fields(Safe(arguments)), " ")
	}
	if chars := []rune(detail); len(chars) > 160 {
		detail = string(chars[:159]) + "…"
	}
	suffix := ""
	if detail != "" {
		suffix = " → " + detail
	}
	return r.heading("tool "+name+suffix, "1;33")
}
func (r Renderer) content(content any, markdown bool) string {
	render := Safe
	if markdown {
		render = r.markdown
	}
	if text, ok := content.(string); ok {
		return render(text)
	}
	parts := []string{}
	if blocks, ok := content.([]any); ok {
		for _, entry := range blocks {
			block, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			switch block["type"] {
			case "text":
				parts = append(parts, render(block["text"]))
			case "thinking":
				parts = append(parts, r.heading("thinking", "2;35")+"\n"+Safe(block["thinking"]))
			case "toolCall":
				parts = append(parts, r.tool(block))
			case "image":
				parts = append(parts, "[image]")
			}
		}
	}
	return strings.Join(parts, "\n\n")
}
func truth(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	case json.Number:
		n, err := v.Float64()
		return err != nil || n != 0
	case []any:
		return len(v) != 0
	case map[string]any:
		return len(v) != 0
	default:
		return true
	}
}
func displayed(entry map[string]any) bool { v, ok := entry["display"]; return !ok || truth(v) }
func (r Renderer) body(entry map[string]any) string {
	switch entry["type"] {
	case "message":
		message, ok := entry["message"].(map[string]any)
		if !ok {
			return ""
		}
		role, _ := message["role"].(string)
		switch role {
		case "user", "assistant", "toolResult", "custom":
			if role == "custom" && !displayed(message) {
				return ""
			}
			label, code := role, "1;36"
			if role == "user" {
				code = "1;32"
			}
			content := r.content(message["content"], role == "assistant")
			if role == "toolResult" {
				failed := truth(message["isError"])
				status := "ok"
				code = "32"
				if failed {
					status = "ERROR"
					code = "1;31"
				}
				label = "result " + value(message, "toolName", "?") + ": " + status
				if !failed {
					content = r.preview(content)
				}
			} else if role == "custom" {
				label = "extension " + value(message, "customType", "?")
			}
			if truth(message["errorMessage"]) {
				content += "\n" + r.heading("ERROR", "1;31") + " " + Safe(message["errorMessage"])
			} else if role == "assistant" && (message["stopReason"] == "error" || message["stopReason"] == "aborted") {
				content += "\n" + r.heading(strings.ToUpper(message["stopReason"].(string)), "1;31")
			}
			return r.heading(label, code) + "\n" + content
		case "bashExecution":
			failed := truth(message["exitCode"]) || truth(message["cancelled"])
			label := "bash: "
			code := "32"
			if failed {
				label += "ERROR, "
				code = "1;31"
			}
			label += "exit " + value(message, "exitCode", "?")
			output := Safe(message["output"])
			if !failed {
				output = r.preview(output)
			}
			return r.heading(label, code) + " " + Safe(message["command"]) + "\n" + output
		}
	case "compaction", "branch_summary":
		return r.heading(entry["type"].(string), "1;36") + "\n" + Safe(entry["summary"])
	case "model_change":
		return r.heading("model", "2") + " " + value(entry, "provider", "?") + "/" + value(entry, "modelId", "?")
	case "thinking_level_change":
		return r.heading("thinking level", "2") + " " + value(entry, "thinkingLevel", "?")
	case "custom_message":
		if displayed(entry) {
			return r.heading("extension "+value(entry, "customType", "?"), "1;36") + "\n" + r.content(entry["content"], false)
		}
	}
	return ""
}
func (r Renderer) Entry(value any) string {
	entry, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	body := r.body(entry)
	if body == "" {
		return ""
	}
	timestamp := entry["timestamp"]
	if !truth(timestamp) {
		if message, ok := entry["message"].(map[string]any); ok {
			timestamp = message["timestamp"]
		}
	}
	if timestamp != nil {
		heading, rest, ok := strings.Cut(body, "\n")
		body = heading + " " + r.paint(timestamp, "2")
		if ok {
			body += "\n" + rest
		}
	}
	return strings.TrimRightFunc(body, unicode.IsSpace)
}
