package git

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These are independently specified byte fixtures, not output captured from a
// Git subprocess. No Git executable or additional encoding library is needed.
func TestNativeEncodingKnownBytes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		encoding string
		worktree string
		utf8     string
	}{
		{"ISO-8859-1", "caf\xe9\n", "café\n"},
		{"latin-1", "caf\xe9\n", "café\n"},
		{"WINDOWS-1252", "\x80\n", "€\n"},
		{"SHIFT-JIS", "\x82\xa0\n", "あ\n"},
		{"UTF-16LE", "A\x00\xe9\x00\n\x00", "Aé\n"},
		{"UTF-16BE", "\x00A\x00\xe9\x00\n", "Aé\n"},
		{"UTF-16LE-BOM", "\xff\xfeA\x00\xe9\x00\n\x00", "Aé\n"},
		{"UTF-32LE", "A\x00\x00\x00\xe9\x00\x00\x00\n\x00\x00\x00", "Aé\n"},
		{"UTF-32BE", "\x00\x00\x00A\x00\x00\x00\xe9\x00\x00\x00\n", "Aé\n"},
	} {
		t.Run(tt.encoding, func(t *testing.T) {
			t.Parallel()
			repo, root := fixture(t, false)
			attributes := "content.txt working-tree-encoding=" + tt.encoding + " -text\n"
			tip := safetyTip(t, repo, ID{}, map[string]string{".gitattributes": attributes, "content.txt": tt.utf8})
			publish(t, repo, "refs/heads/main", tip, ID{})
			checkoutInitial(t, repo, tip)
			if got := readFile(t, filepath.Join(root, "content.txt")); string(got) != tt.worktree {
				t.Fatalf("smudge: %x, want %x", got, tt.worktree)
			}
			id, err := repo.WriteWorktreeBlob("content.txt", []byte(tt.worktree))
			if err != nil {
				t.Fatal(err)
			}
			got, err := repo.Blob(id)
			if err != nil || string(got) != tt.utf8 {
				t.Fatalf("clean: %x %v, want %x", got, err, tt.utf8)
			}
		})
	}
}

func TestNativeEncodingBOMAndInvalidClean(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, encoding, data, want, errorText string
	}{
		{"utf16-le", "UTF-16", "\xff\xfeA\x00", "A", ""},
		{"utf16-be", "UTF-16", "\xfe\xff\x00A", "A", ""},
		{"utf32-le", "UTF-32", "\xff\xfe\x00\x00A\x00\x00\x00", "A", ""},
		{"utf32-be", "UTF-32", "\x00\x00\xfe\xff\x00\x00\x00A", "A", ""},
		{"empty-utf16", "UTF-16", "", "", ""},
		{"empty-unknown", "not-a-real-encoding", "", "", ""},
		{"required16", "UTF-16", "A\x00", "", "BOM is required"},
		{"required32", "utf32", "A\x00\x00\x00", "", "BOM is required"},
		{"prohibited16le", "UTF-16LE", "\xff\xfeA\x00", "", "BOM is prohibited"},
		{"prohibited16be", "utf16be", "\xff\xfeA\x00", "", "BOM is prohibited"},
		{"prohibited32le", "UTF-32LE", "\xff\xfe\x00\x00A\x00\x00\x00", "", "BOM is prohibited"},
		{"prohibited32be", "UTF-32BE", "\x00\x00\xfe\xff\x00\x00\x00A", "", "BOM is prohibited"},
		{"truncated", "UTF-16LE", "A", "", "invalid"},
		{"surrogate", "UTF-16LE", "\x00\xd8", "", "invalid"},
		{"invalid-scalar", "UTF-32LE", "\x00\x00\x11\x00", "", "invalid"},
		{"invalid-scalar-be", "UTF-32BE", "\x00\x11\x00\x00", "", "invalid"},
		{"surrogate32", "UTF-32LE", "\x00\xd8\x00\x00", "", "invalid"},
		{"max-scalar", "UTF-32LE", "\xff\xff\x10\x00", "\xf4\x8f\xbf\xbf", ""},
		{"unknown", "not-a-real-encoding", "A", "", "does not support"},
		// Git treats this spelling as UTF-16 on input, including BE BOMs.
		{"le-bom-accepts-be", "UTF-16LE-BOM", "\xfe\xff\x00A", "A", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo, root := fixture(t, false)
			writeFile(t, filepath.Join(root, ".gitattributes"), []byte("content.txt working-tree-encoding="+tt.encoding+" -text\n"))
			before := safetyFiles(t, root)
			id, err := repo.WriteWorktreeBlob("content.txt", []byte(tt.data))
			if tt.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errorText) || !strings.Contains(err.Error(), "content.txt") {
					t.Fatalf("expected %s for content.txt, got %v", tt.errorText, err)
				}
				if !reflect.DeepEqual(before, safetyFiles(t, root)) {
					t.Fatal("failed clean wrote repository state")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := repo.Blob(id)
			if err != nil || string(got) != tt.want {
				t.Fatalf("%x %v, want %x", got, err, tt.want)
			}
		})
	}
}

