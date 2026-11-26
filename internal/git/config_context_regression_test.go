package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProspectiveConfigurationNormalGlobalContext(t *testing.T) {
	if os.Getenv("TASKTOOL_PROSPECTIVE_GLOBAL_CHILD") != "1" {
		home := t.TempDir()
		writeCommandInclude(t, filepath.Join(home, ".gitconfig"), "[tasktool]\nsource = global\n[includeIf \"onbranch:tasks\"]\npath = future\n")
		writeCommandInclude(t, filepath.Join(home, "future"), "[core]\nautocrlf = true\n[tasktool]\nfuture = global-conditional\n")
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
		t.Setenv("TASKTOOL_PROSPECTIVE_GLOBAL_CHILD", "1")
		injectConfiguration(t)
		t.Setenv("GIT_CONFIG_PARAMETERS", "")
		command := exec.Command(os.Args[0], "-test.run=^TestProspectiveConfigurationNormalGlobalContext$")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("normal global context: %v\n%s", err, output)
		}
		return
	}
	repo, _ := fixture(t, false)
	if err := repo.SetLocalConfig("core.autocrlf", "false"); err != nil {
		t.Fatal(err)
	}
	if err := repo.WithProspectiveWorktree("child", "refs/heads/tasks", func(view *Repository) error {
		for key, want := range map[string]string{"tasktool.source": "global", "tasktool.future": "global-conditional", "core.autocrlf": "false"} {
			got, err := view.Config(key)
			if err != nil || got != want {
				t.Fatalf("%s: %q %v; want %q", key, got, err, want)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProspectiveConfigurationFromLinkedWorktree(t *testing.T) {
	t.Parallel()
	repo, _ := fixture(t, false)
	tip := safetyTip(t, repo, ID{}, map[string]string{"file.txt": "line\n"})
	publish(t, repo, "refs/heads/parent", tip, ID{})
	parentPath := filepath.Join(t.TempDir(), "parent")
	if err := repo.AddWorktree("parent", parentPath, "refs/heads/parent"); err != nil {
		t.Fatal(err)
	}
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetLocalConfig("extensions.worktreeConfig", "true"); err != nil {
		t.Fatal(err)
	}
	writeCommandInclude(t, filepath.Join(paths.CommonDir, "worktrees", "parent", "config.worktree"), "[tasktool]\nparentOnly = must-not-inherit\n")
	future := filepath.Join(t.TempDir(), "future")
	writeCommandInclude(t, future, "[tasktool]\nfuture = child\n")
	if err := repo.SetLocalConfig("includeIf.gitdir:"+filepath.Join(paths.CommonDir, "worktrees", "child")+".path", future); err != nil {
		t.Fatal(err)
	}
	linked, err := Open(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer linked.Close()
	if err := linked.WithProspectiveWorktree("child", "refs/heads/tasks", func(view *Repository) error {
		if got, err := view.Config("tasktool.future"); err != nil || got != "child" {
			t.Fatalf("common-dir future context: %q %v", got, err)
		}
		if got, err := view.Config("tasktool.parentonly"); !HasCode(err, NotFound) {
			t.Fatalf("inherited parent's worktree config: %q %v", got, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProspectiveConfigurationWildcardEdges(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		pattern, subject string
		want             bool
	}{
		{`a/\*/c`, "a/*/c", true},
		{`a/**\/c`, "a/c", true},
		{`a/**\/c`, "a/b/c", true},
		{`a/\?/c`, "a/?/c", true},
		{`a/\[x\]/c`, "a/[x]/c", true},
		{`a/[\]]/c`, "a/]/c", true},
		{`a/[]x]/c`, "a/]/c", true},
		{`a/[[:digit:]]/c`, "a/2/c", true},
		{`a/[[:unknown:]]/c`, "a/2/c", false},
		{`a/[x/c`, "a/x/c", false},
		{`a/[]/c`, "a/]/c", false},
		{"a/c\\", "a/c", false},
		{"a/*/c", "a//c", true},
		{"a/**b/c", "a/x/y/b/c", false},
		{"a/**/c", "a/x/y/c/extra", false},
	} {
		got, err := configurationWildmatch(test.pattern, test.subject, false)
		if err != nil || got != test.want {
			t.Errorf("%q %q: %v %v; want %v", test.pattern, test.subject, got, err, test.want)
		}
	}
	context := configurationContext{gitdir: "/repo/.git/worktrees/child", branch: "refs/heads/topic/child"}
	for _, test := range []struct {
		condition, origin string
		want              bool
	}{
		{"gitdir:./worktrees/child", "/repo/.git/config", true},
		{"gitdir/i:/REPO/.GIT/WORKTREES/CHILD", "", true},
		{"gitdir:worktrees/", "", true},
		{"gitdir:/repo/.git/worktrees/[", "", false},
		{"onbranch:topic/", "", true},
		{"onbranch:topic/*", "", true},
		{"onbranch:topic/[", "", false},
		{"onbranch:topic/child\\", "", false},
	} {
		got, err := context.matches(test.condition, test.origin)
		if err != nil || got != test.want {
			t.Errorf("%q: %v %v; want %v", test.condition, got, err, test.want)
		}
	}
}
