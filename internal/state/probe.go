//go:build darwin || linux

package state

import (
	"errors"
	"os"
	"syscall"
)

// Probe keeps a shared observation lock when no exclusive writer exists. It
// never creates or unlinks a file, and concurrent readers cannot masquerade as
// exclusive owners. Held describes the kernel lock, never PID liveness.
type Probe struct {
	Held bool
	file *os.File
}

func ProbeLock(path string) (*Probe, error) {
	return probeLock(path, syscall.LOCK_SH)
}

// ProbeExclusiveLock also detects shared owners, without creating the file.
func ProbeExclusiveLock(path string) (*Probe, error) {
	return probeLock(path, syscall.LOCK_EX)
}

func probeLock(path string, mode int) (*Probe, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return &Probe{}, nil
	}
	if err != nil {
		return nil, err
	}
	closeError := func(err error) (*Probe, error) { file.Close(); return nil, err }
	info, err := file.Stat()
	if err != nil {
		return closeError(err)
	}
	if !info.Mode().IsRegular() {
		return closeError(errors.New("lock probe requires a regular file"))
	}
	var lockErr error
	for {
		lockErr = syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB)
		if !errors.Is(lockErr, syscall.EINTR) {
			break
		}
	}
	held := errors.Is(lockErr, syscall.EWOULDBLOCK)
	if lockErr != nil && !held {
		return closeError(lockErr)
	}
	actual, err := os.Lstat(path)
	if err != nil {
		if !held {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		}
		return closeError(err)
	}
	if !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		if !held {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		}
		return closeError(errors.New("lock identity changed during probe"))
	}
	return &Probe{Held: held, file: file}, nil
}
func (probe *Probe) Close() error {
	if probe.file == nil {
		return nil
	}
	file := probe.file
	probe.file = nil
	if !probe.Held {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	}
	return file.Close()
}
