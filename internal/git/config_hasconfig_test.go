package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHasconfigURLWildmatch(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		pattern, url string
		want         bool
	}{
		{"https://host/**", "https://host/a/b", true},
		{"https://host/*", "https://host/a/b", false},
		{"https://host/", "https://host/a", false},
		{"host/a", "https://host/a", false},
		{"https://HOST/**", "https://host/a", false},
		{"https://host/a/../b", "https://host/b", false},
		{"https://host/[ab]", "https://host/a", true},
		{"", "", true},
		{"x\xff?", "x\xff\xfe", true},
	} {
		got, err := hasconfigMatches(test.pattern, []string{test.url})
		if err != nil || got != test.want {
			t.Errorf("%q %q: %v %v", test.pattern, test.url, got, err)
		}
	}
}

func TestHasconfigForwardNestedCommandCaptureAndFreshFiles(t *testing.T) {
	original, root := fixture(t, false)
	paths, err := original.Paths()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	command, nested, ordinary := filepath.Join(dir, "command"), filepath.Join(dir, "nested"), filepath.Join(dir, "ordinary")
	writeCommandInclude(t, command, "[tasktool]\nvalue = command\nimplicit\nempty =\n[includeIf \"hasconfig:remote.*.url:https://host/**\"]\npath = nested\n")
	writeCommandInclude(t, nested, "[tasktool]\nvalue = nested\n")
	writeCommandInclude(t, ordinary, "[tasktool]\nfresh = before\n")
	if err := original.SetLocalConfig("includeIf.hasconfig:remote.*.url:https://host/**.path", ordinary); err != nil {
		t.Fatal(err)
	}
	injectConfiguration(t, [2]string{"includeIf.hasconfig:remote.*.url:https://host/**.path", command}, [2]string{"remote.later.url", "https://host/a/b"})
	t.Setenv("GIT_CONFIG_PARAMETERS", "'tasktool.value'='parameter'")
	opened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	entries, err := opened.configEntries("tasktool.value")
	want := []configurationEntry{{"tasktool.value", "command"}, {"tasktool.value", "nested"}, {"tasktool.value", "parameter"}}
	if err != nil || !reflect.DeepEqual(entries, want) {
		t.Fatalf("entries: %#v %v", entries, err)
	}
	for key, want := range map[string]bool{"tasktool.implicit": true, "tasktool.empty": false} {
		if got, err := opened.ConfigBool(key); err != nil || got != want {
			t.Fatalf("%s: %v %v", key, got, err)
		}
	}
	records, err := opened.configuration.effectiveRecords(opened.ptr, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range records {
		if record.name == "tasktool.value" && record.value != nil && *record.value == "nested" {
			found = record.origin == nested && record.depth == 2
		}
	}
	if !found {
		t.Fatal("lost included origin or depth")
	}
	writeCommandInclude(t, command, "[malformed")
	writeCommandInclude(t, nested, "[malformed")
	writeCommandInclude(t, ordinary, "[tasktool]\nfresh = after\n")
	if got, err := opened.Config("tasktool.value"); err != nil || got != "parameter" {
		t.Fatalf("command capture: %q %v", got, err)
	}
	if got, err := opened.Config("tasktool.fresh"); err != nil || got != "after" {
		t.Fatalf("ordinary freshness: %q %v", got, err)
	}
	if err := opened.SetLocalConfig("tasktool.localwrite", "retained"); err != nil {
		t.Fatal(err)
	}
	if got, err := opened.Config("tasktool.localwrite"); err != nil || got != "retained" {
		t.Fatalf("local setter: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(paths.CommonDir, "config")); err != nil {
		t.Fatal(err)
	}
}

func TestHasconfigFalseClosureValidationBeforeInit(t *testing.T) {
	for _, contents := range []string{"[malformed", "[include]\npath = remote\n", "[includeIf \"hasconfig:remote.*.url:never\"]\npath = remote\n"} {
		t.Run(contents, func(t *testing.T) {
			if err := initialize(); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "conditional")
			writeCommandInclude(t, path, contents)
			writeCommandInclude(t, filepath.Join(dir, "remote"), "[remote \"forbidden\"]\nurl = https://host/repo\n")
			injectConfiguration(t, [2]string{"includeIf.hasconfig:remote.*.url:never.path", path})
			t.Setenv("GIT_CONFIG_PARAMETERS", "")
			created := filepath.Join(dir, "not-created")
			if repo, err := Init(created, "", false); err == nil {
				repo.Close()
				t.Fatal("accepted invalid false-match closure")
			}
			if _, err := os.Stat(created); !os.IsNotExist(err) {
				t.Fatalf("mutated destination: %v", err)
			}
		})
	}
}

func TestHasconfigCommandOwnershipRejectedBeforeInit(t *testing.T) {
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "conditional")
	writeCommandInclude(t, path, "[safe]\ndirectory =\n")
	injectConfiguration(t, [2]string{"includeIf.hasconfig:remote.*.url:never.path", path})
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	created := filepath.Join(t.TempDir(), "not-created")
	if repo, err := Init(created, "", false); err == nil {
		repo.Close()
		t.Fatal("accepted command ownership policy")
	} else if !strings.Contains(err.Error(), "command-scope safe.directory") {
		t.Fatal(err)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatal("mutated destination", err)
	}
}

