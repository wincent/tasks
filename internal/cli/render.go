package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"

	"github.com/wincent/tasks/internal/store"
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
	out         io.Writer
	interactive bool
	styled      bool
}

func terminalPresentation(out io.Writer) presentation {
	interactive := terminal.IsTerminal(out)
	// Preserve list's historical nonempty NO_COLOR behavior.
	return presentation{out, interactive, interactive && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"}
}

func (p presentation) paint(value, code string) string {
	if p.interactive {
		// Don't interpret control characters from task titles or other data.
		value = withoutControls(value)
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

// printTaskHeadline fits the title around the fixed parts of a progress or
// result line. The ID and message stay visible even on very narrow terminals.
func (p presentation) printTaskHeadline(task store.Task, icon, prefix, suffix string) {
	if p.styled && icon != "" {
		prefix = icon + " " + prefix
	}
	id := p.paint(task.ID(), "4")
	title := withoutControls(terminal.Plain(task.Title))
	if p.interactive {
		if columns := terminal.Columns(p.out); columns > 0 {
			fixed := uniseg.StringWidth(terminal.Plain(prefix+id+suffix)) + len(" ()")
			title = ellipsize(title, columns-fixed)
		}
	}
	if title != "" {
		id += " (" + p.paint(title, "3") + ")"
	}
	fmt.Fprintln(p.out, prefix+id+suffix)
}

func withoutControls(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
}

func ellipsize(title string, width int) string {
	if width < 1 {
		return ""
	}
	if uniseg.StringWidth(title) <= width {
		return title
	}
	// Leave a column for the ellipsis and don't split a grapheme cluster.
	g := uniseg.NewGraphemes(title)
	used, end := 0, 0
	for g.Next() {
		used += g.Width()
		if used >= width {
			break
		}
		_, end = g.Positions()
	}
	return strings.TrimRightFunc(title[:end], unicode.IsSpace) + "…"
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
