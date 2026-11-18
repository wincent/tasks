package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessHelper(t *testing.T) {
	mode := os.Getenv("TASK_TEST_PROCESS_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "success":
		fmt.Fprintln(os.Stdout, "stdout diagnostic")
		fmt.Fprintln(os.Stderr, "stderr diagnostic")
		os.Exit(0)
	case "failure":
		os.Exit(7)
	case "signal":
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		os.Exit(9)
	case "wait":
		time.Sleep(time.Hour)
	}
	os.Exit(8)
}
func TestNativeProcessExitSignalsAndCancellation(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "failure", "signal", "wait"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if mode == "wait" {
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
			}
			var out, diagnostic bytes.Buffer
			result := (Native{}).Run(ctx, Command{Args: []string{binary, "-test.run=TestProcessHelper"}, Environment: append(os.Environ(), "TASK_TEST_PROCESS_HELPER="+mode), Out: &out, Err: &diagnostic, Group: true, RequireGroupExit: true})
			want := map[string]int{"success": 0, "failure": 7, "signal": -int(syscall.SIGTERM), "wait": -int(syscall.SIGTERM)}[mode]
			if result.Code != want {
				t.Fatalf("%+v want %d", result, want)
			}
			if mode == "success" && (!strings.Contains(out.String(), "stdout diagnostic") || !strings.Contains(diagnostic.String(), "stderr diagnostic")) {
				t.Fatal(out.String(), diagnostic.String())
			}
		})
	}
}
func TestProcessErrorsKeepDiagnostics(t *testing.T) {
	result := (Native{}).Run(context.Background(), Command{Args: []string{"/nonexistent/task-validator"}})
	if result.Code != 127 || result.Err == nil {
		t.Fatal(result)
	}
	err := Failure([]string{"tool", "argument with spaces"}, Result{Code: 7}, "actual stdout failure", "informational stderr")
	for _, value := range []string{"exit 7", "'argument with spaces'", "stdout:\nactual stdout failure", "stderr:\ninformational stderr"} {
		if !strings.Contains(err.Error(), value) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result = (Native{}).Run(ctx, Command{Args: []string{"never-start"}})
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatal(result)
	}
}
