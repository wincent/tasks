package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/wincent/tasks/internal/git"
)

func TestCleanupApplyHint(t *testing.T) {
	t.Parallel()
	const (
		branch       = "task/example-1"
		wouldRemove  = "Would remove branch " + branch + "\n"
		singularHint = "Run `tasks cleanup --apply` to perform removal.\n"
		pluralHint   = "Run `tasks cleanup --apply` to perform removals.\n"
	)
	for _, tc := range []struct {
		name             string
		args             []string
		redirectStderr   bool
		checkout, keep   bool
		wantOut, wantErr string
	}{
		{name: "single removal", wantOut: wouldRemove, wantErr: singularHint},
		{name: "multiple removals", checkout: true, wantOut: wouldRemove, wantErr: pluralHint},
		{name: "redirected stderr", redirectStderr: true, wantOut: wouldRemove},
		{name: "no matches", args: []string{"2"}},
		{name: "all kept", keep: true, wantOut: "Keep branch " + branch + ": symbolic branch refs are protected\n"},
		{name: "apply", args: []string{"--apply"}, wantOut: "Removed branch " + branch + "\n"},
		{name: "numbered", args: []string{"1"}, wantOut: wouldRemove, wantErr: singularHint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, root := fixture(t)
			if tc.keep {
				systemGit(t, root, "symbolic-ref", "refs/heads/"+branch, mainRef)
			} else {
				setRef(t, repo, "refs/heads/"+branch, fixtureRevision(t, repo, mainRef), git.ID{})
			}
			wantOut := tc.wantOut
			if tc.checkout {
				path := filepath.Join(filepath.Dir(root), "checkout")
				systemGit(t, root, "worktree", "add", path, branch)
				wantOut = "Would remove " + path + "\n" + wantOut
			}

			var out, diagnostic bytes.Buffer
			streams := IO{Out: &out, Err: &diagnostic}
			readOut, readErr := out.String, diagnostic.String
			if tc.redirectStderr {
				// A terminal on stdout must not enable the stderr hint.
				streams.Out, readOut = sizedTerminalOutput(t, 80)
			} else {
				streams.Err, readErr = sizedTerminalOutput(t, 80)
			}
			if err := Run(context.Background(), root, append([]string{"cleanup"}, tc.args...), streams); err != nil {
				t.Fatal(err)
			}
			if got := readOut(); got != wantOut {
				t.Errorf("stdout = %q, want %q", got, wantOut)
			}
			if got := readErr(); got != tc.wantErr {
				t.Errorf("stderr = %q, want %q", got, tc.wantErr)
			}
		})
	}
}
