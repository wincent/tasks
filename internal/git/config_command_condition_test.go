package git

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCommandConditionsMatchWithoutReadingFalseTargets(t *testing.T) {
	for _, mode := range []string{"gitdir", "gitdir-relative", "gitdir-directory", "gitdir-empty", "gitdir-false", "gitdir-insensitive", "gitdir-insensitive-empty", "branch", "branch-directory", "branch-empty", "branch-false", "detached", "non-head-symbolic", "unknown", "hasconfig"} {
		t.Run(mode, func(t *testing.T) {
			repo, root := fixture(t, false)
			paths, err := repo.Paths()
			if err != nil {
				t.Fatal(err)
			}
			condition, active := "", true
			switch mode {
			case "gitdir":
				condition = "gitdir:" + filepath.Clean(paths.GitDir)
			case "gitdir-relative":
				condition = "gitdir:.git"
			case "gitdir-directory":
				condition = "gitdir:" + filepath.Dir(filepath.Clean(paths.GitDir)) + "/"
			case "gitdir-empty":
				condition = "gitdir:"
			case "gitdir-false":
				condition, active = "gitdir:/secret-nonmatching-repository/", false
			case "gitdir-insensitive":
				condition = "gitdir/i:" + strings.ToUpper(filepath.Clean(paths.GitDir))
			case "gitdir-insensitive-empty":
				condition = "gitdir/i:"
			case "branch":
				condition = "onbranch:m*"
			case "branch-directory":
				writeCommandInclude(t, filepath.Join(paths.GitDir, "HEAD"), "ref: refs/heads/feature/topic\n")
				condition = "onbranch:feature/"
			case "branch-empty":
				condition, active = "onbranch:", false
			case "branch-false":
				condition, active = "onbranch:not-main", false
			case "detached":
				writeCommandInclude(t, filepath.Join(paths.GitDir, "HEAD"), strings.Repeat("1", 40)+"\n")
				condition, active = "onbranch:*", false
			case "non-head-symbolic":
				writeCommandInclude(t, filepath.Join(paths.GitDir, "HEAD"), "ref: refs/tags/not-a-branch\n")
				condition, active = "onbranch:*", false
			case "unknown":
				condition, active = "unknown:*", false
			case "hasconfig":
				// URL discovery validates even false hasconfig closures, unlike
				// branch/gitdir conditions that do not read false targets.
				condition, active = "hasconfig:remote.*.url:*", false
			}
			target := filepath.Join(t.TempDir(), "target")
			contents := "[malformed secret target"
			if active {
				contents = "[tasktool]\ncondition = matched\n"
			}
			writeCommandInclude(t, target, contents)
			injectConfiguration(t, [2]string{"includeIf." + condition + ".path", target})
			t.Setenv("GIT_CONFIG_PARAMETERS", "")
			temp := t.TempDir()
			t.Setenv("TMPDIR", temp)
			opened, err := Open(root)
			if opened != nil {
				defer opened.Close()
			}
			if mode == "hasconfig" {
				if err == nil || strings.Contains(err.Error(), "secret") {
					t.Fatalf("malformed false hasconfig target must fail without leaking content: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				got, err := opened.Config("tasktool.condition")
				if active && (err != nil || got != "matched") || !active && !HasCode(err, NotFound) {
					t.Fatalf("active=%v: got %q, %v", active, got, err)
				}
			}
			if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
				t.Fatal("condition parser left temporary inputs", entries, err)
			}
		})
	}
}

func TestCommandConditionalTargetsValidateOnlyWhenActive(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, target := range []string{"nil-old", "nil-new", "empty", "relative", "missing", "malformed", "home"} {
			t.Run(target+"-active="+map[bool]string{false: "false", true: "true"}[active], func(t *testing.T) {
				_, root := fixture(t, false)
				condition := "onbranch:not-main"
				if active {
					condition = "onbranch:main"
				}
				key := "includeIf." + condition + ".path"
				path := filepath.Join(t.TempDir(), "secret-target")
				parameter := quoteConfigurationParameter(key)
				wantError := active
				switch target {
				case "nil-old":
				case "nil-new":
					parameter += "="
				case "empty":
					parameter += "=''"
				case "relative":
					parameter += "='secret-relative-target'"
				case "missing":
					parameter += "=" + quoteConfigurationParameter(path)
					wantError = false
				case "malformed":
					writeCommandInclude(t, path, "[secret-invalid-section\nsecret-value")
					parameter += "=" + quoteConfigurationParameter(path)
				case "home":
					writeCommandInclude(t, path, "[tasktool]\nhome = matched\n")
					home, err := os.UserHomeDir()
					if err != nil {
						t.Fatal(err)
					}
					relative, err := filepath.Rel(home, path)
					if err != nil {
						t.Fatal(err)
					}
					parameter += "=" + quoteConfigurationParameter("~/"+relative)
					wantError = false
				}
				injectConfiguration(t)
				t.Setenv("GIT_CONFIG_PARAMETERS", parameter)
				opened, err := Open(root)
				if opened != nil {
					defer opened.Close()
				}
				if (err != nil) != wantError {
					t.Fatalf("want error=%v, got %v", wantError, err)
				}
				if err != nil && strings.Contains(err.Error(), "secret") {
					t.Fatal("conditional diagnostic leaked target or content", err)
				}
				if !active && (target == "nil-old" || target == "nil-new" || target == "empty") {
					if got, err := configBool(opened, key); err != nil || got != (target != "empty") {
						t.Fatal("inactive directive lost nil/empty distinction", got, err)
					}
				}
				if active && target == "home" {
					if got, err := opened.Config("tasktool.home"); err != nil || got != "matched" {
						t.Fatal("home-relative target was not parsed", got, err)
					}
				}
			})
		}
	}
}

