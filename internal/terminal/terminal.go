// Package terminal contains the small POSIX terminal adapter shared by command
// rendering and interactive editing. It does not install signal handlers.
package terminal

/*
#include <poll.h>
#include <sys/ioctl.h>
#include <unistd.h>

static int task_readable(int fd) {
	struct pollfd input = { .fd = fd, .events = POLLIN };
	return poll(&input, 1, 100);
}

static int task_columns(int fd) {
	struct winsize size;
	return ioctl(fd, TIOCGWINSZ, &size) == 0 ? size.ws_col : 0;
}
*/
import "C"

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"
)

func IsTerminal(stream any) bool {
	descriptor, ok := stream.(interface{ Fd() uintptr })
	return ok && C.isatty(C.int(descriptor.Fd())) == 1
}

// Input allows a prompt to stop waiting when the command is cancelled.
// It is intended for a terminal with a single reader.
func Input(ctx context.Context, in io.Reader) io.Reader {
	return terminalInput{ctx, in, in.(interface{ Fd() uintptr }).Fd()}
}

type terminalInput struct {
	ctx context.Context
	in  io.Reader
	fd  uintptr
}

func (in terminalInput) Read(p []byte) (int, error) {
	for {
		if err := in.ctx.Err(); err != nil {
			return 0, err
		}
		ready, err := C.task_readable(C.int(in.fd))
		if ready < 0 && !errors.Is(err, syscall.EINTR) {
			return 0, err
		}
		if ready > 0 {
			return in.in.Read(p)
		}
	}
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
