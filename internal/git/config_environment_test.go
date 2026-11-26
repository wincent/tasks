package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type configurationEntry struct{ name, value string }

func (r *Repository) configEntries(prefix string) ([]configurationEntry, error) {
	all, err := r.ConfigurationEntries()
	if err != nil {
		return nil, err
	}
	var entries []configurationEntry
	for _, entry := range all {
		if strings.HasPrefix(entry.Name, prefix) {
			entries = append(entries, configurationEntry{entry.Name, entry.Value})
		}
	}
	return entries, nil
}

func injectConfiguration(t *testing.T, entries ...[2]string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(len(entries)))
	for i, entry := range entries {
		t.Setenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", i), entry[0])
		t.Setenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", i), entry[1])
	}
}

func TestInjectedConfigurationPrecedenceAndHandleIsolation(t *testing.T) {
	original, root := fixture(t, false)
	if err := original.SetLocalConfig("tasktool.value", "local"); err != nil {
		t.Fatal(err)
	}
	paths, err := original.Paths()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(paths.CommonDir, "config")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	injectConfiguration(t, [2]string{"TaskTool.Value", "first"}, [2]string{"tasktool.value", "last\nvalue='literal'"}, [2]string{"TaskTool.Case=Sensitive.Value", "subsection"}, [2]string{"tasktool.empty", ""})
	opened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	for key, want := range map[string]string{"tasktool.value": "last\nvalue='literal'", "tasktool.Case=Sensitive.value": "subsection", "tasktool.empty": ""} {
		for i := 0; i < 16; i++ {
			if got, e := opened.Config(key); e != nil || got != want {
				t.Fatalf("%s: %q %v, want %q", key, got, e, want)
			}
		}
	}
	if _, err := opened.Config("tasktool.case=sensitive.value"); !HasCode(err, NotFound) {
		t.Fatal("subsection case was folded", err)
	}
	entries, err := opened.configEntries("tasktool.value")
	if err != nil {
		t.Fatal(err)
	}
	want := []configurationEntry{{"tasktool.value", "local"}, {"tasktool.value", "first"}, {"tasktool.value", "last\nvalue='literal'"}}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("ordered multivars %#v want %#v", entries, want)
	}
	if got, err := original.Config("tasktool.value"); err != nil || got != "local" {
		t.Fatalf("other handle changed: %q %v", got, err)
	}
	if after, err := os.ReadFile(configPath); err != nil || !bytes.Equal(before, after) {
		t.Fatal("injected configuration rewrote disk", err)
	}
	// Writes still target the local file, not the immutable command overlay.
	if err := opened.SetLocalConfig("tasktool.value", "new local"); err != nil {
		t.Fatal(err)
	}
	if got, err := opened.Config("tasktool.value"); err != nil || got != "last\nvalue='literal'" {
		t.Fatal("local write displaced command configuration", got, err)
	}
	injectConfiguration(t)
	plain, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if got, err := plain.Config("tasktool.value"); err != nil || got != "new local" {
		t.Fatal("local write was lost", got, err)
	}
	if got, err := opened.Config("tasktool.value"); err != nil || got != "last\nvalue='literal'" {
		t.Fatal("existing handle changed with environment", got, err)
	}
}

