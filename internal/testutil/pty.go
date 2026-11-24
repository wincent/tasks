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
static int task_openpty(int *master, int *slave, unsigned short columns) {
 struct winsize size = {.ws_col = columns};
 if (openpty(master, slave, NULL, NULL, columns ? &size : NULL) < 0) return errno;
 return 0;
}
*/
import "C"

import (
	"os"
	"syscall"
)

func OpenPTY() (*os.File, *os.File, error) {
	return OpenPTYWithWidth(0)
}

func OpenPTYWithWidth(columns uint16) (*os.File, *os.File, error) {
	syscall.ForkLock.RLock()
	var master, slave C.int
	if code := C.task_openpty(&master, &slave, C.ushort(columns)); code != 0 {
		syscall.ForkLock.RUnlock()
		return nil, nil, syscall.Errno(code)
	}
	syscall.CloseOnExec(int(master))
	syscall.CloseOnExec(int(slave))
	syscall.ForkLock.RUnlock()
	if err := syscall.SetNonblock(int(master), true); err != nil {
		syscall.Close(int(master))
		syscall.Close(int(slave))
		return nil, nil, err
	}
	return os.NewFile(uintptr(master), "pty master"), os.NewFile(uintptr(slave), "pty slave"), nil
}

// ClosePTY closes the master before the slave. On Darwin, closing the slave
// first can stall while waiting for undrained output.
func ClosePTY(master, slave *os.File) {
	master.Close()
	slave.Close()
}
