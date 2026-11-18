package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProtectedBareRepositoryDiscovery(t *testing.T) {
	for _, mode := range []string{"bare", "nested-bare", "misleading-core-bare", "ordinary", "separate", "container", "admin", "linked", "linked-admin", "submodule-admin"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, "repository")
			bare := mode == "bare" || mode == "nested-bare" || mode == "misleading-core-bare" || mode == "container" || mode == "admin" || mode == "submodule-admin"
			workdir := ""
			if mode == "separate" {
				workdir = filepath.Join(base, "checkout")
			}
			if mode == "container" || mode == "admin" {
				path = filepath.Join(base, "container", ".git")
			}
			if mode == "submodule-admin" {
				path = filepath.Join(base, "super", ".git", "modules", "child")
			}
			if mode == "nested-bare" {
				outer, err := Init(filepath.Join(base, "outer"), "", false)
				if err != nil {
					t.Fatal(err)
				}
				defer outer.Close()
				path = filepath.Join(base, "outer", "nested", "bare")
			}
			repo, err := Init(path, workdir, bare)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if err := repo.SetLocalConfig("safe.bareRepository", "all"); err != nil {
				t.Fatal(err)
			}
			if mode == "misleading-core-bare" {
				if err := repo.SetLocalConfig("core.bare", "false"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "separate" {
				path = workdir
			}
			if mode == "container" {
				path = filepath.Dir(path)
			}
			if mode == "linked" || mode == "linked-admin" {
				tip := commit(t, repo, "base\n")
				publish(t, repo, "refs/heads/main", tip, ID{})
				publish(t, repo, "refs/heads/linked", tip, ID{})
				linked := filepath.Join(base, "linked")
				if err := repo.AddWorktree("linked", linked, "refs/heads/linked"); err != nil {
					t.Fatal(err)
				}
				path = linked
				if mode == "linked-admin" {
					paths, err := repo.Paths()
					if err != nil {
						t.Fatal(err)
					}
					path = filepath.Join(paths.CommonDir, "worktrees", "linked")
				}
			}
			reject := mode == "bare" || mode == "nested-bare" || mode == "misleading-core-bare"
			injectConfiguration(t, [2]string{"safe.bareRepository", "explicit"})
			before := mergePreflightSnapshot(t, base)
			opened, err := Open(path)
			if opened != nil {
				opened.Close()
			}
			if reject {
				if err == nil || !strings.Contains(err.Error(), "safe.bareRepository") {
					t.Fatalf("bare discovery admitted: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if after := mergePreflightSnapshot(t, base); !reflect.DeepEqual(before, after) {
				t.Fatal("discovery policy changed repository state")
			}
			injectConfiguration(t, [2]string{"safe.bareRepository", "all"})
			opened, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			opened.Close()
		})
	}
}

func TestProtectedBareRepositoryCommandIncludesAndPrecedence(t *testing.T) {
	_, root := fixture(t, true)
	file := filepath.Join(t.TempDir(), "included")
	writeFile(t, file, []byte("[safe]\nbareRepository = explicit\n"))
	injectConfiguration(t, [2]string{"safe.bareRepository", "all"}, [2]string{"include.path", file})
	if repo, err := Open(root); err == nil {
		repo.Close()
		t.Fatal("ignored included protected policy")
	}
	t.Setenv("GIT_CONFIG_PARAMETERS", "'safe.bareRepository'='all'")
	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	repo.Close()
	// A repo-dependent include is not active in the protected-config pass.
	writeFile(t, file, []byte("[includeIf \"onbranch:main\"]\npath = conditional\n"))
	writeFile(t, filepath.Join(filepath.Dir(file), "conditional"), []byte("[safe]\nbareRepository = all\n"))
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	injectConfiguration(t, [2]string{"safe.bareRepository", "explicit"}, [2]string{"include.path", file})
	if repo, err := Open(root); err == nil {
		repo.Close()
		t.Fatal("conditional ordinary configuration relaxed protected policy")
	}
	for _, value := range []string{"", "EXPLICIT", "secret-invalid-policy"} {
		injectConfiguration(t, [2]string{"safe.bareRepository", value}, [2]string{"safe.bareRepository", "all"})
		if repo, err := Open(root); err == nil {
			repo.Close()
			t.Fatal("accepted an invalid earlier policy value")
		} else if strings.Contains(err.Error(), "secret-invalid-policy") {
			t.Fatal("policy diagnostic leaked a supplied value")
		}
	}
}

func TestProtectedBareRepositoryGlobalPolicy(t *testing.T) {
	const child = "TASK_NATIVE_BARE_POLICY_CHILD"
	if root := os.Getenv(child); root != "" {
		if repo, err := Open(root); err == nil {
			repo.Close()
			t.Fatal("ignored protected global bare policy")
		} else if !strings.Contains(err.Error(), "safe.bareRepository") {
			t.Fatal(err)
		}
		return
	}
	_, root := fixture(t, true)
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".gitconfig"), []byte("[safe]\nbareRepository = explicit\n"))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// A fresh process gives the controlled HOME fixture its own native default
	// config discovery, without changing cached process-global search paths.
	injectConfiguration(t)
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestProtectedBareRepositoryGlobalPolicy$")
	command.Env = append(os.Environ(), "HOME="+home, child+"="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("global policy child: %v\n%s", err, output)
	}
}
