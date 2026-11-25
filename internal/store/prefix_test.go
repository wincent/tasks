package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
)

func TestParseConfigValid(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		data string
		want Config
	}{
		{
			name: "prefix only defaults to main",
			data: "prefix: DEMO\n",
			want: Config{Prefix: "DEMO", DefaultTargetBranch: "main"},
		},
		{
			name: "prefix and default_target_branch",
			data: "prefix: APP2\ndefault_target_branch: master\n",
			want: Config{Prefix: "APP2", DefaultTargetBranch: "master"},
		},
		{
			name: "reversed key order with slash branch",
			data: "default_target_branch: release/v1\nprefix: REL\n",
			want: Config{Prefix: "REL", DefaultTargetBranch: "release/v1"},
		},
		{
			name: "quoted keys values comments blank lines and CRLF",
			data: "\r\n# Repository configuration\r\n'prefix': 'RUN' # ticket prefix\r\n\"default_target_branch\": \"trunk\" # primary branch\r\n",
			want: Config{Prefix: "RUN", DefaultTargetBranch: "trunk"},
		},
		{
			name: "single quote escape and inline hash without space",
			data: "prefix: \"DEMO\"\ndefault_target_branch: 'feat''s/branch'\n",
			want: Config{Prefix: "DEMO", DefaultTargetBranch: "feat's/branch"},
		},
		{
			name: "plain inline comment with tab",
			data: "prefix: DEMO\t# tab comment\ndefault_target_branch: main\t# explicit default\n",
			want: Config{Prefix: "DEMO", DefaultTargetBranch: "main"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseConfig([]byte(tc.data))
			if err != nil {
				t.Fatalf("parseConfig(%q): %v", tc.data, err)
			}
			if got != tc.want {
				t.Fatalf("parseConfig(%q) = %+v, want %+v", tc.data, got, tc.want)
			}
		})
	}
}

func TestParseConfigInvalid(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{"empty", "", "missing prefix"},
		{"comments only", "# nothing here\n\n", "missing prefix"},
		{"missing prefix with target", "default_target_branch: master\n", "missing prefix"},
		{"duplicate prefix", "prefix: A\nprefix: B\n", "duplicate key"},
		{"duplicate target", "prefix: A\ndefault_target_branch: main\ndefault_target_branch: master\n", "duplicate key"},
		{"unknown key", "prefix: A\nother: value\n", "unknown key"},
		{"lowercase prefix", "prefix: demo\n", "invalid prefix"},
		{"prefix with dash", "prefix: DEMO-\n", "invalid prefix"},
		{"prefix starting with digit", "prefix: 1DEMO\n", "invalid prefix"},
		{"empty target branch", "prefix: A\ndefault_target_branch: \"\"\n", "invalid default_target_branch"},
		{"refs prefix target", "prefix: A\ndefault_target_branch: refs/heads/master\n", "invalid default_target_branch"},
		{"tasks branch target", "prefix: A\ndefault_target_branch: tasks\n", "invalid default_target_branch"},
		{"TASKS case-insensitive target", "prefix: A\ndefault_target_branch: TASKS\n", "invalid default_target_branch"},
		{"reserved task namespace", "prefix: A\ndefault_target_branch: task/demo-1\n", "invalid default_target_branch"},
		{"reserved Task namespace uppercase", "prefix: A\ndefault_target_branch: Task/custom\n", "invalid default_target_branch"},
		{"HEAD target", "prefix: A\ndefault_target_branch: HEAD\n", "invalid default_target_branch"},
		{"caret target", "prefix: A\ndefault_target_branch: master^\n", "invalid default_target_branch"},
		{"leading dash target", "prefix: A\ndefault_target_branch: -master\n", "invalid default_target_branch"},
		{"space in target", "prefix: A\ndefault_target_branch: two words\n", "invalid default_target_branch"},
		{"list syntax", "prefix: [A, B]\n", "unsupported YAML value syntax"},
		{"indented key", "  prefix: A\n", "unexpected indentation"},
		{"missing space after colon", "prefix:A\n", "expected whitespace"},
		{"trailing text after quoted scalar", "prefix: \"A\" extra\n", "unexpected trailing characters"},
		{"hash without space after quoted scalar", "prefix: \"A\"#comment\n", "unexpected trailing characters"},
		{"unterminated single quote", "prefix: 'A\n", "unterminated single-quoted scalar"},
		{"unterminated double quote", "prefix: \"A\n", "unterminated double-quoted scalar"},
		{"invalid utf8", "prefix: A\n# \xff\n", "UTF-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseConfig([]byte(tc.data))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), Branch) {
				t.Fatalf("parseConfig(%q) error = %v; want substring %q and %q", tc.data, err, tc.want, Branch)
			}
		})
	}
}

