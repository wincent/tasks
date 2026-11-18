package tail

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"
)

func decode(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	return value, nil
}

func ValidateHeader(line []byte, worktree, sessionID string) error {
	value, err := decode(line)
	if err != nil {
		return errors.New("invalid saved Pi session header")
	}
	header, ok := value.(map[string]any)
	if !ok {
		return errors.New("invalid saved Pi session header")
	}
	id, ok := header["id"].(string)
	if !ok || id == "" || header["type"] != "session" {
		return errors.New("invalid saved Pi session header")
	}
	cwd, ok := header["cwd"].(string)
	if !ok || !filepath.IsAbs(cwd) {
		return errors.New("invalid saved Pi session header")
	}
	// New launches record the same canonical checkout in both places.
	// Compare those paths directly so logs stay readable after cleanup.
	if filepath.Clean(cwd) != filepath.Clean(worktree) {
		actual, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			return err
		}
		expected, err := filepath.EvalSymlinks(worktree)
		if err != nil {
			return err
		}
		if actual != expected {
			return errors.New("saved Pi session belongs to another worktree")
		}
	}
	if sessionID != "" && id != sessionID {
		return errors.New("saved Pi session does not match the active Pi session ID")
	}
	return nil
}

// Follow consumes the header then a fixed initial window of complete physical
// lines. It follows the same file identity after task completion; it never picks
// a different session based on timestamps or the newest record.
func Follow(ctx context.Context, file *os.File, worktree, sessionID string, color bool, out, diagnostic io.Writer) error {
	identity, err := file.Stat()
	if err != nil {
		return err
	}
	reader := bufio.NewReader(io.NewSectionReader(file, 0, identity.Size()))
	header, err := reader.ReadBytes('\n')
	if err != nil {
		return errors.New("invalid saved Pi session header")
	}
	if err := ValidateHeader(header, worktree, sessionID); err != nil {
		return err
	}
	start := int64(len(header))
	offset := start
	count := 0
	offsets := make([]int64, 0, HistoryLines)
	for offset < identity.Size() {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return err
		}
		if len(line) == 0 || line[len(line)-1] != '\n' {
			break
		}
		if len(offsets) == HistoryLines {
			copy(offsets, offsets[1:])
			offsets = offsets[:HistoryLines-1]
		}
		offsets = append(offsets, offset)
		count++
		offset += int64(len(line))
	}
	if len(offsets) > 0 {
		start = offsets[0]
	}
	skipped := count - len(offsets)
	lineNumber := 1 + skipped
	renderer := Renderer{Color: color}
	if skipped > 0 {
		if _, err := fmt.Fprintln(out, renderer.paint(fmt.Sprintf("[showing recent session activity; skipped %d lines]", skipped), "2")+"\n"); err != nil {
			return err
		}
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return err
	}
	reader = bufio.NewReader(file)
	offset = start
	pending := []byte{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, readErr := reader.ReadBytes('\n')
		offset += int64(len(line))
		pending = append(pending, line...)
		if len(pending) > 0 && pending[len(pending)-1] == '\n' {
			lineNumber++
			entry, err := decode(pending)
			if err != nil {
				if _, err := fmt.Fprintf(diagnostic, "tasks tail: skipping malformed session record at line %d\n", lineNumber); err != nil {
					return err
				}
			} else if rendered := renderer.Entry(entry); rendered != "" {
				if _, err := fmt.Fprintln(out, rendered+"\n"); err != nil {
					return err
				}
			}
			pending = pending[:0]
		}
		if readErr == nil {
			continue
		}
		if readErr != io.EOF {
			return readErr
		}
		current, err := os.Stat(file.Name())
		if err != nil {
			return err
		}
		if !os.SameFile(identity, current) {
			return errors.New("saved Pi session was replaced; restart tasks tail to inspect it")
		}
		if current.Size() < offset {
			return errors.New("saved Pi session was truncated; restart tasks tail to inspect it")
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
