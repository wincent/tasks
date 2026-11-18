// Package testutil contains native fixture adapters, not production backends.
package testutil

/*
#cgo linux LDFLAGS: -lutil
#if defined(__APPLE__)
#include <util.h>
#else
#include <pty.h>
#endif
#include <errno.h>
static int task_openpty(int *master, int *slave) {
 if (openpty(master, slave, NULL, NULL, NULL) < 0) return errno;
 return 0;
}
*/
import "C"

import (
	"os"
	"syscall"
)

func OpenPTY() (*os.File, *os.File, error) {
	var master, slave C.int
	if code := C.task_openpty(&master, &slave); code != 0 {
		return nil, nil, syscall.Errno(code)
	}
	if err := syscall.SetNonblock(int(master), true); err != nil {
		syscall.Close(int(master))
		syscall.Close(int(slave))
		return nil, nil, err
	}
	syscall.CloseOnExec(int(master))
	syscall.CloseOnExec(int(slave))
	return os.NewFile(uintptr(master), "pty master"), os.NewFile(uintptr(slave), "pty slave"), nil
}