func TestHasconfigGlobalLocalWorktreeAndProtectedScope(t *testing.T) {
	if os.Getenv("TASKTOOL_HASCONFIG_CHILD") != "1" {
		home := t.TempDir()
		writeCommandInclude(t, filepath.Join(home, ".gitconfig"), "[includeIf \"hasconfig:remote.*.url:https://local/**\"]\npath = conditional\n[tasktool]\norder = global\n[safe]\nbareRepository = explicit\n")
		writeCommandInclude(t, filepath.Join(home, "conditional"), "[tasktool]\nmatched = global\n[safe]\nbareRepository = all\n")
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
		t.Setenv("TASKTOOL_HASCONFIG_CHILD", "1")
		injectConfiguration(t)
		t.Setenv("GIT_CONFIG_PARAMETERS", "")
		command := exec.Command(os.Args[0], "-test.run=^TestHasconfigGlobalLocalWorktreeAndProtectedScope$")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("child: %v\n%s", err, output)
		}
		return
	}
	repo, root := fixture(t, false)
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"remote.later.url": "https://local/repo", "extensions.worktreeConfig": "true", "tasktool.order": "local"} {
		if err := repo.SetLocalConfig(key, value); err != nil {
			t.Fatal(err)
		}
	}
	writeCommandInclude(t, filepath.Join(paths.GitDir, "config.worktree"), "[tasktool]\norder = worktree\nparentOnly = true\n")
	injectConfiguration(t, [2]string{"tasktool.order", "count"})
	t.Setenv("GIT_CONFIG_PARAMETERS", "'tasktool.order'='parameter'")
	opened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	entries, err := opened.configEntries("tasktool.order")
	want := []configurationEntry{{"tasktool.order", "global"}, {"tasktool.order", "local"}, {"tasktool.order", "worktree"}, {"tasktool.order", "count"}, {"tasktool.order", "parameter"}}
	if err != nil || !reflect.DeepEqual(entries, want) {
		t.Fatalf("precedence: %#v %v", entries, err)
	}
	if got, err := opened.Config("tasktool.matched"); err != nil || got != "global" {
		t.Fatalf("forward local URL: %q %v", got, err)
	}
	values, err := opened.configuration.protectedValues("safe.barerepository")
	if err != nil || len(values) != 1 || values[0] == nil || *values[0] != "explicit" {
		t.Fatalf("local URL relaxed protected policy: %#v %v", values, err)
	}
	_, affected, err := opened.configuration.protectedRecords()
	if err != nil || affected {
		t.Fatalf("ordinary conditional configuration affected ownership: %v %v", affected, err)
	}
	if err := opened.WithProspectiveWorktree("child", "refs/heads/tasks", func(view *Repository) error {
		if _, err := view.Config("tasktool.parentonly"); !HasCode(err, NotFound) {
			t.Fatal("future inherited config.worktree", err)
		}
		if got, err := view.Config("tasktool.matched"); err != nil || got != "global" {
			t.Fatalf("future URL context: %q %v", got, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// HOME is isolated before native initialization; only this disposable fixture's
// global file is changed. The subprocess inherits ceilings and other environment.
func TestHasconfigConditionalOwnershipAdmission(t *testing.T) {
	if os.Getenv("TASKTOOL_HASCONFIG_OWNERSHIP_CHILD") != "1" {
		home := t.TempDir()
		writeCommandInclude(t, filepath.Join(home, ".gitconfig"), "[tasktool]\nfixture = true\n")
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
		t.Setenv("TASKTOOL_HASCONFIG_OWNERSHIP_CHILD", "1")
		injectConfiguration(t)
		t.Setenv("GIT_CONFIG_PARAMETERS", "")
		command := exec.Command(os.Args[0], "-test.run=^TestHasconfigConditionalOwnershipAdmission$")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("ownership subprocess: %v\n%s", err, output)
		}
		return
	}
	repo, root := fixture(t, false)
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	global := filepath.Join(home, ".gitconfig")
	leaf := filepath.Join(home, "secret-policy-source")
	before := hasconfigTreeSnapshot(t, root)
	checkRejected := func(t *testing.T, opened *Repository, err error) {
		t.Helper()
		if opened != nil {
			opened.Close()
		}
		if err == nil || err.Error() != conditionalOwnershipPolicyError().Error() || strings.Contains(err.Error(), "secret") {
			t.Fatalf("wrong or unredacted ownership rejection: %v", err)
		}
	}
	for _, commands := range []bool{false, true} {
		for _, pattern := range []string{"https://host/**", "never"} {
			for _, nested := range []bool{false, true} {
				for _, policy := range []string{"directory =", "directory = *", "directory = /secret-policy-value", "directory", "directory = false", "directory = true"} {
					t.Run(strings.Join([]string{pattern, policy, map[bool]string{false: "plain", true: "commands"}[commands], map[bool]string{false: "direct", true: "nested"}[nested]}, "/"), func(t *testing.T) {
						injectConfiguration(t)
						if commands {
							injectConfiguration(t, [2]string{"tasktool.ordinary", "command"})
						}
						target := "secret-policy-source"
						if nested {
							target = "nested"
							writeCommandInclude(t, filepath.Join(home, target), "[include]\npath = secret-policy-source\n")
						}
						writeCommandInclude(t, leaf, "[safe]\n"+policy+"\n")
						writeCommandInclude(t, global, "[safe]\ndirectory = *\n[remote \"ordinary\"]\nurl = https://host/repo\n[includeIf \"hasconfig:remote.*.url:"+pattern+"\"]\npath = "+target+"\n")
						env := os.Environ()
						prepared, err := prepareEnvironmentConfiguration()
						if prepared != nil {
							prepared.close()
						}
						checkRejected(t, nil, err)
						opened, err := Open(root)
						checkRejected(t, opened, err)
						missing := filepath.Join(t.TempDir(), "not-created")
						opened, err = Open(missing)
						checkRejected(t, opened, err)
						opened, err = Init(missing, "", false)
						checkRejected(t, opened, err)
						if _, err := os.Lstat(missing); !os.IsNotExist(err) {
							t.Fatal("ownership rejection created destination", err)
						}
						if !reflect.DeepEqual(before, hasconfigTreeSnapshot(t, root)) || !reflect.DeepEqual(env, os.Environ()) {
							t.Fatal("ownership rejection mutated repository or environment")
						}
					})
				}
			}
		}
	}
	// Both nesting orders must use active repository context. False ordinary
	// conditions must not read their malformed targets, even under hasconfig.
	for _, condition := range []string{"onbranch:main", "gitdir:" + filepath.Clean(paths.GitDir), "gitdir:" + filepath.Clean(paths.GitDir) + "/", "gitdir/i:" + strings.ToUpper(filepath.Clean(paths.GitDir))} {
		for _, reverse := range []bool{false, true} {
			for _, active := range []bool{false, true} {
				actual := condition
				if !active {
					actual += "-never"
				}
				first, second := actual, "hasconfig:remote.*.url:never"
				if reverse {
					first, second = second, first
				}
				writeCommandInclude(t, global, "[includeIf \""+first+"\"]\npath = nested\n")
				writeCommandInclude(t, filepath.Join(home, "nested"), "[includeIf \""+second+"\"]\npath = secret-policy-source\n")
				contents := "[malformed"
				if active {
					contents = "[safe]\ndirectory =\n"
				}
				writeCommandInclude(t, leaf, contents)
				opened, err := Open(root)
				if active {
					checkRejected(t, opened, err)
				} else {
					if err != nil {
						t.Fatalf("read false ordinary include (%s, reverse=%v): %v", condition, reverse, err)
					}
					opened.Close()
				}
			}
		}
	}
	// Init uses its prospective Git directory and explicit main initial branch,
	// before creating either the repository path or a separate working directory.
	for _, mode := range []string{"ordinary", "bare", "separate", "dotgit"} {
		for _, branch := range []bool{false, true} {
			parent := t.TempDir()
			target, workdir := filepath.Join(parent, "not-created"), ""
			gitdir := filepath.Join(target, ".git")
			if mode == "bare" || mode == "separate" {
				gitdir = target
			}
			if mode == "separate" {
				workdir = filepath.Join(parent, "not-created-worktree")
			}
			if mode == "dotgit" {
				target = gitdir
			}
			gitdir, err = configurationIncludeIdentity(gitdir)
			if err != nil {
				t.Fatal(err)
			}
			condition := "gitdir:" + gitdir
			if branch {
				condition = "onbranch:main"
			}
			writeCommandInclude(t, global, "[includeIf \""+condition+"\"]\npath = nested\n")
			writeCommandInclude(t, filepath.Join(home, "nested"), "[includeIf \"hasconfig:remote.*.url:never\"]\npath = secret-policy-source\n")
			writeCommandInclude(t, leaf, "[safe]\ndirectory =\n")
			opened, err := Init(target, workdir, mode == "bare")
			checkRejected(t, opened, err)
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatal("contextual ownership rejection mutated Init destinations", entries, err)
			}
		}
	}
	writeCommandInclude(t, global, "[include]\npath = secret-policy-source\n")
	writeCommandInclude(t, leaf, "[safe]\ndirectory = *\n")
	opened, err := Open(root)
	if err != nil {
		t.Fatal("unconditional ownership allowance rejected", err)
	}
	opened.Close()
	if !reflect.DeepEqual(before, hasconfigTreeSnapshot(t, root)) {
		t.Fatal("admission checks changed existing repository")
	}
}

// Synthetic file sources cover system/XDG/global levels without changing native
// global search paths or writing the host's system configuration.
func TestHasconfigOwnershipSourceLevelsAndFalseOrdinaryTargets(t *testing.T) {
	t.Parallel()
	for _, level := range []int{2, 3, 4} {
		for _, reverse := range []bool{false, true} {
			for _, active := range []bool{false, true} {
				for _, condition := range []string{"gitdir:/repository/.git", "gitdir:/repository/.git/", "onbranch:main"} {
					context := configurationContext{gitdir: "/repository/.git", branch: "refs/heads/main"}
					if !active {
						context = configurationContext{gitdir: "/other/.git", branch: "refs/heads/other"}
					}
					first, second := condition, hasconfigURLPrefix+"never"
					if reverse {
						first, second = second, first
					}
					middle, leaf := "/middle", "/leaf"
					reads := 0
					source := hasconfigSource{
						records: []hasconfigRecord{{name: "includeif." + first + ".path", value: &middle, level: level}},
						load: func(path string) ([]hasconfigRecord, error) {
							reads++
							if !active && (path == leaf || !reverse) {
								t.Fatal("read an inactive ordinary include target")
							}
							if path == middle {
								return []hasconfigRecord{{name: "includeif." + second + ".path", value: &leaf}}, nil
							}
							return []hasconfigRecord{{name: "safe.directory"}}, nil
						},
					}
					err := (&hasconfigEvaluation{matches: context.matchesOwnership}).validateOwnership([]hasconfigSource{source})
					if active {
						if err == nil || err.Error() != conditionalOwnershipPolicyError().Error() || reads != 2 {
							t.Fatalf("level %d missed active nested ownership: reads=%d err=%v", level, reads, err)
						}
					} else if err != nil {
						t.Fatal("false ordinary condition failed", err)
					}
				}
			}
		}
	}
}

func TestHasconfigOwnershipReadOnlyBranchContext(t *testing.T) {
	t.Parallel()
	for _, head := range []string{"ref: refs/heads/main\n", strings.Repeat("1", 40) + "\n", "ref: refs/tags/not-a-branch\n"} {
		dir := t.TempDir()
		gitdir := filepath.Join(dir, "gitdir")
		if err := os.Mkdir(gitdir, 0700); err != nil {
			t.Fatal(err)
		}
		writeCommandInclude(t, filepath.Join(gitdir, "HEAD"), head)
		alias := filepath.Join(dir, "alias")
		if err := os.Symlink(gitdir, alias); err != nil {
			t.Fatal(err)
		}
		context, err := configurationAdmissionContext(alias, "")
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := filepath.EvalSymlinks(gitdir)
		if err != nil || context.gitdir != resolved {
			t.Fatal("wrong canonical Git directory", context.gitdir, err)
		}
		matched, err := context.matchesOwnership("onbranch:main", "")
		if err != nil || matched != strings.HasPrefix(head, "ref: refs/heads/") {
			t.Fatal("wrong detached/unborn branch context", context.branch, err)
		}
		if got := readFile(t, filepath.Join(gitdir, "HEAD")); string(got) != head {
			t.Fatal("read-only context changed HEAD")
		}
	}
}

func hasconfigTreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		result[path] = info.Mode().String()
		if info.Mode().IsRegular() {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result[path] += string(contents)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHasconfigDepthAndFalseNestedClosure(t *testing.T) {
	t.Parallel()
	for _, limit := range []uint{10, 11} {
		value := "/next"
		depth := uint(0)
		source := hasconfigSource{
			records: []hasconfigRecord{{name: "includeif.hasconfig:remote.*.url:never.path", value: &value}},
			load: func(string) ([]hasconfigRecord, error) {
				depth++
				if depth == limit {
					return []hasconfigRecord{{name: "tasktool.leaf"}}, nil
				}
				return []hasconfigRecord{{name: "include.path", value: &value}}, nil
			},
		}
		_, err := (&hasconfigEvaluation{}).evaluate([]hasconfigSource{source})
		if (err != nil) != (limit > 10) {
			t.Fatalf("depth %d: %v", limit, err)
		}
	}
}

func TestHasconfigInvalidOrdinaryConfigurationPreventsMutation(t *testing.T) {
	t.Parallel()
	repo, _ := fixture(t, false)
	target := filepath.Join(t.TempDir(), "target")
	writeCommandInclude(t, target, "[tasktool]\nvalid = true\n")
	if err := repo.SetLocalConfig("includeIf.hasconfig:remote.*.url:never.path", target); err != nil {
		t.Fatal(err)
	}
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(paths.CommonDir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	writeCommandInclude(t, target, "[malformed")
	if err := repo.SetLocalConfig("tasktool.mutated", "yes"); err == nil {
		t.Fatal("mutated with invalid ordinary closure")
	}
	after, err := os.ReadFile(filepath.Join(paths.CommonDir, "config"))
	if err != nil || string(after) != string(before) {
		t.Fatal("config changed", err)
	}
}

func TestHasconfigOtherConditionsAndDetachedFuture(t *testing.T) {
	t.Parallel()
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "config")
	writeCommandInclude(t, root, "[includeIf \"hasconfig:remote.*.url:https://branch/**\"]\npath = matched\n[includeIf \"onbranch:tasks\"]\npath = remote\n")
	writeCommandInclude(t, filepath.Join(dir, "matched"), "[tasktool]\nmatched = true\n")
	writeCommandInclude(t, filepath.Join(dir, "remote"), "[remote \"later\"]\nurl = https://branch/repo\n")
	files := &environmentConfiguration{}
	defer files.close()
	records, err := files.loadRecords(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"refs/heads/tasks", ""} {
		context := configurationContext{gitdir: dir, branch: branch}
		evaluation := &hasconfigEvaluation{matches: context.matches}
		got, err := evaluation.evaluate([]hasconfigSource{{records: records, load: files.loadRecords}})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, record := range got {
			found = found || record.name == "tasktool.matched"
		}
		if found != (branch != "") {
			t.Fatalf("branch %q: matched=%v", branch, found)
		}
	}
}
