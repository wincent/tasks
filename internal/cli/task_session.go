package cli

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/tail"
)

type taskSession struct {
	Worktree    string  `json:"worktree"`
	Branch      string  `json:"branch,omitempty"`
	SessionID   *string `json:"session_id,omitempty"`
	SessionFile string  `json:"session_file,omitempty"`
	Reason      string  `json:"reason,omitempty"`
	Revision    string  `json:"revision,omitempty"`
	Summary     string  `json:"summary,omitempty"`
}

func taskSessionPath(directory string, n int) string {
	return filepath.Join(directory, fmt.Sprintf("claim-%d.json", n))
}

func readTaskSession(directory string, n int) (*taskSession, error) {
	data, err := readRegular(taskSessionPath(directory, n))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record taskSession
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(record.Worktree) || (record.SessionFile != "" && !filepath.IsAbs(record.SessionFile)) {
		return nil, fmt.Errorf("invalid saved checkout or session in %s", filepath.Base(taskSessionPath(directory, n)))
	}
	return &record, nil
}

func writeTaskSession(directory string, n int, record taskSession) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if record.SessionFile != "" && record.Branch != "" {
		if err := state.AtomicWrite(checkoutSessionPath(directory, record.Worktree, record.Branch), data); err != nil {
			return err
		}
	}
	return state.AtomicWrite(taskSessionPath(directory, n), data)
}

func checkoutSessionPath(directory, worktree, branch string) string {
	key := sha256.Sum256([]byte(worktree + "\x00" + branch))
	return filepath.Join(directory, fmt.Sprintf("checkout-%x.json", key))
}

func openRegular(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("not a regular file: %s", path)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func readRegular(path string) ([]byte, error) {
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// Older launches kept an exact checkout-to-session pointer. Read it when
// continuing existing work; never search by timestamp.
func legacySession(directory, worktree string, n int) (string, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(worktree)
	var quoted strings.Builder
	for _, r := range strings.TrimSuffix(b.String(), "\n") {
		switch {
		case r <= 127:
			quoted.WriteRune(r)
		case r <= 0xffff:
			fmt.Fprintf(&quoted, "\\u%04x", r)
		default:
			a, b := utf16.EncodeRune(r)
			fmt.Fprintf(&quoted, "\\u%04x\\u%04x", a, b)
		}
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("[%s, %d]", quoted.String(), n)))
	data, err := readRegular(filepath.Join(directory, fmt.Sprintf("session-%x.launch", hash)))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(data))
	if !filepath.IsAbs(path) || filepath.Dir(path) != filepath.Join(directory, "sessions") {
		return "", errors.New("invalid saved task session path")
	}
	return path, nil
}

func openSession(record taskSession) (*os.File, error) {
	file, err := openRegular(record.SessionFile)
	if err != nil {
		return nil, err
	}
	header, err := bufio.NewReader(file).ReadBytes('\n')
	id := ""
	if record.SessionID != nil {
		id = *record.SessionID
	}
	if err == nil {
		err = tail.ValidateHeader(header, record.Worktree, id)
	}
	if err == nil {
		_, err = file.Seek(0, 0)
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func newSessionDirectory(scope string) (string, error) {
	agentDir := os.Getenv("PI_CODING_AGENT_DIR")
	if agentDir == "" || agentDir == "~" || strings.HasPrefix(agentDir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locate Pi session store: %w", err)
		}
		switch {
		case agentDir == "":
			agentDir = filepath.Join(home, ".pi", "agent")
		case agentDir == "~":
			agentDir = home
		default:
			agentDir = filepath.Join(home, agentDir[2:])
		}
	}
	if !filepath.IsAbs(agentDir) {
		// Relative overrides are resolved from Pi's launch directory.
		agentDir = filepath.Join(scope, agentDir)
	}
	// Pi's all-projects picker scans the immediate subdirectories of sessions.
	return filepath.Join(agentDir, "sessions", "dev.wincent.tasks"), nil
}

func prepareSession(directory, scope, root, branch string, n int, saved *taskSession) (taskSession, error) {
	record := taskSession{Worktree: root, Branch: branch}
	if saved != nil && saved.Worktree == root && (saved.Branch == "" || saved.Branch == branch) {
		record.SessionFile, record.SessionID = saved.SessionFile, saved.SessionID
	} else {
		data, err := readRegular(checkoutSessionPath(directory, root, branch))
		if err == nil {
			var previous taskSession
			if err := json.Unmarshal(data, &previous); err != nil {
				return record, err
			}
			if previous.Worktree != root || previous.Branch != branch || !filepath.IsAbs(previous.SessionFile) {
				return record, errors.New("saved session identifies a different task checkout")
			}
			record.SessionFile, record.SessionID = previous.SessionFile, previous.SessionID
		} else if !errors.Is(err, os.ErrNotExist) {
			return record, err
		}
	}
	var err error
	if record.SessionFile == "" {
		record.SessionFile, err = legacySession(directory, root, n)
		if err != nil {
			return record, err
		}
	}
	if record.SessionFile != "" {
		file, err := openSession(record)
		if err == nil {
			return record, file.Close()
		}
		if !errors.Is(err, os.ErrNotExist) {
			return record, fmt.Errorf("open saved Pi session: %w", err)
		}
	}

	path, err := newSessionDirectory(scope)
	if err != nil {
		return record, err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return record, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return record, err
	}
	if !info.IsDir() {
		return record, fmt.Errorf("session directory is not a real directory: %s", path)
	}
	hex, err := token()
	if err != nil {
		return record, err
	}
	id := hex[:8] + "-" + hex[8:12] + "-4" + hex[13:16] + "-a" + hex[17:20] + "-" + hex[20:]
	record.SessionID = &id
	record.SessionFile = filepath.Join(path, id+".jsonl")
	f, err := os.OpenFile(record.SessionFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return record, err
	}
	err = json.NewEncoder(f).Encode(map[string]any{"type": "session", "version": 3, "id": id, "cwd": root, "timestamp": time.Now().UTC().Format(time.RFC3339Nano)})
	return record, errors.Join(err, f.Close())
}

func lockHeld(path string) (bool, error) {
	p, err := state.ProbeLock(path)
	if err != nil {
		return false, err
	}
	return p.Held, p.Close()
}

func runLockPath(directory string, n int) string {
	return filepath.Join(directory, fmt.Sprintf("run-%d.lock", n))
}

func running(directory string, prefix store.Prefix, n int, saved *taskSession) (bool, error) {
	if saved != nil && saved.Branch == "" {
		// Older launchers only held the task-wide lifetime lock.
		return lockHeld(taskLockPath(directory, prefix, n))
	}
	return lockHeld(runLockPath(directory, n))
}
