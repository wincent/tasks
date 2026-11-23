package cli

import (
	"context"
	"fmt"
	"runtime/debug"
)

func init() {
	commands.register(commandSpec{
		name: "version", autoCorrect: allowCorrection,
		summary: "Print the version and exit.",
		run: func(_ context.Context, _ string, _ arguments, streams IO) error {
			_, err := fmt.Fprintln(streams.Out, buildVersion())
			return err
		},
	})
}

// releaseVersion is set by the release packager with -ldflags.
var releaseVersion string

func buildVersion() string {
	if releaseVersion != "" {
		return releaseVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