func TestCommandConditionalRelativePatternHasNoSyntheticOrigin(t *testing.T) {
	_, root := fixture(t, false)
	for _, prefix := range []string{"gitdir:", "gitdir/i:"} {
		t.Run(prefix, func(t *testing.T) {
			injectConfiguration(t, [2]string{"includeIf." + prefix + "./secret-pattern.path", "/secret-target"})
			t.Setenv("GIT_CONFIG_PARAMETERS", "")
			if opened, err := Open(root); err == nil {
				opened.Close()
				t.Fatal("synthetic marker supplied command origin")
			} else if !strings.Contains(err.Error(), "no originating configuration file") || strings.Contains(err.Error(), "secret") {
				t.Fatal("wrong or unredacted origin diagnostic", err)
			}
		})
	}
}

func TestCommandConditionalOrderingOriginsAndIsolation(t *testing.T) {
	original, root := fixture(t, false)
	if err := original.SetLocalConfig("tasktool.value", "local"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	writeCommandInclude(t, target, "[tasktool]\nvalue = conditional\nimplicit\nempty =\n[include]\npath = nested\n[includeIf \"onbranch:main\"]\npath = branch\n")
	writeCommandInclude(t, filepath.Join(dir, "nested"), "[tasktool]\nvalue = nested\n")
	writeCommandInclude(t, filepath.Join(dir, "branch"), "[tasktool]\nvalue = branch\n")
	injectConfiguration(t, [2]string{"tasktool.value", "before"}, [2]string{"includeIf.onbranch:main.path", target}, [2]string{"tasktool.value", "after"})
	t.Setenv("GIT_CONFIG_PARAMETERS", quoteConfigurationParameter("includeIf.onbranch:main.path")+"="+quoteConfigurationParameter(target)+" 'tasktool.value'='parameters'")
	opened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	entries, err := opened.configEntries("tasktool.value")
	want := []configurationEntry{
		{"tasktool.value", "local"}, {"tasktool.value", "before"},
		{"tasktool.value", "conditional"}, {"tasktool.value", "nested"}, {"tasktool.value", "branch"}, {"tasktool.value", "after"},
		{"tasktool.value", "conditional"}, {"tasktool.value", "nested"}, {"tasktool.value", "branch"}, {"tasktool.value", "parameters"},
	}
	if err != nil || !reflect.DeepEqual(entries, want) {
		t.Fatalf("conditional inline order: %#v, %v", entries, err)
	}
	for key, want := range map[string]bool{"tasktool.implicit": true, "tasktool.empty": false} {
		if got, err := configBool(opened, key); err != nil || got != want {
			t.Fatal("included nil and empty lost distinction", key, got, err)
		}
	}
	writeCommandInclude(t, target, "[malformed")
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	injectConfiguration(t)
	if got, err := opened.Config("tasktool.value"); err != nil || got != "parameters" {
		t.Fatal("applied handle reread target or environment", got, err)
	}
	if got, err := original.Config("tasktool.value"); err != nil || got != "local" {
		t.Fatal("command conditional changed existing handle", got, err)
	}
}

func TestCommandConditionalsDoNotEnterProtectedPolicy(t *testing.T) {
	repo, _ := fixture(t, false)
	target := filepath.Join(t.TempDir(), "conditional")
	writeCommandInclude(t, target, "[safe]\ndirectory = *\nbareRepository = all\n[tasktool]\ncondition = matched\n")
	injectConfiguration(t, [2]string{"safe.bareRepository", "explicit"})
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	baseline, err := prepareEnvironmentConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	defer baseline.close()
	ownerBefore, err := baseline.protectedValues("safe.directory")
	if err != nil {
		t.Fatal(err)
	}
	injectConfiguration(t, [2]string{"safe.bareRepository", "explicit"}, [2]string{"includeIf.onbranch:main.path", target})
	prepared, err := prepareEnvironmentConfiguration()
	if err != nil {
		t.Fatal("repository-dependent ownership value rejected as unconditional", err)
	}
	defer prepared.close()
	if values, err := prepared.protectedValues("safe.directory"); err != nil || !reflect.DeepEqual(values, ownerBefore) {
		t.Fatal("conditional ownership value promoted to protected policy", values, err)
	}
	if values, err := prepared.protectedValues("safe.barerepository"); err != nil || len(values) == 0 || values[len(values)-1] == nil || *values[len(values)-1] != "explicit" {
		t.Fatal("conditional bare policy promoted to protected policy", values, err)
	}
	// The prepared directive is immutable even if command inputs change.
	injectConfiguration(t, [2]string{"includeIf.onbranch:main.path", "/secret-replacement"})
	if err := prepared.apply(repo.ptr); err != nil {
		t.Fatal(err)
	}
	view := preparedConfigurationView(repo, prepared)
	for key, want := range map[string]string{"safe.directory": "*", "safe.barerepository": "all", "tasktool.condition": "matched"} {
		if got, err := view.Config(key); err != nil || got != want {
			t.Fatal("ordinary conditional value missing", key, got, err)
		}
	}
}

func TestCommandConditionalCannotRelaxBareDiscoveryPolicy(t *testing.T) {
	_, root := fixture(t, true)
	target := filepath.Join(t.TempDir(), "target")
	writeCommandInclude(t, target, "[safe]\nbareRepository = all\n")
	injectConfiguration(t, [2]string{"safe.bareRepository", "explicit"}, [2]string{"includeIf.gitdir:.path", target})
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	if opened, err := Open(root); err == nil {
		opened.Close()
		t.Fatal("conditional target relaxed protected bare discovery policy")
	} else if !strings.Contains(err.Error(), "safe.bareRepository") {
		t.Fatal("wrong discovery policy error", err)
	}
}

func TestCommandConditionalInitUsesRepositoryContext(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	writeCommandInclude(t, target, "[tasktool]\ncondition = matched\n")
	injectConfiguration(t, [2]string{"includeIf.onbranch:main.path", target})
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	path := filepath.Join(t.TempDir(), "new-repository")
	repo, err := Init(path, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if got, err := repo.Config("tasktool.condition"); err != nil || got != "matched" {
		t.Fatal("initial unborn branch not supplied to command condition", got, err)
	}
}

func TestFrozenConditionalNilTargetValidation(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "inactive", true: "active"}[active], func(t *testing.T) {
			repo, _ := fixture(t, false)
			condition := "onbranch:not-main"
			if active {
				condition = "onbranch:main"
			}
			top := filepath.Join(t.TempDir(), "top")
			writeCommandInclude(t, top, "[includeIf \""+condition+"\"]\npath\n")
			injectConfiguration(t, [2]string{"include.path", top})
			t.Setenv("GIT_CONFIG_PARAMETERS", "")
			prepared, err := prepareEnvironmentConfiguration()
			if err != nil {
				t.Fatal("conditional nil target checked without repository context", err)
			}
			defer prepared.close()
			writeCommandInclude(t, top, "[malformed")
			err = prepared.apply(repo.ptr)
			if (err != nil) != active || err != nil && !strings.Contains(err.Error(), "requires a value") {
				t.Fatalf("active=%v: %v", active, err)
			}
		})
	}
}
