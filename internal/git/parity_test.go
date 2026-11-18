package git

import (
	"strings"
	"testing"
)

func TestOpenRejectsAmbientRepositoryOverrides(t *testing.T) {
	_, root := fixture(t, false)
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_CONFIG_COUNT", "GIT_NAMESPACE", "GIT_OBJECT_DIRECTORY"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "not-the-requested-repository")
			repo, err := Open(root)
			if repo != nil {
				repo.Close()
			}
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("ignored %s: %v", key, err)
			}
		})
	}
}

func TestConfiguredRemoteNamesNeedNoTransport(t *testing.T) {
	repo, _ := fixture(t, false)
	for key, value := range map[string]string{
		"remote.origin.url": "ssh://unresolvable-alias.invalid/project",
		"remote.backup.url": "https://unreachable.invalid/project",
		"credential.helper": "unsupported-helper",
	} {
		if err := repo.SetLocalConfig(key, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"GIT_SSH", "GIT_SSH_COMMAND", "GIT_ASKPASS", "SSH_ASKPASS"} {
		t.Setenv(key, "not-an-executable")
	}
	names, err := repo.Remotes()
	if err != nil || len(names) != 2 {
		t.Fatalf("names=%v error=%v", names, err)
	}
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
	}
	if !seen["origin"] || !seen["backup"] {
		t.Fatal(names)
	}
}
