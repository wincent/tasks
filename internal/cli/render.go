package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"github.com/wincent/tasks/internal/terminal"
)

func renderMarkdown(ctx context.Context, cwd, content string, raw bool, streams IO) error {
	if !raw && terminal.IsTerminal(streams.Out) {
		if binary, err := exec.LookPath("glow"); err == nil {
			command := exec.CommandContext(ctx, binary, "-")
			command.Dir = cwd
			command.Stdin = strings.NewReader(content)
			command.Stdout = streams.Out
			command.Stderr = streams.Err
			if err := command.Run(); err == nil {
				return nil
			} else {
				fmt.Fprintf(streams.Err, "tasks: glow failed (%v); showing raw Markdown\n", err)
			}
		}
	}
	_, err := fmt.Fprint(streams.Out, content)
	return err
}

// Base presentation on the destination stream: stdout can be redirected
// while prompts and progress on stderr still go to a terminal.
type presentation struct {
	interactive bool
	styled      bool
}

func terminalPresentation(out io.Writer) presentation {
	interactive := terminal.IsTerminal(out)
	// Preserve list's historical nonempty NO_COLOR behavior.
	return presentation{interactive, interactive && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"}
}

func (p presentation) paint(value, code string) string {
	if p.interactive {
		// Don't interpret control characters from task titles or other data.
		value = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, value)
	}
	if p.styled && code != "" && value != "" {
		return "\x1b[" + code + "m" + value + "\x1b[0m"
	}
	return value
}

func (p presentation) label(value, icon, code string) string {
	if p.styled && icon != "" {
		return icon + " " + p.paint(value, code)
	}
	return p.paint(value, code)
}

func (p presentation) task(id, title, separator string) string {
	return p.paint(id, "4") + separator + p.paint(title, "3")
}

func (p presentation) status(value string) string {
	switch value {
	case "active":
		return p.paint(value, "36")
	case "blocked":
		return p.paint(value, "33")
	case "done", "merged":
		return p.paint(value, "32")
	case "closed":
		return p.paint(value, "2")
	}
	return value
}

func (p presentation) changeMarker(value string) string {
	if value == "?" {
		return p.paint(value, "34")
	}
	return p.paint(value, "31")
}

func taskResult(out io.Writer, action, id, title, path string) error {
	p := terminalPresentation(out)
	if !p.interactive {
		_, err := fmt.Fprintln(out, path)
		return err
	}
	icon, color := "✅", "32"
	if action == "Unchanged" {
		icon, color = "ℹ️", "36"
	}
	_, err := fmt.Fprintf(out, "%s %s %s\n",
		p.label(action+":", icon, "1;"+color), p.paint(id, "4;"+color), p.paint(title, "3;"+color))
	return err
}
