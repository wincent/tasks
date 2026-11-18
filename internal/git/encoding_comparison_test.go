package git

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func encodingComparisonFixture(t *testing.T) (*Repository, string, ID) {
	t.Helper()
	repo, root := fixture(t, false)
	tip := safetyTip(t, repo, ID{}, map[string]string{".gitattributes": "content.txt working-tree-encoding=UTF-16LE-BOM -text\n", "content.txt": "A"})
	publish(t, repo, "refs/heads/main", tip, ID{})
	if err := repo.SetLocalConfig("core.checkRoundtripEncoding", "UTF-16LE-BOM"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckoutMetadata(ID{}, tip); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "content.txt")
	writeFile(t, path, []byte("\xfe\xff\x00A"))
	if err := os.Chtimes(path, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	return repo, root, tip
}

func TestNativeEncodingComparisonDoesNotRelaxObjectWrites(t *testing.T) {
	repo, _, _ := encodingComparisonFixture(t)
	other, _, _ := encodingComparisonFixture(t)
	// Reuse the same OS thread to catch a comparison-context leak, including
	// interleaving another repository. The callback context is not configuration.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for range 4 {
		for _, current := range []*Repository{repo, other} {
			changes, err := current.Status(false)
			if err != nil || len(changes) != 0 {
				t.Fatalf("comparison should ignore a non-writing roundtrip: %+v %v", changes, err)
			}
			if _, err := current.WriteWorktreeBlob("content.txt", []byte("\xfe\xff\x00A")); err == nil || !strings.Contains(err.Error(), "roundtrip") {
				t.Fatalf("comparison relaxed an object write: %v", err)
			}
		}
	}
}

func TestNativeEncodingCheckoutComparisonRetainsStrictSmudge(t *testing.T) {
	repo, root, base := encodingComparisonFixture(t)
	next := safetyTip(t, repo, base, map[string]string{"content.txt": "B"})
	if err := repo.Checkout(base, next); err != nil {
		t.Fatal(err)
	}
	if got := string(readFile(t, filepath.Join(root, "content.txt"))); got != "\xff\xfeB\x00" {
		t.Fatalf("checkout smudge: %x", got)
	}
	if _, err := repo.WriteWorktreeBlob("content.txt", []byte("\xfe\xff\x00B")); err == nil || !strings.Contains(err.Error(), "roundtrip") {
		t.Fatalf("checkout comparison relaxed a later write: %v", err)
	}
}

func TestNativeEncodingInvalidComparisonStillDetectsChanges(t *testing.T) {
	repo, root, _ := encodingComparisonFixture(t)
	writeFile(t, filepath.Join(root, "content.txt"), []byte{0xff})
	changes, err := repo.Status(false)
	if err != nil || len(changes) != 1 || changes[0].Path != "content.txt" || changes[0].Status != WorktreeModified {
		t.Fatalf("invalid comparison must retain raw bytes and detect changes: %+v %v", changes, err)
	}
	if _, err := repo.WriteWorktreeBlob("content.txt", []byte{0xff}); err == nil {
		t.Fatal("comparison conversion fallback bypassed strict object conversion")
	}
}

func TestNativeEncodingImplicitRoundtripPolicyIsInvalid(t *testing.T) {
	_, root, _ := encodingComparisonFixture(t)
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.checkRoundtripEncoding'")
	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if _, err := repo.WriteWorktreeBlob("content.txt", []byte("\xff\xfeA\x00")); err == nil || !strings.Contains(err.Error(), "requires a value") {
		t.Fatalf("accepted valueless roundtrip policy: %v", err)
	}
	if _, err := repo.Status(false); err == nil || !strings.Contains(err.Error(), "requires a value") {
		t.Fatalf("comparison hid invalid configuration: %v", err)
	}
}
