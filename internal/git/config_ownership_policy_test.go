package git

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A low-level prepare/apply test must query through that same configuration
// owner: ordinary Repository methods refresh their stored configuration. This
// borrowed view owns neither resource; the fixture and caller close them.
func preparedConfigurationView(repo *Repository, prepared *environmentConfiguration) *Repository {
	return &Repository{ptr: repo.ptr, configuration: prepared}
}

func TestCommandOwnershipPolicyRejectedBeforeOpenAndInit(t *testing.T) {
	original, root := fixture(t, false)
	if err := original.SetLocalConfig("tasktool.value", "local"); err != nil {
		t.Fatal(err)
	}
	paths, err := original.Paths()
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(paths.CommonDir, "config")
	before := readFile(t, config)
	for _, mode := range []string{"counted-empty", "counted-star", "counted-path", "old-empty", "new-empty", "old-nil", "new-nil", "nested-reset", "nested-nil", "parameter-nested"} {
		t.Run(mode, func(t *testing.T) {
			injectConfiguration(t)
			t.Setenv("GIT_CONFIG_PARAMETERS", "")
			switch mode {
			case "counted-empty":
				injectConfiguration(t, [2]string{"SaFe.DiReCtOrY", ""})
			case "counted-star":
				injectConfiguration(t, [2]string{"safe.directory", "*"})
			case "counted-path":
				injectConfiguration(t, [2]string{"safe.directory", "/secret-policy-path"})
			case "old-empty":
				t.Setenv("GIT_CONFIG_PARAMETERS", "'SaFe.DiReCtOrY='")
			case "new-empty":
				t.Setenv("GIT_CONFIG_PARAMETERS", "'SaFe.DiReCtOrY'=''")
			case "old-nil":
				t.Setenv("GIT_CONFIG_PARAMETERS", "'safe.directory'")
			case "new-nil":
				t.Setenv("GIT_CONFIG_PARAMETERS", "'safe.directory'=")
			default:
				dir := t.TempDir()
				top := filepath.Join(dir, "secret-policy-source")
				writeCommandInclude(t, top, "[include]\npath = nested\n")
				text := "[SaFe]\nDiReCtOrY =\nDiReCtOrY = /secret-policy-path\n"
				if mode == "nested-nil" {
					text = "[safe]\ndirectory\n"
				}
				writeCommandInclude(t, filepath.Join(dir, "nested"), text)
				if mode == "parameter-nested" {
					t.Setenv("GIT_CONFIG_PARAMETERS", "'include.path'="+quoteConfigurationParameter(top))
				} else {
					injectConfiguration(t, [2]string{"include.path", top})
				}
			}
			env := os.Environ()
			for _, initialize := range []bool{false, true} {
				dest := filepath.Join(t.TempDir(), "not-created")
				var repo *Repository
				var err error
				if initialize {
					repo, err = Init(dest, "", false)
				} else {
					// A nonexistent path proves the policy check precedes native
					// open/discovery, not merely a post-admission readback.
					repo, err = Open(dest)
				}
				if err == nil {
					repo.Close()
					t.Fatal("accepted unsupported ownership policy")
				}
				if !strings.Contains(err.Error(), "command-scope safe.directory") || strings.Contains(err.Error(), "secret") {
					t.Fatal("wrong or unredacted diagnostic", err)
				}
				if _, err := os.Lstat(dest); !os.IsNotExist(err) {
					t.Fatal("policy rejection mutated destination", err)
				}
			}
			if !reflect.DeepEqual(env, os.Environ()) {
				t.Fatal("policy check changed environment")
			}
			if got, err := original.Config("tasktool.value"); err != nil || got != "local" {
				t.Fatal("policy check changed another handle", got, err)
			}
			if !bytes.Equal(before, readFile(t, config)) {
				t.Fatal("policy check changed local configuration")
			}
		})
	}
	plain, err := Open(root)
	if err != nil {
		t.Fatal("rejection contaminated subsequent handle", err)
	}
	plain.Close()
}