func TestInjectedConfigurationAppliesToPrivateRepositories(t *testing.T) {
	_, root := fixture(t, false)
	injectConfiguration(t, [2]string{"tasktool.scope", "private"})
	first, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Init(filepath.Join(t.TempDir(), "private"), "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var group sync.WaitGroup
	for _, repo := range []*Repository{first, second} {
		group.Add(1)
		go func(repo *Repository) {
			defer group.Done()
			for i := 0; i < 32; i++ {
				if got, err := repo.Config("tasktool.scope"); err != nil || got != "private" {
					t.Errorf("private configuration %q %v", got, err)
					return
				}
			}
		}(repo)
	}
	group.Wait()
}

func TestInjectedConfigurationRejectsMalformedInputBeforeMutation(t *testing.T) {
	_, root := fixture(t, false)
	for _, mode := range []string{"blank-count", "negative-count", "overflow-count", "missing-key", "missing-value", "empty-key", "invalid-key", "numeric-variable", "include", "invalid-conditional-key"} {
		t.Run(mode, func(t *testing.T) {
			injectConfiguration(t, [2]string{"tasktool.value", "secret-that-must-not-appear"})
			switch mode {
			case "blank-count":
				t.Setenv("GIT_CONFIG_COUNT", " ")
			case "negative-count":
				t.Setenv("GIT_CONFIG_COUNT", "-1")
			case "overflow-count":
				t.Setenv("GIT_CONFIG_COUNT", strings.Repeat("9", 100))
			case "missing-key":
				if err := os.Unsetenv("GIT_CONFIG_KEY_0"); err != nil {
					t.Fatal(err)
				}
			case "missing-value":
				if err := os.Unsetenv("GIT_CONFIG_VALUE_0"); err != nil {
					t.Fatal(err)
				}
			case "empty-key":
				t.Setenv("GIT_CONFIG_KEY_0", "")
			case "invalid-key":
				t.Setenv("GIT_CONFIG_KEY_0", "secret-that-must-not-appear")
			case "numeric-variable":
				t.Setenv("GIT_CONFIG_KEY_0", "tasktool.1invalid")
			case "include":
				t.Setenv("GIT_CONFIG_KEY_0", "include.path")
			case "invalid-conditional-key":
				t.Setenv("GIT_CONFIG_KEY_0", "includeIf.gitdir:secret-that-must-not-appear\n.path")
			}
			if repo, err := Open(root); err == nil {
				repo.Close()
				t.Fatal("accepted malformed configuration")
			} else if strings.Contains(err.Error(), "secret-that-must-not-appear") {
				t.Fatal("configuration leaked", err)
			}
			path := filepath.Join(t.TempDir(), "not-created")
			if repo, err := Init(path, "", false); err == nil {
				repo.Close()
				t.Fatal("initialized with malformed configuration")
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("invalid configuration created a repository", err)
			}
		})
	}
}

func TestInjectedConfigurationIgnoresUnusedEntries(t *testing.T) {
	_, root := fixture(t, false)
	for _, unset := range []bool{false, true} {
		t.Run(strconv.FormatBool(unset), func(t *testing.T) {
			injectConfiguration(t)
			if unset {
				if err := os.Unsetenv("GIT_CONFIG_COUNT"); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("GIT_CONFIG_KEY_0", "tasktool.unused")
			t.Setenv("GIT_CONFIG_VALUE_0", "must not apply")
			repo, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if _, err := repo.Config("tasktool.unused"); !HasCode(err, NotFound) {
				t.Fatal("unused injected value applied", err)
			}
		})
	}
}

func quoteConfigurationParameter(value string) string {
	return "'" + strings.NewReplacer("'", "'\\''", "!", "'\\!'").Replace(value) + "'"
}

func TestInjectedParametersPrecedenceAndIsolation(t *testing.T) {
	original, root := fixture(t, false)
	if err := original.SetLocalConfig("tasktool.value", "local"); err != nil {
		t.Fatal(err)
	}
	paths, _ := original.Paths()
	before := readFile(t, filepath.Join(paths.CommonDir, "config"))
	sentinel := filepath.Join(t.TempDir(), "must-not-exist")
	literal := "$(touch " + sentinel + "); quote=' and bang!\nsecond line"
	injectConfiguration(t, [2]string{"tasktool.value", "counted"})
	t.Setenv("GIT_CONFIG_PARAMETERS", "'TaskTool.Value'='first' 'tasktool.value=last=value' 'TaskTool.Case=Sensitive.Value'='subsection' 'tasktool.literal'="+quoteConfigurationParameter(literal))
	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	for key, want := range map[string]string{"tasktool.value": "last=value", "tasktool.Case=Sensitive.value": "subsection", "tasktool.literal": literal} {
		for i := 0; i < 16; i++ {
			if got, err := repo.Config(key); err != nil || got != want {
				t.Fatalf("%s: %q, %v; want %q", key, got, err, want)
			}
		}
	}
	entries, err := repo.configEntries("tasktool.value")
	want := []configurationEntry{{"tasktool.value", "local"}, {"tasktool.value", "counted"}, {"tasktool.value", "first"}, {"tasktool.value", "last=value"}}
	if err != nil || !reflect.DeepEqual(entries, want) {
		t.Fatalf("parameter ordering: %#v, %v", entries, err)
	}
	if got, err := original.Config("tasktool.value"); err != nil || got != "local" {
		t.Fatal("existing handle changed", got, err)
	}
	if _, err := os.Lstat(sentinel); !os.IsNotExist(err) {
		t.Fatal("parameter was executed", err)
	}
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	plain, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if got, err := plain.Config("tasktool.value"); err != nil || got != "counted" {
		t.Fatal("new handle retained previous parameters", got, err)
	}
	if got, err := repo.Config("tasktool.value"); err != nil || got != "last=value" {
		t.Fatal("live handle followed changed environment", got, err)
	}
	if !bytes.Equal(before, readFile(t, filepath.Join(paths.CommonDir, "config"))) {
		t.Fatal("parameters changed repository config")
	}
}

func TestInjectedParametersBooleanAndEmptyCount(t *testing.T) {
	_, root := fixture(t, false)
	t.Setenv("GIT_CONFIG_COUNT", "") // Git treats an empty count as zero.
	t.Setenv("GIT_CONFIG_KEY_0", "tasktool.unused")
	t.Setenv("GIT_CONFIG_VALUE_0", "unused")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'tasktool.oldtrue' 'tasktool.newtrue'= 'tasktool.oldfalse=' 'tasktool.newfalse'='' 'tasktool.numeric'='2'\t'tasktool.case'='TRUE'\n")
	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	for key, want := range map[string]bool{"oldtrue": true, "newtrue": true, "oldfalse": false, "newfalse": false, "numeric": true, "case": true} {
		for i := 0; i < 16; i++ {
			if got, err := repo.ConfigBool("tasktool." + key); err != nil || got != want {
				t.Fatalf("%s: %v %v; want %v", key, got, err, want)
			}
		}
	}
	if _, err := repo.Config("tasktool.unused"); !HasCode(err, NotFound) {
		t.Fatal("empty count applied unused configuration", err)
	}
	if _, err := repo.ConfigBool("tasktool.missing"); !HasCode(err, NotFound) {
		t.Fatal("missing boolean lost error identity", err)
	}
}

func TestInjectedParametersRejectMalformedInputBeforeMutation(t *testing.T) {
	_, root := fixture(t, false)
	for i, input := range []string{
		" ", " 'tasktool.value'='secret'", "tasktool.value=secret", "\"tasktool.value=secret\"",
		"'tasktool.value'='secret", "'tasktool.value'=secret", "'tasktool.value'='secret'x",
		"'tasktool.value'='a'\\x'b'", "'tasktool.value'='a'\\!'", "'tasktool.value'='a''b'",
		"'tasktool.value'='secret'; command", "''='secret'", "'tasktool.1invalid'='secret'",
		"'include.path'='secret'", "'includeIf.gitdir:secret\n.path'='secret'",
	} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			injectConfiguration(t)
			t.Setenv("GIT_CONFIG_PARAMETERS", input)
			for _, initialize := range []bool{false, true} {
				path := filepath.Join(t.TempDir(), "not-created")
				var repo *Repository
				var err error
				if initialize {
					repo, err = Init(path, "", false)
				} else {
					repo, err = Open(root)
				}
				if err == nil {
					repo.Close()
					t.Fatal("accepted malformed parameters")
				}
				if strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "GIT_CONFIG_PARAMETERS") {
					t.Fatalf("unsafe or missing diagnostic: %v", err)
				}
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("malformed parameters created a repository", err)
				}
			}
		})
	}
}

