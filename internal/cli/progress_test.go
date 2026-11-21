package cli

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/testutil"
)

func TestPiProgressAttentionPresentation(t *testing.T) {
	for _, tc := range []struct {
		name, term, noColor string
		tty, styled         bool
	}{
		{"terminal", "xterm", "", true, true},
		{"redirected", "xterm", "", false, false},
		{"no color", "xterm", "1", true, false},
		{"dumb terminal", "dumb", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TERM", tc.term)
			t.Setenv("NO_COLOR", tc.noColor)

			var buffer bytes.Buffer
			progress := piProgress{out: &buffer}
			readOutput := buffer.String
			if tc.tty {
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
				progress.out = slave
				readOutput = func() string {
					slave.Close()
					return strings.ReplaceAll(string(<-done), "\r\n", "\n")
				}
			}

			const ordinary = "Quoted Needs attention: in an ordinary summary."
			const attention = "Needs attention:\tA question.\n\nDetails."
			for _, summary := range []string{ordinary, attention} {
				if _, err := fmt.Fprintf(&progress, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`+"\n", summary); err != nil {
					t.Fatal(err)
				}
				if progress.summary != summary {
					t.Fatalf("recorded summary = %q, want %q", progress.summary, summary)
				}
			}
			want := ordinary + "\n" + attention + "\n"
			if tc.styled {
				want = ordinary + "\n\x1b[1;4mNeeds attention:\x1b[0m\tA question.\n\nDetails.\n"
			}
			if got := readOutput(); got != want {
				t.Errorf("progress output = %q, want %q", got, want)
			}
		})
	}
}
