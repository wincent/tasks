package git

import "testing"

func TestConfigurationWildmatch(t *testing.T) {
	for _, test := range []struct {
		pattern, value string
		fold, want     bool
	}{
		{"a/*/c", "a/b/c", false, true}, {"a/*/c", "a/b/d/c", false, false},
		{"a/**/c", "a/c", false, true}, {"a/***/c", "a/b/d/c", false, true},
		{"a/\\* ", "a/* ", false, true}, {"[[:upper:]]", "a", true, true},
		{"[^x]", "/", false, false}, {"?", "é", false, false},
		{"??", "é", false, true}, {"!x", "!x", false, true},
		{"#x", "#x", false, true}, {"x ", "x", false, false},
		{"a", "x/a", false, false}, {"A/**", "a/b/c", true, true},
	} {
		got, err := configurationWildmatch(test.pattern, test.value, test.fold)
		if err != nil || got != test.want {
			t.Errorf("%q %q: %v %v; want %v", test.pattern, test.value, got, err, test.want)
		}
	}
}