func TestLoadConfigValidation(t *testing.T) {
	t.Parallel()
	repo, err := git.Init(filepath.Join(t.TempDir(), "repo"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	// Missing refs/heads/tasks returns ErrMissingLocalHistory.
	if _, err := LoadConfig(repo); !errors.Is(err, ErrMissingLocalHistory) {
		t.Fatalf("missing tasks branch: got %v, want ErrMissingLocalHistory", err)
	}

	// Empty refs/heads/tasks (no .tasks.yml).
	emptyTree, err := repo.WriteTree(nil)
	if err != nil {
		t.Fatal(err)
	}
	emptyCommit, err := repo.WriteCommit(emptyTree, nil, signature, signature, "empty tasks\n")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, repo, Branch, emptyCommit, git.ID{})
	if _, err := LoadConfig(repo); err == nil || !strings.Contains(err.Error(), ".tasks.yml is missing from refs/heads/tasks") {
		t.Fatalf("missing .tasks.yml: %v", err)
	}

	// Non-regular .tasks.yml (symlink).
	targetBlob, err := repo.WriteBlob([]byte("other.yml"))
	if err != nil {
		t.Fatal(err)
	}
	symlinkTree, err := repo.WriteTree([]git.TreeEntry{{Name: ".tasks.yml", ID: targetBlob, Mode: 0120000}})
	if err != nil {
		t.Fatal(err)
	}
	symlinkCommit, err := repo.WriteCommit(symlinkTree, []git.ID{emptyCommit}, signature, signature, "symlink config\n")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, repo, Branch, symlinkCommit, emptyCommit)
	if _, err := LoadConfig(repo); err == nil || !strings.Contains(err.Error(), ".tasks.yml on refs/heads/tasks must be a regular file") {
		t.Fatalf("symlink .tasks.yml: %v", err)
	}

	// Valid .tasks.yml.
	cfgBlob, err := repo.WriteBlob([]byte("prefix: DEMO\ndefault_target_branch: master\n"))
	if err != nil {
		t.Fatal(err)
	}
	cfgTree, err := repo.WriteTree([]git.TreeEntry{{Name: ".tasks.yml", ID: cfgBlob, Mode: 0100644}})
	if err != nil {
		t.Fatal(err)
	}
	cfgCommit, err := repo.WriteCommit(cfgTree, []git.ID{symlinkCommit}, signature, signature, "valid config\n")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, repo, Branch, cfgCommit, symlinkCommit)
	cfg, err := LoadConfig(repo)
	if err != nil {
		t.Fatalf("valid .tasks.yml: %v", err)
	}
	if cfg != (Config{Prefix: "DEMO", DefaultTargetBranch: "master"}) {
		t.Fatalf("LoadConfig() = %+v", cfg)
	}
}

func TestTopicPreservesMain(t *testing.T) {
	t.Parallel()
	got, err := Topic("# DEMO-1: Title\n\nStatus: open\nTopic: main\n\n## Description\n\nBody\n")
	if err != nil || got != "main" {
		t.Fatalf("Topic(Topic: main) = %q, %v; want %q", got, err, "main")
	}
	got, err = Topic("# DEMO-1: Title\n\nStatus: open\n\n## Description\n\nBody\n")
	if err != nil || got != "" {
		t.Fatalf("Topic(omitted) = %q, %v; want empty", got, err)
	}
}

func TestOrphanHistoryCheckUsesDefaultTargetBranch(t *testing.T) {
	t.Parallel()
	repo, err := git.Init(filepath.Join(t.TempDir(), "repo"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	// Create refs/heads/master (no refs/heads/main).
	emptyTree, err := repo.WriteTree(nil)
	if err != nil {
		t.Fatal(err)
	}
	master, err := repo.WriteCommit(emptyTree, nil, signature, signature, "master\n")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, repo, "refs/heads/master", master, git.ID{})

	// Make refs/heads/tasks descend from master with default_target_branch: master.
	cfgBlob, err := repo.WriteBlob([]byte("prefix: DEMO\ndefault_target_branch: master\n"))
	if err != nil {
		t.Fatal(err)
	}
	cfgTree, err := repo.WriteTree([]git.TreeEntry{{Name: ".tasks.yml", ID: cfgBlob, Mode: 0100644}})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := repo.WriteCommit(cfgTree, []git.ID{master}, signature, signature, "shared history\n")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, repo, Branch, shared, git.ID{})

	if _, err := Inspect(repo, "DEMO"); err == nil || !strings.Contains(err.Error(), "tasks shares implementation history with master") {
		t.Fatalf("Inspect shared with master: %v", err)
	}
	withSession(t, repo, func(session *Session) {
		if opened, err := session.Ensure(signature); err == nil || !strings.Contains(err.Error(), "tasks shares implementation history with master") {
			if opened != nil {
				opened.Close()
			}
			t.Fatalf("Ensure shared with master: %v", err)
		}
	})

	// Replace refs/heads/tasks with a true orphan commit configuring master.
	orphan, err := repo.WriteCommit(cfgTree, nil, signature, signature, "orphan tasks\n")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, repo, Branch, orphan, shared)

	withSession(t, repo, func(session *Session) {
		opened, err := session.Ensure(signature)
		if err != nil {
			t.Fatalf("Ensure orphan with master: %v", err)
		}
		opened.Close()
	})

	// Open also checks independence against master (not main).
	publish(t, repo, "refs/heads/master", orphan, master)
	if opened, err := Open(repo); err == nil || !strings.Contains(err.Error(), "tasks shares implementation history with master") {
		if opened != nil {
			opened.Close()
		}
		t.Fatalf("Open shared with master: %v", err)
	}
	publish(t, repo, "refs/heads/master", master, orphan)
	opened, err := Open(repo)
	if err != nil {
		t.Fatalf("Open independent from master: %v", err)
	}
	opened.Close()
}
