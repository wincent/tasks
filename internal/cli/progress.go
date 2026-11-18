package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/wincent/tasks/internal/tail"
)

// Pi's JSON mode streams native events instead of waiting until the final
// response. Display compact progress and keep the exact transcript on disk.
type piProgress struct {
	out              io.Writer
	pending          []byte
	skip             bool
	summary, failure string
}

func (p *piProgress) Write(data []byte) (int, error) {
	length := len(data)
	for len(data) > 0 {
		line, rest, end := bytes.Cut(data, []byte{'\n'})
		if len(p.pending)+len(line) > 1024*1024 {
			p.skip = true
			p.pending = nil
		}
		if !p.skip {
			p.pending = append(p.pending, line...)
		}
		if end {
			if !p.skip {
				if err := p.event(p.pending); err != nil {
					return 0, err
				}
			}
			p.pending = p.pending[:0]
			p.skip = false
		}
		data = rest
	}
	return length, nil
}

func shortResult(s string) string {
	r := []rune(tail.Safe(strings.TrimSpace(s)))
	if len(r) > 1200 {
		return string(r[:1200]) + "\n(See the session for the rest.)"
	}
	return string(r)
}

func (p *piProgress) event(line []byte) error {
	var e struct {
		Type     string `json:"type"`
		ToolName string `json:"toolName"`
		IsError  bool   `json:"isError"`
		Message  struct {
			Role       string                        `json:"role"`
			StopReason string                        `json:"stopReason"`
			Error      string                        `json:"errorMessage"`
			Content    []struct{ Type, Text string } `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &e); err != nil {
		return nil
	}
	var output string
	switch e.Type {
	case "tool_execution_start":
		output = "Running " + e.ToolName + "..."
	case "tool_execution_end":
		if e.IsError {
			output = e.ToolName + " reported an error (see tasks tail)."
		}
	case "message_end":
		if e.Message.Role != "assistant" {
			break
		}
		p.failure = ""
		switch e.Message.StopReason {
		case "error", "aborted", "length":
			p.failure = e.Message.Error
			if p.failure == "" {
				p.failure = "Pi stopped: " + e.Message.StopReason
			}
		}
		var text []string
		for _, c := range e.Message.Content {
			if c.Type == "text" {
				text = append(text, c.Text)
			}
		}
		output = strings.TrimSpace(strings.Join(text, "\n"))
		p.summary = output
	}
	if output != "" {
		_, err := fmt.Fprintln(p.out, tail.Safe(output))
		return err
	}
	return nil
}