func TestInjectedConfigurationGitOracle(t *testing.T) {
	if os.Getenv("TASKTOOL_GIT_ORACLE") != "1" {
		t.Skip("explicit Git CLI configuration oracle only")
	}
	repo, root := fixture(t, false)
	if err := repo.SetLocalConfig("tasktool.value", "local"); err != nil {
		t.Fatal(err)
	}
	injectConfiguration(t, [2]string{"TaskTool.Value", "first"}, [2]string{"tasktool.value", "last"})
	t.Setenv("GIT_CONFIG_PARAMETERS", "'tasktool.value'='propagated' 'tasktool.enabled'= 'tasktool.disabled'=''")
	opened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	entries, err := opened.configEntries("tasktool.value")
	if err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	for _, entry := range entries {
		fmt.Fprintln(&want, entry.value)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", root, "config", "--get-all", "tasktool.value")
	output, err := command.CombinedOutput()
	if err != nil || string(output) != want.String() {
		t.Fatalf("Git configuration oracle: %q %v, native %q", output, err, want.String())
	}
	for _, key := range []string{"tasktool.enabled", "tasktool.disabled"} {
		value, err := opened.ConfigBool(key)
		if err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(ctx, "git", "-C", root, "config", "--bool", "--get", key)
		output, err := command.CombinedOutput()
		if err != nil || string(output) != strconv.FormatBool(value)+"\n" {
			t.Fatalf("Git boolean oracle %s: %q %v; native %v", key, output, err, value)
		}
	}
}