func TestNativeEncodingAttributeStates(t *testing.T) {
	t.Parallel()
	for _, attribute := range []string{"", "!working-tree-encoding", "working-tree-encoding=", "working-tree-encoding=UTF-8", "working-tree-encoding=uTf8", "working-tree-encoding", "-working-tree-encoding"} {
		t.Run(attribute, func(t *testing.T) {
			t.Parallel()
			repo, root := fixture(t, false)
			writeFile(t, filepath.Join(root, ".gitattributes"), []byte("content.txt -text "+attribute+"\n"))
			input := []byte{0xff, 0, '\r', '\n'} // UTF-8 is a bypass, not validation.
			id, err := repo.WriteWorktreeBlob("content.txt", input)
			if attribute == "working-tree-encoding" || attribute == "-working-tree-encoding" || attribute == "working-tree-encoding=" {
				if err == nil || !strings.Contains(err.Error(), "true/false and empty assignments") ||
					!strings.Contains(err.Error(), "working-tree-encoding=UTF-8") || !strings.Contains(err.Error(), "!working-tree-encoding") {
					t.Fatalf("expected boolean/empty encoding refusal with bypass guidance: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := repo.Blob(id)
			if err != nil || !bytes.Equal(got, input) {
				t.Fatalf("bypass: %x %v", got, err)
			}
		})
	}
}

func TestNativeEncodingRoundtripConfigurationIsPerHandle(t *testing.T) {
	t.Parallel()
	// Reading a BE BOM through UTF-16LE-BOM is valid, but writing it back
	// produces LE bytes. This provides a deterministic lossy roundtrip.
	for _, list := range []string{"", "SHIFT-JIS", "UTF-16LE", "XUTF-16LE-BOM", "utf-16le-bom", "ASCII,\tUTF-16LE-BOM, SHIFT-JIS"} {
		t.Run(list, func(t *testing.T) {
			t.Parallel()
			repo, root := fixture(t, false)
			writeFile(t, filepath.Join(root, ".gitattributes"), []byte("content.txt working-tree-encoding=UTF-16LE-BOM -text\n"))
			if err := repo.SetLocalConfig("core.checkRoundtripEncoding", list); err != nil {
				t.Fatal(err)
			}
			_, err := repo.WriteWorktreeBlob("content.txt", []byte("\xfe\xff\x00A"))
			wantError := list == "utf-16le-bom" || strings.HasPrefix(list, "ASCII,")
			if wantError {
				if err == nil || !strings.Contains(err.Error(), "roundtrip") {
					t.Fatalf("expected roundtrip error, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			other, otherRoot := fixture(t, false)
			writeFile(t, filepath.Join(otherRoot, ".gitattributes"), []byte("content.txt working-tree-encoding=UTF-16LE-BOM -text\n"))
			if _, err := other.WriteWorktreeBlob("content.txt", []byte("\xfe\xff\x00A")); err != nil {
				t.Fatalf("configuration leaked across handles: %v", err)
			}
		})
	}
}

func TestNativeEncodingCRLFIdentAndSymlink(t *testing.T) {
	t.Parallel()
	repo, root := fixture(t, false)
	attrs := "content.txt working-tree-encoding=UTF-16LE text eol=crlf ident\nlink working-tree-encoding=not-a-real-encoding\nplain.txt text eol=crlf\n"
	blob, err := repo.WriteBlob([]byte("$Id$\ncafé\n"))
	if err != nil {
		t.Fatal(err)
	}
	link, err := repo.WriteBlob([]byte("content.txt"))
	if err != nil {
		t.Fatal(err)
	}
	base := safetyTip(t, repo, ID{}, map[string]string{".gitattributes": attrs, "plain.txt": "plain\n"})
	tree, err := repo.EditTree(base, []TreeEdit{{Path: "content.txt", ID: blob, Mode: 0100644}, {Path: "link", ID: link, Mode: 0120000}})
	if err != nil {
		t.Fatal(err)
	}
	tip, err := repo.WriteCommit(tree, []ID{base}, testSignature, testSignature, "encoded fixture\n")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, repo, "refs/heads/main", tip, ID{})
	checkoutInitial(t, repo, tip)
	want := "$Id: " + blob.String() + " $\r\ncafé\r\n"
	var encoded []byte
	for _, r := range want {
		encoded = append(encoded, byte(r), byte(r>>8))
	}
	got := readFile(t, filepath.Join(root, "content.txt"))
	if !bytes.Equal(got, encoded) {
		t.Fatalf("CRLF/ident/encoding smudge ordering: %x, want %x", got, encoded)
	}
	cleaned, err := repo.WriteWorktreeBlob("content.txt", got)
	if err != nil || cleaned != blob {
		t.Fatalf("encoding/ident/CRLF clean ordering: %s %v, want %s", cleaned, err, blob)
	}
	if got := string(readFile(t, filepath.Join(root, "plain.txt"))); got != "plain\r\n" {
		t.Fatalf("unrelated native transform: %q", got)
	}
	if got, err := os.Readlink(filepath.Join(root, "link")); err != nil || got != "content.txt" {
		t.Fatalf("symlink transformed: %q %v", got, err)
	}
}

func TestNativeEncodingCheckoutFailurePreflight(t *testing.T) {
	t.Parallel()
	for _, data := range []string{"unrepresentable €\n", "invalid \xff\n", "overlong \xc0\xaf", "surrogate \xed\xa0\x80", "out-of-range \xf4\x90\x80\x80"} {
		t.Run(data, func(t *testing.T) {
			t.Parallel()
			repo, root := fixture(t, false)
			base := safetyTip(t, repo, ID{}, map[string]string{"a.txt": "before\n", "z.txt": "before\n"})
			publish(t, repo, "refs/heads/main", base, ID{})
			checkoutInitial(t, repo, base)
			next := safetyTip(t, repo, base, map[string]string{"a.txt": "after\n", "z.txt": data, ".gitattributes": "z.txt working-tree-encoding=ISO-8859-1\n"})
			publish(t, repo, "refs/heads/next", next, ID{})
			before := safetyFiles(t, root)
			if err := repo.Checkout(base, next); err == nil || !strings.Contains(err.Error(), "working-tree-encoding") {
				t.Fatalf("expected encoding failure, got %v", err)
			}
			if !reflect.DeepEqual(before, safetyFiles(t, root)) {
				t.Fatal("failed encoding checkout mutated files/index/objects/refs")
			}
			destination := filepath.Join(t.TempDir(), "encoded-worktree")
			if err := repo.AddWorktree("encoded", destination, "refs/heads/next"); err == nil || !strings.Contains(err.Error(), "working-tree-encoding") {
				t.Fatalf("expected worktree encoding failure, got %v", err)
			}
			if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatalf("failed preflight created worktree: %v", err)
			}
			if !reflect.DeepEqual(before, safetyFiles(t, root)) {
				t.Fatal("failed encoding preflight created worktree registration")
			}
		})
	}
}