// This is a deterministic prepare/apply boundary test, not an ownership
// admission test: no foreign-owner fixture or native validation bypass is used.
func TestCommandIncludePolicySnapshotSurvivesSourceReplacement(t *testing.T) {
	for _, replacement := range []string{"policy", "malformed", "removed", "previously-missing"} {
		t.Run(replacement, func(t *testing.T) {
			repo, _ := fixture(t, false)
			policyBefore, err := repo.configEntries("safe.directory")
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			top, nested := filepath.Join(dir, "top"), filepath.Join(dir, "nested")
			writeCommandInclude(t, top, "[tasktool]\nvalue = before\n[include]\npath = nested\n")
			if replacement != "previously-missing" {
				writeCommandInclude(t, nested, "[tasktool]\nvalue = frozen\nenabled\ndisabled =\n")
			}
			injectConfiguration(t, [2]string{"include.path", top})
			t.Setenv("GIT_CONFIG_PARAMETERS", "'tasktool.value'='last'")
			prepared, err := prepareEnvironmentConfiguration()
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.close()
			writeCommandInclude(t, top, "[safe]\ndirectory =\n")
			switch replacement {
			case "malformed":
				writeCommandInclude(t, nested, "[malformed")
			case "removed":
				if err := os.Remove(nested); err != nil {
					t.Fatal(err)
				}
			default:
				writeCommandInclude(t, nested, "[safe]\ndirectory =\n[tasktool]\nvalue = changed\n")
			}
			if err := prepared.apply(repo.ptr); err != nil {
				t.Fatal("apply reread a protected source", err)
			}
			view := preparedConfigurationView(repo, prepared)
			if got, err := view.configEntries("safe.directory"); err != nil || !reflect.DeepEqual(got, policyBefore) {
				t.Fatal("replacement ownership policy entered frozen overlay", got, err)
			}
			entries, err := view.configEntries("tasktool.value")
			want := []configurationEntry{{"tasktool.value", "before"}}
			if replacement != "previously-missing" {
				want = append(want, configurationEntry{"tasktool.value", "frozen"})
				for key, want := range map[string]bool{"enabled": true, "disabled": false} {
					if got, err := view.ConfigBool("tasktool." + key); err != nil || got != want {
						t.Fatal("lost nil/empty include value", key, got, err)
					}
				}
			}
			want = append(want, configurationEntry{"tasktool.value", "last"})
			if err != nil || !reflect.DeepEqual(entries, want) {
				t.Fatalf("frozen ordered entries: %#v, %v; want %#v", entries, err, want)
			}
		})
	}
}

// Run with a fresh native library state so the normal global search paths have
// not been cached by unrelated tests. This proves config compatibility only,
// not admission of a foreign-owned repository.
func TestCommandOwnershipPolicyLeavesFilePolicyAlone(t *testing.T) {
	if os.Getenv("TASKTOOL_CONFIG_POLICY_CHILD") != "1" {
		home := t.TempDir()
		writeCommandInclude(t, filepath.Join(home, ".gitconfig"), "[safe]\ndirectory = *\n")
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
		t.Setenv("TASKTOOL_CONFIG_POLICY_CHILD", "1")
		injectConfiguration(t)
		t.Setenv("GIT_CONFIG_PARAMETERS", "")
		command := exec.Command(os.Args[0], "-test.run=^TestCommandOwnershipPolicyLeavesFilePolicyAlone$")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("file policy subprocess: %v\n%s", err, output)
		}
		return
	}
	repo, root := fixture(t, false)
	if got, err := repo.Config("safe.directory"); err != nil || got != "*" {
		t.Fatal("normal global policy not loaded", got, err)
	}
	if err := repo.SetLocalConfig("safe.directory", "local-policy-value"); err != nil {
		t.Fatal(err)
	}
	injectConfiguration(t, [2]string{"tasktool.ordinary", "command"})
	opened, err := Open(root)
	if err != nil {
		t.Fatal("ordinary command overlay rejected file policy", err)
	}
	defer opened.Close()
	if got, err := opened.Config("safe.directory"); err != nil || got != "local-policy-value" {
		t.Fatal("file policy changed", got, err)
	}
	injectConfiguration(t, [2]string{"safe.directory", ""})
	if rejected, err := Open(root); err == nil {
		rejected.Close()
		t.Fatal("accepted command reset of normal global wildcard")
	} else if !strings.Contains(err.Error(), "command-scope safe.directory") {
		t.Fatal(err)
	}
}

func TestCommandProtectedAndRepositoryConditionalContextsRemainDistinct(t *testing.T) {
	repo, _ := fixture(t, false)
	dir := t.TempDir()
	top, nested := filepath.Join(dir, "top"), filepath.Join(dir, "nested")
	writeCommandInclude(t, top, "[include]\npath = nested\n[tasktool]\nvalue = after\n")
	writeCommandInclude(t, nested, "[includeIf \"onbranch:main\"]\npath = branch\n[includeIf \"onbranch:not-main\"]\npath = malformed\n")
	writeCommandInclude(t, filepath.Join(dir, "branch"), "[safe]\ndirectory =\n[tasktool]\nvalue = conditional\n")
	writeCommandInclude(t, filepath.Join(dir, "malformed"), "[malformed")
	injectConfiguration(t, [2]string{"include.path", top})
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	prepared, err := prepareEnvironmentConfiguration()
	if err != nil {
		t.Fatal("repo-dependent policy treated as protected", err)
	}
	defer prepared.close()
	// Retain the original real-file origin and directives, not their new text.
	writeCommandInclude(t, nested, "[safe]\ndirectory = *\n")
	if err := prepared.apply(repo.ptr); err != nil {
		t.Fatal(err)
	}
	view := preparedConfigurationView(repo, prepared)
	if got, err := view.Config("safe.directory"); err != nil || got != "" {
		t.Fatal("ordinary conditional config missing", got, err)
	}
	entries, err := view.configEntries("tasktool.value")
	want := []configurationEntry{{"tasktool.value", "conditional"}, {"tasktool.value", "after"}}
	if err != nil || !reflect.DeepEqual(entries, want) {
		t.Fatalf("conditional inline ordering: %#v %v", entries, err)
	}
}
