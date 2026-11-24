package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCommandInclude(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCommandIncludePathContext(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "relative", "../relative", "~other/config"} {
		if _, err := commandIncludePath(&value); err == nil {
			t.Fatalf("accepted context-free relative path %q", value)
		}
	}
	if _, err := commandIncludePath(nil); err == nil {
		t.Fatal("accepted missing include value")
	}
	for _, value := range []string{"/absolute/config", "~/config"} {
		if got, err := commandIncludePath(&value); err != nil || got != value {
			t.Fatalf("include path %q: %q %v", value, got, err)
		}
	}
}

func TestCommandIncludeFilePrecedenceAndIsolation(t *testing.T) {
	original, root := fixture(t, false)
	if err := original.SetLocalConfig("tasktool.value", "local"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	included := filepath.Join(dir, "included")
	writeCommandInclude(t, filepath.Join(dir, "nested"), "[tasktool]\nvalue = nested\nempty =\n")
	writeCommandInclude(t, filepath.Join(dir, "branch"), "[tasktool]\nbranch = matched\n")
	writeCommandInclude(t, included, "[tasktool]\nvalue = included\n[include]\npath = nested\n[includeIf \"onbranch:main\"]\npath = branch\n")
	injectConfiguration(t, [2]string{"tasktool.value", "before"}, [2]string{"include.path", included})
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	opened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	for key, want := range map[string]string{"tasktool.value": "nested", "tasktool.empty": "", "tasktool.branch": "matched", "include.path": "nested"} {
		if got, err := opened.Config(key); err != nil || got != want {
			t.Fatalf("%s: got %q %v, want %q", key, got, err, want)
		}
	}
	if got, err := original.Config("tasktool.value"); err != nil || got != "local" {
		t.Fatalf("other handle changed: %q %v", got, err)
	}
	t.Setenv("GIT_CONFIG_PARAMETERS", "'tasktool.value'='after'")
	later, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer later.Close()
	if got, err := later.Config("tasktool.value"); err != nil || got != "after" {
		t.Fatalf("parameters did not override included config: %q %v", got, err)
	}
	if got, err := opened.Config("tasktool.value"); err != nil || got != "nested" {
		t.Fatalf("existing handle changed: %q %v", got, err)
	}
}

func TestCommandIncludeParametersMissingFileAndMalformedFile(t *testing.T) {
	_, root := fixture(t, false)
	path := filepath.Join(t.TempDir(), "secret-config-name")
	injectConfiguration(t)
	t.Setenv("GIT_CONFIG_PARAMETERS", "'include.path'="+quoteConfigurationParameter(path))
	repo, err := Open(root)
	if err != nil {
		t.Fatal("missing include should be ignored", err)
	}
	repo.Close()
	writeCommandInclude(t, path, "[tasktool]\nvalue = from-parameters\n")
	repo, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Config("tasktool.value"); err != nil || got != "from-parameters" {
		t.Fatalf("parameter include: %q %v", got, err)
	}
	repo.Close()
	writeCommandInclude(t, path, "[secret-invalid-section\nsecret-value\n")
	created := filepath.Join(t.TempDir(), "not-created")
	if repo, err := Init(created, "", false); err == nil {
		repo.Close()
		t.Fatal("accepted malformed include")
	} else if strings.Contains(err.Error(), "secret") {
		t.Fatal("include diagnostic leaked contents or path", err)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatal("malformed unconditional include created repository", err)
	}
}

func TestGitConfigCommandFileDoesNotRedirectRepositoryConfig(t *testing.T) {
	original, root := fixture(t, false)
	if err := original.SetLocalConfig("tasktool.value", "local"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config-command-only")
	writeCommandInclude(t, path, "malformed and must not be read\n")
	for _, value := range []string{"", path} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("GIT_CONFIG", value)
			repo, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if got, err := repo.Config("tasktool.value"); err != nil || got != "local" {
				t.Fatalf("GIT_CONFIG affected ordinary configuration: %q %v", got, err)
			}
		})
	}
}
