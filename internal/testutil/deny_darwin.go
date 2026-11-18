//go:build darwin

package testutil

/*
#include <sandbox.h>
#include <stdlib.h>
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
static int task_deny_exec(char **error) {
 return sandbox_init("(version 1)(allow default)(deny process-exec)", 0, error);
}
static void task_free_sandbox_error(char *message) { sandbox_free_error(message); }
#pragma clang diagnostic pop
*/
import "C"

import "fmt"

// DenyExec restricts only the calling disposable test process. Unlike PATH
// removal, it also denies absolute paths and interpreter execution.
func DenyExec() error {
	var message *C.char
	if C.task_deny_exec(&message) != 0 {
		defer C.task_free_sandbox_error(message)
		return fmt.Errorf("cannot establish test exec denial: %s", C.GoString(message))
	}
	return nil
}
