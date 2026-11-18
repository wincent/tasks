//go:build linux

package testutil

/*
#include <errno.h>
#include <stddef.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <unistd.h>
static int task_deny_exec(void) {
 struct sock_filter instructions[] = {
  BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
  BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, SYS_execve, 0, 1),
  BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | EPERM),
  BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, SYS_execveat, 0, 1),
  BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | EPERM),
  BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
 };
 struct sock_fprog program = {sizeof(instructions)/sizeof(instructions[0]), instructions};
 if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) < 0) return errno;
 // Synchronize the filter across all Go runtime threads.
 if (syscall(SYS_seccomp, SECCOMP_SET_MODE_FILTER, SECCOMP_FILTER_FLAG_TSYNC, &program) != 0) return errno ? errno : EBUSY;
 return 0;
}
*/
import "C"

import "syscall"

// DenyExec restricts only the calling disposable test process, on every thread.
func DenyExec() error {
	if code := C.task_deny_exec(); code != 0 {
		return syscall.Errno(code)
	}
	return nil
}
