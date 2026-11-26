package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/git"
)

func TestMetadataRemainsUnsignedWithSigningPolicy(t *testing.T) {
	for _, scope := range []string{"global", "local"} {
		t.Run(scope, func(t *testing.T) {
			const globalChild = "TASKS_TEST_METADATA_GLOBAL_SIGNING_CHILD"
			if scope == "global" && os.Getenv(globalChild) != "1" {
				// libgit2 caches global config search paths at initialization.
				// Use a fresh process so earlier tests cannot select another home.
				home := t.TempDir()
				mustWrite(t, filepath.Join(home, ".gitconfig"), []byte("[commit]\ngpgSign = true\n[gpg]\nprogram = /nonexistent/signer\n"))
				t.Setenv("HOME", home)
				t.Setenv("XDG_CONFIG_HOME", home)
				t.Setenv("GIT_CONFIG_COUNT", "0")
				t.Setenv("GIT_CONFIG_PARAMETERS", "")
				t.Setenv(globalChild, "1")
				command := exec.Command(os.Args[0], "-test.run=^TestMetadataRemainsUnsignedWithSigningPolicy$/^global$")
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("global signing policy in fresh process: %v\n%s", err, output)
				}
				return
			}
			for key, value := range map[string]string{"GIT_AUTHOR_NAME": "Fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_NAME": "Fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid"} {
				t.Setenv(key, value)
			}
			repo, tasks, initial := fixture(t)
			if scope == "local" {
				if err := repo.SetLocalConfig("commit.gpgsign", "true"); err != nil {
					t.Fatal(err)
				}
			}
			configEntries, err := tasks.Repository.ConfigurationEntries()
			if err != nil {
				t.Fatal(err)
			}
			var enabled bool
			for _, entry := range slices.Backward(configEntries) {
				if entry.Name == "commit.gpgsign" {
					enabled, err = git.ConfigurationBool(entry)
					break
				}
			}
			if err != nil || !enabled {
				t.Fatalf("fixture did not request signing: %v %v", enabled, err)
			}
			for _, key := range []string{"GIT_AUTHOR_DATE", "GIT_COMMITTER_DATE"} {
				t.Setenv(key, "1700000000 +0000")
			}
			t.Setenv("PATH", "/nonexistent")
			revision, err := commitTestFiles(context.Background(), repo, map[string][]byte{"EXAMPLE-1 Unsigned.md": []byte("body\n")}, CommitOptions{FromEnvironment: true, Message: "unsigned metadata"})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := tasks.Repository.ExtractCommitSignature(revision); !git.HasCode(err, git.NotFound) {
				t.Fatalf("metadata signature: %v", err)
			}
			entries, err := tasks.Repository.Tree(revision)
			if err != nil {
				t.Fatal(err)
			}
			tree, err := tasks.Repository.WriteTree(entries)
			if err != nil {
				t.Fatal(err)
			}
			who := git.Signature{Name: "Fixture", Email: "fixture@example.invalid", When: time.Unix(1700000000, 0).UTC()}
			expected, err := tasks.Repository.WriteCommit(tree, []git.ID{initial}, who, who, "unsigned metadata\n")
			if err != nil || expected != revision {
				t.Fatalf("unsigned commit-tree identity: got %s want %s: %v", revision, expected, err)
			}
		})
	}
}

func TestRuntimeCommitSignaturesAndReflog(t *testing.T) {
	repo, tasks, initial := fixture(t)
	for key, value := range map[string]string{
		"GIT_AUTHOR_NAME": "Author", "GIT_AUTHOR_EMAIL": "author@example.invalid", "GIT_AUTHOR_DATE": "1700000000 +0230",
		"GIT_COMMITTER_NAME": "Committer", "GIT_COMMITTER_EMAIL": "committer@example.invalid", "GIT_COMMITTER_DATE": "1700000123 -0430",
	} {
		t.Setenv(key, value)
	}
	revision, err := commitTestFiles(context.Background(), repo, map[string][]byte{"EXAMPLE-1 Actors.md": []byte("body\n")}, CommitOptions{FromEnvironment: true, Message: "actors"})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := tasks.Repository.Tree(revision)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := tasks.Repository.WriteTree(entries)
	if err != nil {
		t.Fatal(err)
	}
	author := git.Signature{Name: "Author", Email: "author@example.invalid", When: time.Unix(1700000000, 0).In(time.FixedZone("", 9000))}
	committer := git.Signature{Name: "Committer", Email: "committer@example.invalid", When: time.Unix(1700000123, 0).In(time.FixedZone("", -16200))}
	expected, err := tasks.Repository.WriteCommit(tree, []git.ID{initial}, author, committer, "actors\n")
	if err != nil || expected != revision {
		t.Fatalf("commit signatures: got %s want %s: %v", revision, expected, err)
	}
	log := mustRead(t, filepath.Join(tasks.Paths.CommonDir, "logs", "refs", "heads", "tasks"))
	if !strings.Contains(string(log), "Committer <committer@example.invalid> 1700000123 -0430\tactors") {
		t.Fatalf("reflog: %s", log)
	}
}

func TestRuntimeInvalidSignatureDoesNotCreateSessionOrObjects(t *testing.T) {
	for _, key := range []string{"GIT_AUTHOR_DATE", "GIT_COMMITTER_DATE"} {
		t.Run(key, func(t *testing.T) {
			repo, tasks, _ := fixture(t)
			t.Setenv("GIT_AUTHOR_NAME", "Author")
			t.Setenv("GIT_AUTHOR_EMAIL", "author@example.invalid")
			t.Setenv("GIT_COMMITTER_NAME", "Committer")
			t.Setenv("GIT_COMMITTER_EMAIL", "committer@example.invalid")
			t.Setenv(key, "invalid-date")
			before := bootstrapSnapshot(t, filepath.Dir(tasks.Paths.CommonDir))
			if _, err := MetadataSignatures(repo); err == nil {
				t.Fatal("invalid date accepted by MetadataSignatures")
			}
			if !reflect.DeepEqual(before, bootstrapSnapshot(t, filepath.Dir(tasks.Paths.CommonDir))) {
				t.Fatal("identity refusal changed repository")
			}
			if _, err := os.Stat(filepath.Join(tasks.Paths.CommonDir, "tasks")); !os.IsNotExist(err) {
				t.Fatalf("session created: %v", err)
			}
			withSession(t, repo, func(session *Session) {
				afterBegin := bootstrapSnapshot(t, filepath.Dir(tasks.Paths.CommonDir))
				if _, err := session.CommitFiles(map[string][]byte{"EXAMPLE-1 Refused.md": []byte("body\n")}, CommitOptions{FromEnvironment: true, Message: "refused"}); err == nil {
					t.Fatal("invalid date accepted by CommitFiles")
				}
				if !reflect.DeepEqual(afterBegin, bootstrapSnapshot(t, filepath.Dir(tasks.Paths.CommonDir))) {
					t.Fatal("commit identity refusal changed repository")
				}
			})
		})
	}
}
