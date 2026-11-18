// Package terminal contains the small POSIX terminal adapter shared by command
// rendering and interactive editing. It does not install signal handlers.
package terminal

/*
#include <unistd.h>
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

func Color(out io.Writer) bool {
	_, disabled := os.LookupEnv("NO_COLOR")
	return IsTerminal(out) && !disabled && os.Getenv("TERM") != "dumb"
}
