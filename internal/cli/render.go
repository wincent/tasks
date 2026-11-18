package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

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

func changeMarker(value string, out any) string {
	// Match list's historical nonempty NO_COLOR behavior (tail uses presence).
	if value != "" && terminal.IsTerminal(out) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" {
		code := "31"
		if value == "?" {
			code = "34"
		}
		return "\x1b[" + code + "m" + value + "\x1b[0m"
	}
	return value
}
