package git

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeEncodingGenericBOMSmudge(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		encoding string
		le, be   []byte
	}{
		{"UTF-16", []byte{0xff, 0xfe, 'A', 0}, []byte{0xfe, 0xff, 0, 'A'}},
		{"UTF-32", []byte{0xff, 0xfe, 0, 0, 'A', 0, 0, 0}, []byte{0, 0, 0xfe, 0xff, 0, 0, 0, 'A'}},
	} {
		t.Run(tt.encoding, func(t *testing.T) {
			t.Parallel()
			repo, root := fixture(t, false)
			tip := safetyTip(t, repo, ID{}, map[string]string{
				".gitattributes": "content.txt working-tree-encoding=" + tt.encoding + " -text\n",
				"content.txt":    "A",
			})
			publish(t, repo, "refs/heads/main", tip, ID{})
			if err := repo.CheckoutMetadata(ID{}, tip); err != nil {
				t.Fatal(err)
			}
			// Git preserves the host iconv's endian choice, but requires a BOM.
			got := readFile(t, filepath.Join(root, "content.txt"))
			if !bytes.Equal(got, tt.le) && !bytes.Equal(got, tt.be) {
				t.Fatalf("missing/incorrect BOM or encoded content: %x", got)
			}
			id, err := repo.WriteWorktreeBlob("content.txt", got)
			if err != nil {
				t.Fatal(err)
			}
			clean, err := repo.Blob(id)
			if err != nil || string(clean) != "A" {
				t.Fatalf("BOM roundtrip: %x %v", clean, err)
			}
		})
	}
}

func TestNativeEncodingLargeAndStatefulContent(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, encoding, utf8, encoded string
	}{
		{"buffer-growth", "UTF-32LE", strings.Repeat("A", 100000), strings.Repeat("A\x00\x00\x00", 100000)},
		{"stateful", "ISO-2022-JP", "あ\n", "\x1b$B$\x22\x1b(B\n"},
		// Git does not flush a state-reset escape at EOF.
		{"stateful-eof", "ISO-2022-JP", "あ", "\x1b$B$\x22"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo, root := fixture(t, false)
			tip := safetyTip(t, repo, ID{}, map[string]string{
				".gitattributes": "content.txt working-tree-encoding=" + tt.encoding + " -text\n",
				"content.txt":    tt.utf8,
			})
			publish(t, repo, "refs/heads/main", tip, ID{})
			if err := repo.CheckoutMetadata(ID{}, tip); err != nil {
				t.Fatal(err)
			}
			got := readFile(t, filepath.Join(root, "content.txt"))
			if string(got) != tt.encoded {
				t.Fatalf("smudge mismatch: got %d bytes, want %d", len(got), len(tt.encoded))
			}
			id, err := repo.WriteWorktreeBlob("content.txt", got)
			if err != nil {
				t.Fatal(err)
			}
			clean, err := repo.Blob(id)
			if err != nil || string(clean) != tt.utf8 {
				t.Fatalf("clean mismatch: got %d bytes, want %d: %v", len(clean), len(tt.utf8), err)
			}
		})
	}
}
