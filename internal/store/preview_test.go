package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
)

func TestPreviewReadsNativeSmudgedTargetBytes(t *testing.T) {
	for _, encoding := range []bool{false, true} {
		t.Run(map[bool]string{false: "crlf", true: "utf16"}[encoding], func(t *testing.T) {
			repo, err := git.Init(filepath.Join(t.TempDir(), "repository"), "", false)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			for key, value := range map[string]string{"user.name": signature.Name, "user.email": signature.Email} {
				if err := repo.SetLocalConfig(key, value); err != nil {
					t.Fatal(err)
				}
			}
			attr := "EXAMPLE-* text eol=crlf\n"
			if encoding {
				attr = "EXAMPLE-* working-tree-encoding=UTF-16LE\n"
			}
			var entries []git.TreeEntry
			for name, content := range map[string]string{
				".gitattributes":       attr,
				"EXAMPLE-1 Preview.md": "# EXAMPLE-1: Preview\n\nStatus: open\n",
			} {
				blob, err := repo.WriteBlob([]byte(content))
				if err != nil {
					t.Fatal(err)
				}
				entries = append(entries, git.TreeEntry{Name: name, ID: blob, Mode: 0100644})
			}
			tree, err := repo.WriteTree(entries)
			if err != nil {
				t.Fatal(err)
			}
			tip, err := repo.WriteCommit(tree, nil, signature, signature, "tasks\n")
			if err != nil {
				t.Fatal(err)
			}
			publish(t, repo, Branch, tip, git.ID{})
			session, err := Begin(context.Background(), repo)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			preview, err := session.Preview("EXAMPLE")
			if err != nil {
				t.Fatal(err)
			}
			defer preview.Close()
			task, readErr := preview.Read(1)
			// UTF-16LE ASCII happens to be valid UTF-8 (embedded NULs); the
			// preview must preserve those bytes, not substitute canonical text.
			if readErr != nil {
				t.Fatal(readErr)
			}
			if encoding {
				if !strings.ContainsRune(task.Content, 0) {
					t.Fatal("preview skipped native encoding")
				}
			} else if !strings.Contains(task.Content, "\r\n") {
				t.Fatal("preview skipped native CRLF")
			}
			if _, err := os.Lstat(preview.Path); !os.IsNotExist(err) {
				t.Fatalf("preview created checkout: %v", err)
			}
			opened, err := session.Ensure(signature)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			actual, err := os.ReadFile(task.Path)
			if err != nil || string(actual) != task.Content {
				t.Fatalf("preview differs from native checkout: %q %v", actual, err)
			}
		})
	}
}
