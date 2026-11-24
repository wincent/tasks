package cli

import "testing"

func TestValueOptionsDoNotConsumeHelp(t *testing.T) {
	t.Parallel()
	for name, spec := range commands {
		for option, kind := range spec.options {
			if kind != valueOption {
				continue
			}
			t.Run(name+"/"+option, func(t *testing.T) {
				_, err := parse([]string{name, option, "--help"})
				if err == nil || err.Error() != option+" requires a value" {
					t.Fatalf("expected missing value error, got %v", err)
				}
			})
		}
	}
}

func TestMissingOptionValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args   []string
		option string
	}{
		{[]string{"run", "1", "--prompt"}, "--prompt"},
		{[]string{"run", "1", "--prompt", "--"}, "--prompt"},
		{[]string{"run", "1", "--prompt", "--unknown=with spaces"}, "--prompt"},
		{[]string{"list", "-q", "-v"}, "--query"},
		{[]string{"go", "Title", "--topic", "-name"}, "--topic"},
	} {
		_, err := parse(tc.args)
		if err == nil || err.Error() != tc.option+" requires a value" {
			t.Errorf("parse(%q): expected missing value error for %s, got %v", tc.args, tc.option, err)
		}
	}
}

func TestPromptValuesDoNotConsumeNoMerge(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		option []string
		want   string
	}{
		{[]string{"--prompt=--no-merge"}, "--no-merge"},
		{[]string{"--prompt", "-"}, "-"},
		{[]string{"--prompt", "-1"}, "-1"},
		{[]string{"--prompt", "- Run tests.\n- Check lint."}, "- Run tests.\n- Check lint."},
	} {
		args := append([]string{"run", "1"}, tc.option...)
		args = append(args, "--no-merge")
		a, err := parse(args)
		if err != nil || a.value("--prompt") != tc.want || !a.flags["--no-merge"] {
			t.Errorf("parse(%q): prompt = %q, no-merge = %v, error = %v", args, a.value("--prompt"), a.flags["--no-merge"], err)
		}
	}
}
