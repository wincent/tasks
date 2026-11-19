package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/testutil"
)

func TestGlowProcess(t *testing.T) {
	if os.Getenv("TASK_TEST_GLOW_HELPER") != "1" {
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(os.Getenv("TASK_TEST_GLOW_CAPTURE"), data, 0600); err != nil {
		os.Exit(4)
	}
	if os.Args[len(os.Args)-1] != "-" {
		os.Exit(5)
	}
	if os.Getenv("TASK_TEST_GLOW_MODE") == "create" {
		cwd, _ := os.Getwd()
		if err := Run(context.Background(), cwd, []string{"create", "Concurrent", "--body", "renderer was running"}, IO{In: strings.NewReader(""), Out: io.Discard, Err: os.Stderr}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(6)
		}
	}
	if os.Getenv("TASK_TEST_GLOW_MODE") == "fail" {
		os.Exit(7)
	}
	fmt.Fprintln(os.Stdout, "rendered marker")
	os.Exit(0)
}
func outputTerminal(t *testing.T, root string, args ...string) (string, string, error) {
	t.Helper()
	return captureTerminal(t, root, false, args...)
}

// With a shared terminal, capture the editor and command output in order.
func captureTerminal(t *testing.T, root string, shared bool, args ...string) (string, string, error) {
	t.Helper()
	master, slave, err := testutil.OpenPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err := master.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(master); done <- data }()
	var diagnostic bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	streams := IO{Out: slave, Err: &diagnostic}
	if shared {
		streams.In, streams.Err = slave, slave
	}
	err = Run(ctx, root, args, streams)
	slave.Close()
	data := <-done
	return strings.ReplaceAll(string(data), "\r\n", "\n"), diagnostic.String(), err
}
func TestRealGlowInvocationFallbackAndUnlockedMetadata(t *testing.T) {
	for _, mode := range []string{"render", "latest", "create", "fail", "spawn-failure", "unavailable", "raw", "pipe", "missing"} {
		t.Run(mode, func(t *testing.T) {
			_, root := fixture(t)
			path := strings.TrimSpace(run(t, root, "create", "Rendering", "--body", "text"))
			content := "not a valid task status\n# Exact Markdown without a final newline"
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			tools := t.TempDir()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			capture := filepath.Join(t.TempDir(), "capture")
			t.Setenv("TASK_TEST_GLOW_HELPER", "1")
			t.Setenv("TASK_TEST_GLOW_CAPTURE", capture)
			t.Setenv("TASK_TEST_GLOW_MODE", mode)
			t.Setenv("PATH", tools)
			script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run=^TestGlowProcess$ -- \"$@\"\n"
			if mode == "spawn-failure" {
				script = "invalid executable without an interpreter\n"
			}
			if mode != "unavailable" {
				if err := os.WriteFile(filepath.Join(tools, "glow"), []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"show", "1"}
			if mode == "latest" {
				args = []string{"show"}
			}
			if mode == "raw" {
				args = append(args, "--raw")
			}
			if mode == "missing" {
				args[1] = "2"
			}
			var output, diagnostic string
			if mode == "pipe" {
				output, diagnostic, err = invoke(root, "", args...)
			} else {
				output, diagnostic, err = outputTerminal(t, root, args...)
			}
			if mode == "missing" {
				if err == nil {
					t.Fatal("missing task accepted")
				}
			} else if err != nil {
				t.Fatalf("%v %s", err, diagnostic)
			}
			data, readErr := os.ReadFile(capture)
			invoked := mode == "render" || mode == "latest" || mode == "create" || mode == "fail"
			if invoked {
				if readErr != nil || string(data) != content {
					t.Fatalf("renderer input %q %v", data, readErr)
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatal("renderer unexpectedly invoked", readErr)
			}
			if mode == "render" || mode == "latest" || mode == "create" {
				if output != "rendered marker\n" {
					t.Fatal(output)
				}
			} else if mode != "missing" && output != content {
				t.Fatalf("raw fallback changed content %q", output)
			}
			if mode == "fail" || mode == "spawn-failure" {
				if !strings.Contains(diagnostic, "glow failed") {
					t.Fatal(diagnostic)
				}
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != content {
				t.Fatal("show modified task", err)
			}
		})
	}
}

func TestActualTerminalListOverviewAndJSONColorPolicy(t *testing.T) {
	for _, policy := range []struct {
		term, noColor string
		color         bool
	}{{"xterm", "", true}, {"dumb", "", false}, {"xterm", "1", false}} {
		t.Run(policy.term+policy.noColor, func(t *testing.T) {
			_, root := fixture(t)
			path := strings.TrimSpace(run(t, root, "create", "Edited", "--body", "details"))
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(original, []byte("manual edit\n")...), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), "EXAMPLE-9 Manual.md"), []byte("Status: open\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TERM", policy.term)
			t.Setenv("NO_COLOR", policy.noColor)
			for _, args := range [][]string{{"list"}, {}, {"search", "edited"}} {
				output, diagnostic, err := outputTerminal(t, root, args...)
				if err != nil {
					t.Fatalf("%v %s", err, diagnostic)
				}
				if strings.Contains(output, "\x1b[") != policy.color {
					t.Fatal("color policy", output)
				}
				edited := "EXAMPLE-1\topen\tEdited (M)\n"
				manual := "EXAMPLE-9\topen\tManual (?)\n"
				if policy.color {
					edited = "\x1b[4mEXAMPLE-1\x1b[0m\topen\t\x1b[3mEdited\x1b[0m (\x1b[31mM\x1b[0m)\n"
					manual = "\x1b[4mEXAMPLE-9\x1b[0m\topen\t\x1b[3mManual\x1b[0m (\x1b[34m?\x1b[0m)\n"
				}
				expectManual := len(args) == 0 || args[0] == "list"
				if !strings.Contains(output, edited) || strings.Contains(output, manual) != expectManual {
					t.Fatalf("unexpected listing %q", output)
				}
			}
			output, diagnostic, err := outputTerminal(t, root, "list", "--json")
			if piped := run(t, root, "list", "--json"); err != nil || output != piped {
				t.Fatalf("JSON terminal %v %q %q; want %q", err, output, diagnostic, piped)
			}
			if !strings.Contains(output, `"title": "Edited"`) || strings.Contains(output, `"change"`) {
				t.Fatal(output)
			}
		})
	}
}
