package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"strings"

	"github.com/wincent/tasks/internal/terminal"
)

type programStatusState string

const (
	programStatusWorking programStatusState = "working"
	programStatusDone    programStatusState = "done"
	programStatusError   programStatusState = "error"
	programStatusClear   programStatusState = "clear"
)

// programStatus emits OSC 7501 program status escape sequences wrapped in tmux
// DCS passthrough when running non-interactive task runs on an interactive
// terminal inside tmux.
type programStatus struct {
	out     io.Writer
	id      string
	enabled bool
	active  bool
	last    string
}

func newProgramStatus(streams IO) *programStatus {
	if !terminal.IsTerminal(streams.Out) || streams.getenv("TMUX") == "" || streams.getenv("TERM") == "dumb" {
		return &programStatus{}
	}
	var token [16]byte
	_, _ = rand.Read(token[:])
	return &programStatus{
		out:     streams.Out,
		id:      "tasks/" + hex.EncodeToString(token[:]),
		enabled: true,
	}
}

func (s *programStatus) working() {
	if s == nil || !s.enabled {
		return
	}
	s.active = true
	s.write(programStatusWorking)
}

func (s *programStatus) finish(ctx context.Context, err error) {
	if s == nil || !s.active {
		return
	}
	s.active = false
	switch {
	case err == nil:
		s.write(programStatusDone)
	case ctx.Err() != nil:
		s.write(programStatusClear)
	default:
		s.write(programStatusError)
	}
}

// clearIfActive removes an in-flight working record if unwinding unexpectedly
// before finish runs.
func (s *programStatus) clearIfActive() {
	if s == nil || !s.active {
		return
	}
	s.active = false
	s.write(programStatusClear)
}

func (s *programStatus) write(state programStatusState) {
	if s == nil || !s.enabled {
		return
	}
	report := "\x1b]7501;state=" + string(state) + ":id=" + s.id + ":app=tasks\x1b\\"
	if report == s.last {
		return
	}
	sequence := "\x1bPtmux;" + strings.ReplaceAll(report, "\x1b", "\x1b\x1b") + "\x1b\\"
	if _, err := io.WriteString(s.out, sequence); err == nil {
		s.last = report
	}
}
