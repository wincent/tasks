package git

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func detachedActors(t *testing.T) {
	t.Helper()
	clearSignatureEnvironment(t)
	for key, value := range map[string]string{
		"GIT_AUTHOR_NAME": "Detached Author", "GIT_AUTHOR_EMAIL": "author@example.invalid", "GIT_AUTHOR_DATE": "1700000000 +0230",
		"GIT_COMMITTER_NAME": "Detached Committer", "GIT_COMMITTER_EMAIL": "committer@example.invalid", "GIT_COMMITTER_DATE": "1700000123 -0430",
	} {
		t.Setenv(key, value)
	}
}

func TestDetachedWorktreeUsesFinalConfiguration(t *testing.T) {
	for _, mode := range []string{"branch-transform", "branch-filter", "local-branch-transform", "local-branch-filter", "frozen-gitdir"} {
		t.Run(mode, func(t *testing.T) {
			original, root := fixture(t, false)
			detachedActors(t)
			if err := original.SetLocalConfig("core.autocrlf", "false"); err != nil {
				t.Fatal(err)
			}
			if err := original.SetLocalConfig("core.logAllRefUpdates", "true"); err != nil {
				t.Fatal(err)
			}
			tip := safetyTip(t, original, ID{}, map[string]string{".gitattributes": "*.txt text filter=future\n", "file.txt": "line\n"})
			// The explicit target is not HEAD: planning must use its actual tree.
			publish(t, original, "refs/heads/main", commit(t, original, "source"), ID{})
			paths, err := original.Paths()
			if err != nil {
				t.Fatal(err)
			}
			include := filepath.Join(t.TempDir(), "candidate.config")
			data := "[core]\nautocrlf = true\n"
			if strings.HasSuffix(mode, "branch-filter") {
				data = "[filter \"future\"]\nsmudge = must-not-run\nrequired = true\n"
			}
			if err := os.WriteFile(include, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			key := "includeIf.onbranch:task/child.path"
			if mode == "frozen-gitdir" {
				key = "includeIf.gitdir:" + filepath.Join(paths.CommonDir, "worktrees", "child") + ".path"
			}
			if strings.HasPrefix(mode, "local-") {
				injectConfiguration(t)
				if err := original.SetLocalConfig(key, include); err != nil {
					t.Fatal(err)
				}
			} else {
				injectConfiguration(t, [2]string{key, include})
			}
			repo, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			destination := filepath.Join(t.TempDir(), "child")
			var prepare func() error
			if mode == "frozen-gitdir" {
				prepare = func() error {
					// Simulate replacement after admission: a fresh command-include
					// parse would now select opposite line endings.
					return os.WriteFile(include, []byte("[core]\nautocrlf = false\n"), 0600)
				}
			}
			if err := repo.AddDetachedWorktree("child", destination, tip, prepare); err != nil {
				t.Fatal(err)
			}
			want := "line\n"
			if mode == "frozen-gitdir" {
				want = "line\r\n"
			}
			if got := string(readFile(t, filepath.Join(destination, "file.txt"))); got != want {
				t.Fatalf("checkout = %q, want %q", got, want)
			}
			child, err := Open(destination)
			if err != nil {
				t.Fatal(err)
			}
			defer child.Close()
			head, err := child.Head()
			if err != nil || head.Branch != "" || head.ID != tip {
				t.Fatalf("HEAD: %+v %v", head, err)
			}
			entries, err := child.IndexEntries()
			if err != nil || len(entries) != 2 {
				t.Fatalf("index: %+v %v", entries, err)
			}
			for _, entry := range entries {
				if entry.Stage != 0 {
					t.Fatalf("unmerged index: %+v", entry)
				}
				if entry.Path == "file.txt" && entry.ID != mergePreflightBlobID("line\n") {
					t.Fatalf("incorrect index blob: %+v", entry)
				}
			}
			refs, err := repo.References("refs/heads/task/")
			if err != nil || len(refs) != 0 {
				t.Fatalf("transient refs: %+v %v", refs, err)
			}
			childPaths, err := child.Paths()
			if err != nil {
				t.Fatal(err)
			}
			log := string(readFile(t, filepath.Join(childPaths.GitDir, "logs", "HEAD")))
			if !strings.Contains(log, "Detached Committer <committer@example.invalid> 1700000123 -0430\tdetach integration candidate") || strings.Contains(log, "Detached Author") {
				t.Fatalf("HEAD reflog actor: %q", log)
			}
			actors, err := child.Signatures()
			if err != nil || actors.Author.Name != "Detached Author" || actors.Committer.Name != "Detached Committer" || actors.Author.When.Unix() != 1700000000 || actors.Committer.When.Unix() != 1700000123 {
				t.Fatalf("actors: %+v %v", actors, err)
			}
		})
	}
}

func TestDetachedWorktreeAdmissionIsReadOnly(t *testing.T) {
	for _, mode := range []string{"invalid-name", "missing-email", "filter", "encoding"} {
		t.Run(mode, func(t *testing.T) {
			original, root := fixture(t, false)
			clearSignatureEnvironment(t)
			for key, value := range map[string]string{"user.name": "Source Actor", "user.email": "source@example.invalid", "core.autocrlf": "false"} {
				if err := original.SetLocalConfig(key, value); err != nil {
					t.Fatal(err)
				}
			}
			attrs, data, want := "", "[user]\nname =\n", "identity"
			switch mode {
			case "missing-email":
				data = "[user]\nemail =\n"
			case "filter":
				attrs, data, want = "*.txt filter=future\n", "[filter \"future\"]\nsmudge = must-not-run\n", "external filter"
			case "encoding":
				attrs, data, want = "*.txt working-tree-encoding=NOT-A-CHARSET\n", "[core]\nautocrlf = false\n", "encoding"
			}
			tip := safetyTip(t, original, ID{}, map[string]string{".gitattributes": attrs, "file.txt": "line\n"})
			paths, err := original.Paths()
			if err != nil {
				t.Fatal(err)
			}
			include := filepath.Join(t.TempDir(), "candidate.config")
			if err := os.WriteFile(include, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			injectConfiguration(t, [2]string{"includeIf.gitdir:" + filepath.Join(paths.CommonDir, "worktrees", "child") + ".path", include})
			repo, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if _, err := repo.Signatures(); err != nil {
				t.Fatalf("source identity: %v", err)
			}
			parent := filepath.Join(root, "new-parent")
			before := mergePreflightSnapshot(t, root)
			prepared := false
			err = repo.AddDetachedWorktree("child", filepath.Join(parent, "child"), tip, func() error { prepared = true; return os.Mkdir(parent, 0755) })
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("expected %s refusal: %v", want, err)
			}
			if prepared {
				t.Fatal("parent creation preceded admission")
			}
			if !reflect.DeepEqual(before, mergePreflightSnapshot(t, root)) {
				t.Fatal("refusal changed checkout, refs, registration, index or ODB")
			}
		})
	}
}

func TestDetachedWorktreePreservesCollisions(t *testing.T) {
	for _, mode := range []string{"path", "ref", "registration", "raced-path"} {
		t.Run(mode, func(t *testing.T) {
			repo, root := fixture(t, false)
			detachedActors(t)
			tip := commit(t, repo, "target")
			parent := t.TempDir()
			destination := filepath.Join(parent, "child")
			paths, err := repo.Paths()
			if err != nil {
				t.Fatal(err)
			}
			occupied := destination
			if mode == "registration" {
				occupied = filepath.Join(paths.CommonDir, "worktrees", "child")
			}
			populate := func() error {
				if err := os.MkdirAll(occupied, 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(occupied, "sentinel"), []byte("unowned\n"), 0600)
			}
			var prepare func() error
			switch mode {
			case "ref":
				publish(t, repo, "refs/heads/task/child", tip, ID{})
			case "raced-path":
				prepare = populate
			default:
				if err := populate(); err != nil {
					t.Fatal(err)
				}
			}
			before := mergePreflightSnapshot(t, root, parent)
			if err := repo.AddDetachedWorktree("child", destination, tip, prepare); err == nil {
				t.Fatal("accepted collision")
			}
			if mode == "path" {
				if !reflect.DeepEqual(before, mergePreflightSnapshot(t, root, parent)) {
					t.Fatal("collision changed existing inputs")
				}
			} else if mode == "ref" {
				expectRef(t, repo, "refs/heads/task/child", tip)
				if _, err := os.Lstat(destination); !os.IsNotExist(err) {
					t.Fatalf("destination exists: %v", err)
				}
			} else if got := string(readFile(t, filepath.Join(occupied, "sentinel"))); got != "unowned\n" {
				t.Fatal("discarded unowned content")
			}
		})
	}
}
