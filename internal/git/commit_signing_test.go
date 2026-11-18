package git

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestCommitSigningPublicAPIsExactBytesAndNoReferences(t *testing.T) {
	repo, _ := fixture(t, false)
	first := commit(t, repo, "first")
	second := commit(t, repo, "second", first)
	tree, err := repo.WriteTree(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetLocalConfig("commit.gpgsign", "true"); err != nil {
		t.Fatal(err)
	}
	payload, err := repo.CommitBuffer(tree, []ID{first, second}, testSignature, testSignature, "message\n\nbody\n")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("tree %s\nparent %s\nparent %s\nauthor Task Test <task@example.invalid> 1700000000 +0000\ncommitter Task Test <task@example.invalid> 1700000000 +0000\n\nmessage\n\nbody\n", tree, first, second)
	if string(payload) != want {
		t.Fatalf("payload: %q != %q", payload, want)
	}
	// Protocol fixture only: native APIs must not interpret cryptography.
	sig := []byte("-----BEGIN PGP SIGNATURE-----\nc2ln\n-----END PGP SIGNATURE-----\n")
	id, err := repo.WriteSignedCommit(payload, sig)
	if err != nil {
		t.Fatal(err)
	}
	actual, original, err := repo.ExtractCommitSignature(id)
	if err != nil || !bytes.Equal(actual, sig) || !bytes.Equal(original, payload) {
		t.Fatalf("extract: %q %q %v", actual, original, err)
	}
	for i, parent := range []ID{first, second} {
		got, err := repo.ResolveCommit(fmt.Sprintf("%s^%d", id, i+1))
		if err != nil || got != parent {
			t.Fatalf("parent: %s %v", got, err)
		}
	}
	refs, err := repo.References("refs/")
	if err != nil || len(refs) != 0 {
		t.Fatalf("unexpected refs: %v %v", refs, err)
	}
	unsigned, err := repo.WriteCommit(tree, []ID{first, second}, testSignature, testSignature, "message\n\nbody\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.ExtractCommitSignature(unsigned); !HasCode(err, NotFound) {
		t.Fatalf("plumbing signed unexpectedly: %v", err)
	}
	if _, err := repo.WriteSignedCommit(payload, nil); err == nil {
		t.Fatal("accepted empty signature")
	}
	if _, err := repo.WriteSignedCommit(payload, []byte("signature\x00suffix")); err == nil {
		t.Fatal("accepted signature NUL")
	}
	if _, err := repo.CommitBuffer(tree, nil, testSignature, testSignature, "message\x00suffix"); err == nil {
		t.Fatal("accepted message NUL")
	}
	if strings.Contains(string(payload), "gpgsig") {
		t.Fatal("payload mutated")
	}
}
