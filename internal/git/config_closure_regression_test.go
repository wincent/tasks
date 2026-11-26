package git

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCommandIncludeCrossTopologyCapture(t *testing.T) {
	for _, mutation := range []string{"edit", "delete", "replace", "empty", "missing", "alias"} {
		t.Run(mutation, func(t *testing.T) {
			_, root := fixture(t, false)
			dir := t.TempDir()
			top, leaf, later := filepath.Join(dir, "top"), filepath.Join(dir, "leaf"), filepath.Join(dir, "later")
			writeCommandInclude(t, top, "[tasktool]\nvalue = top-before\n[include]\npath = leaf\n[tasktool]\nvalue = top-after\n")
			leafData := "[tasktool]\nvalue = leaf\nenabled\nempty =\n[includeIf \"onbranch:tasks\"]\npath = branch\n"
			writeCommandInclude(t, filepath.Join(dir, "branch"), "[tasktool]\nvalue = branch\n")
			if mutation != "missing" {
				if mutation == "empty" {
					leafData = ""
				}
				writeCommandInclude(t, leaf, leafData)
			}
			target := leaf
			if mutation == "alias" {
				target = filepath.Join(dir, "alias")
				if err := os.Symlink(leaf, target); err != nil {
					t.Fatal(err)
				}
			}
			// A newly active file includes a previously captured leaf. The
			// native parser must never reopen that leaf via the later route.
			writeCommandInclude(t, later, "[tasktool]\nvalue = later-before\n[include]\npath = "+quoteConfigurationFile(target)+"\n[tasktool]\nvalue = later-after\n")
			injectConfiguration(t, [2]string{"include.path", top}, [2]string{"includeIf.onbranch:tasks.path", later})
			t.Setenv("GIT_CONFIG_PARAMETERS", "")
			repo, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			switch mutation {
			case "delete":
				if err := os.Remove(leaf); err != nil {
					t.Fatal(err)
				}
			case "replace":
				replacement := filepath.Join(dir, "replacement")
				writeCommandInclude(t, replacement, "[malformed")
				if err := os.Rename(replacement, leaf); err != nil {
					t.Fatal(err)
				}
			default:
				writeCommandInclude(t, leaf, "[malformed")
			}
			want := []configurationEntry{{"tasktool.value", "top-before"}}
			if mutation != "empty" && mutation != "missing" {
				want = append(want, configurationEntry{"tasktool.value", "leaf"}, configurationEntry{"tasktool.value", "branch"})
			}
			want = append(want, configurationEntry{"tasktool.value", "top-after"}, configurationEntry{"tasktool.value", "later-before"})
			if mutation != "empty" && mutation != "missing" {
				want = append(want, configurationEntry{"tasktool.value", "leaf"}, configurationEntry{"tasktool.value", "branch"})
			}
			want = append(want, configurationEntry{"tasktool.value", "later-after"})
			for attempt := 0; attempt < 2; attempt++ {
				err = repo.WithProspectiveWorktree("child", filepath.Join(dir, "child"), "refs/heads/tasks", func(view *Repository) error {
					got, err := view.configEntries("tasktool.value")
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("ordered capture: %#v %v; want %#v", got, err, want)
					}
					if mutation != "empty" && mutation != "missing" {
						if enabled, err := configBool(view, "tasktool.enabled"); err != nil || !enabled {
							t.Fatalf("nil value lost: %v %v", enabled, err)
						}
						if empty, err := view.Config("tasktool.empty"); err != nil || empty != "" {
							t.Fatalf("empty value lost: %q %v", empty, err)
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				writeCommandInclude(t, later, "[malformed")
				writeCommandInclude(t, filepath.Join(dir, "branch"), "[malformed")
			}
		})
	}
}

func TestCommandIncludeCapturedDepthAndCycle(t *testing.T) {
	for _, cyclic := range []bool{false, true} {
		t.Run(fmt.Sprint(cyclic), func(t *testing.T) {
			dir := t.TempDir()
			for i := 0; i < 11; i++ {
				next := fmt.Sprint(i + 1)
				if cyclic {
					next = "0"
				}
				writeCommandInclude(t, filepath.Join(dir, fmt.Sprint(i)), "[include]\npath = "+next+"\n")
			}
			injectConfiguration(t, [2]string{"include.path", filepath.Join(dir, "0")})
			t.Setenv("GIT_CONFIG_PARAMETERS", "")
			prepared, err := prepareEnvironmentConfiguration()
			if prepared != nil {
				prepared.close()
			}
			if err == nil || !strings.Contains(err.Error(), "nesting depth") {
				t.Fatalf("unbounded closure: %v", err)
			}
		})
	}
}

func TestCommandIncludeMissingSymlinkCapture(t *testing.T) {
	_, root := fixture(t, false)
	dir := t.TempDir()
	target, alias := filepath.Join(dir, "target"), filepath.Join(dir, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	injectConfiguration(t, [2]string{"include.path", alias}, [2]string{"includeIf.onbranch:tasks.path", target})
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	writeCommandInclude(t, target, "[malformed")
	if err := repo.WithProspectiveWorktree("child", filepath.Join(dir, "child"), "refs/heads/tasks", func(view *Repository) error {
		return nil
	}); err != nil {
		t.Fatal("reread captured missing alias target", err)
	}
}

func TestCommandIncludeNativeDepthBoundary(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		data := "[tasktool]\nvalue = deepest\n"
		if i < 9 {
			data = "[include]\npath = " + fmt.Sprint(i+1) + "\n"
		}
		writeCommandInclude(t, filepath.Join(dir, fmt.Sprint(i)), data)
	}
	injectConfiguration(t, [2]string{"include.path", filepath.Join(dir, "0")})
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	prepared, err := prepareEnvironmentConfiguration()
	if err != nil {
		t.Fatal("native depth-nine leaf rejected", err)
	}
	prepared.close()
	writeCommandInclude(t, filepath.Join(dir, "9"), "[include]\npath = 10\n")
	writeCommandInclude(t, filepath.Join(dir, "10"), "[tasktool]\nvalue = too-deep\n")
	prepared, err = prepareEnvironmentConfiguration()
	if prepared != nil {
		prepared.close()
	}
	if err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("native depth-ten leaf accepted: %v", err)
	}
}

func TestConfigurationIncludeMaskPreservesLogicalLines(t *testing.T) {
	t.Parallel()
	input := "\xef\xbb\xbf[InClUdE]\npath = child\n[tasktool]\nvalue = \"first\\\n[include] second\"\n# [include]\n; [include]\ncontinued = first\\\n[include] second\n[includeIf \"onbranch:tasks\"]\npath = active\n[include.other]\npath = literal\n"
	got, prefix := configurationWithoutIncludes(input)
	want := strings.Replace(input, "[InClUdE]", "["+prefix+"]", 1)
	want = strings.Replace(want, "[include.other]", "["+prefix+".other]", 1)
	want = strings.Replace(want, `[includeIf "onbranch:tasks"]`, `[`+prefix+`If "onbranch:tasks"]`, 1)
	if got != want {
		t.Fatalf("mask changed non-header bytes:\n%q\nwant:\n%q", got, want)
	}
}
