// Package terminal contains the small POSIX terminal adapter shared by command
// rendering and interactive editing. It does not install signal handlers.
package terminal

/*
#include <sys/ioctl.h>
#include <unistd.h>

static int task_columns(int fd) {
	struct winsize size;
	return ioctl(fd, TIOCGWINSZ, &size) == 0 ? size.ws_col : 0;
}
*/
import "C"

import (
	"io"
	"os"
)

func IsTerminal(stream any) bool {
	descriptor, ok := stream.(interface{ Fd() uintptr })
	return ok && C.isatty(C.int(descriptor.Fd())) == 1
}

// Columns returns the terminal's current width, or zero if unavailable.
func Columns(out io.Writer) int {
	if descriptor, ok := out.(interface{ Fd() uintptr }); ok {
		return int(C.task_columns(C.int(descriptor.Fd())))
	}
	return 0
}

func Color(out io.Writer) bool {
	_, disabled := os.LookupEnv("NO_COLOR")
	return IsTerminal(out) && !disabled && os.Getenv("TERM") != "dumb"
}
