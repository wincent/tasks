package cli

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func TestParseUserConfigValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		input      string
		want       map[string]string
		wantStatus *bool
	}{
		{
			name:  "empty file",
			input: "",
			want:  nil,
		},
		{
			name:  "blank and comments only",
			input: "\n  # top comment\n   \t  \n# another\n",
			want:  nil,
		},
		{
			name:  "empty models section",
			input: "models:\n",
			want:  nil,
		},
		{
			name:  "empty models section with comments",
			input: "models: # section comment\n  # nothing yet\n",
			want:  nil,
		},
		{
			name:  "empty aliases section",
			input: "models:\n  aliases:\n",
			want:  nil,
		},
		{
			name:  "empty aliases section with comments",
			input: "models:\n  aliases: # comment\n    # empty\n",
			want:  nil,
		},
		{
			name:  "empty terminal section",
			input: "terminal:\n",
			want:  nil,
		},
		{
			name:  "empty terminal section with comments",
			input: "terminal: # section comment\n  # nothing yet\n",
			want:  nil,
		},
		{
			name:       "terminal status false",
			input:      "terminal:\n  status: false\n",
			wantStatus: boolPtr(false),
		},
		{
			name:       "terminal status true with comment",
			input:      "terminal:\n  status: true # inline comment\n",
			wantStatus: boolPtr(true),
		},
		{
			name:       "terminal status truthy variants",
			input:      "terminal:\n  status: ON\n",
			wantStatus: boolPtr(true),
		},
		{
			name:       "terminal status numeric 1",
			input:      "terminal:\n  status: 1\n",
			wantStatus: boolPtr(true),
		},
		{
			name:       "terminal status numeric 0",
			input:      "terminal:\n  status: 0\n",
			wantStatus: boolPtr(false),
		},
		{
			name:       "terminal status mixed case off",
			input:      "terminal:\n  status: Off\n",
			wantStatus: boolPtr(false),
		},
		{
			name:       "terminal status mixed case true",
			input:      "terminal:\n  status: TrUe\n",
			wantStatus: boolPtr(true),
		},
		{
			name:       "terminal status quoted keys and value",
			input:      "\"terminal\":\n  'status': \"FALSE\" # disable\n",
			wantStatus: boolPtr(false),
		},
		{
			name: "models followed by terminal",
			input: strings.Join([]string{
				"models:",
				"  aliases:",
				"    sol: openai-codex/gpt-6.2-sol",
				"terminal:",
				"  status: false",
				"",
			}, "\n"),
			want: map[string]string{
				"sol": "openai-codex/gpt-6.2-sol",
			},
			wantStatus: boolPtr(false),
		},
		{
			name: "terminal followed by models with distinct indentation",
			input: strings.Join([]string{
				"terminal:",
				"    status: on",
				"models:",
				"  aliases:",
				"    fast: anthropic/claude-haiku-4-5:low",
				"",
			}, "\n"),
			want: map[string]string{
				"fast": "anthropic/claude-haiku-4-5:low",
			},
			wantStatus: boolPtr(true),
		},
		{
			name:       "empty models followed by terminal",
			input:      "models:\nterminal:\n  status: 0\n",
			wantStatus: boolPtr(false),
		},
		{
			name:       "empty aliases followed by terminal",
			input:      "models:\n  aliases:\nterminal:\n  status: 1\n",
			wantStatus: boolPtr(true),
		},
		{
			name:  "empty terminal followed by models",
			input: "terminal:\nmodels:\n  aliases:\n    sol: openai-codex/gpt-6.2-sol\n",
			want: map[string]string{
				"sol": "openai-codex/gpt-6.2-sol",
			},
		},
		{
			name: "plain aliases with colons and comments",
			input: strings.Join([]string{
				"# Model aliases",
				"models:",
				"  aliases:",
				"    sol: openai-codex/gpt-6.2-sol",
				"    fast: anthropic/claude-haiku-4-5:low # inline comment",
				"    local: ollama/llama3:8b   ",
				"    tagged: provider/model#v2 # hash without space is preserved",
				"",
			}, "\n"),
			want: map[string]string{
				"sol":    "openai-codex/gpt-6.2-sol",
				"fast":   "anthropic/claude-haiku-4-5:low",
				"local":  "ollama/llama3:8b",
				"tagged": "provider/model#v2",
			},
		},
		{
			name: "quoted keys and values preserving hashes and escapes",
			input: strings.Join([]string{
				`"models":`,
				`  'aliases':`,
				`    'single': 'openai-codex/gpt-6.2-sol # keep hash' # real comment`,
				`    "double": "provider/model:high # also keep" # real comment`,
				`    escaped-sq: 'it''s:low'`,
				`    escaped-dq: "a\"b\\c\/d\u003ahigh"`,
				`    spaced-colon :  openai-codex/gpt-6.2-sol:medium`,
			}, "\n"),
			want: map[string]string{
				"single":       "openai-codex/gpt-6.2-sol # keep hash",
				"double":       "provider/model:high # also keep",
				"escaped-sq":   "it's:low",
				"escaped-dq":   `a"b\c/d:high`,
				"spaced-colon": "openai-codex/gpt-6.2-sol:medium",
			},
		},
		{
			name:  "CRLF line endings without trailing newline",
			input: "models:\r\n  aliases:\r\n    sol: openai-codex/gpt-6.2-sol\r\n    fast: anthropic/claude-haiku-4-5:low\r\nterminal:\r\n  status: off",
			want: map[string]string{
				"sol":  "openai-codex/gpt-6.2-sol",
				"fast": "anthropic/claude-haiku-4-5:low",
			},
			wantStatus: boolPtr(false),
		},
		{
			name:  "non-2-space consistent indentation",
			input: "models:\n   aliases:\n        sol: openai-codex/gpt-6.2-sol\n        fast: anthropic/claude-haiku-4-5:low\n",
			want: map[string]string{
				"sol":  "openai-codex/gpt-6.2-sol",
				"fast": "anthropic/claude-haiku-4-5:low",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := parseUserConfig([]byte(tc.input))
			if err != nil {
				t.Fatalf("parseUserConfig(%q) unexpected error: %v", tc.input, err)
			}
			if !maps.Equal(cfg.modelAliases, tc.want) {
				t.Fatalf("aliases = %#v, want %#v", cfg.modelAliases, tc.want)
			}
			if (cfg.terminalStatus == nil) != (tc.wantStatus == nil) || (cfg.terminalStatus != nil && *cfg.terminalStatus != *tc.wantStatus) {
				t.Fatalf("terminalStatus = %v, want %v", cfg.terminalStatus, tc.wantStatus)
			}
		})
	}
}

func TestParseUserConfigErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		input    []byte
		wantLine string
		wantMsg  string
	}{
		{
			name:     "invalid UTF-8 on line 1",
			input:    []byte{0xff, 0xfe},
			wantLine: "line 1:",
			wantMsg:  "invalid UTF-8",
		},
		{
			name:     "invalid UTF-8 on line 3",
			input:    []byte("models:\n  aliases:\n    sol: \xff\n"),
			wantLine: "line 3:",
			wantMsg:  "invalid UTF-8",
		},
		{
			name:     "bare CR",
			input:    []byte("models:\r  aliases:\n"),
			wantLine: "line 1:",
			wantMsg:  "control character",
		},
		{
			name:     "NUL byte",
			input:    []byte("models:\n  aliases:\n    sol: foo\x00bar\n"),
			wantLine: "line 3:",
			wantMsg:  "control character",
		},
		{
			name:     "unknown top-level key",
			input:    []byte("other:\n"),
			wantLine: "line 1:",
			wantMsg:  `unknown top-level key "other"`,
		},
		{
			name:     "unknown key under models",
			input:    []byte("models:\n  default: sol\n"),
			wantLine: "line 2:",
			wantMsg:  `unknown key "default" under models`,
		},
		{
			name:     "unknown key under models after empty aliases",
			input:    []byte("models:\n  aliases:\n  extra: foo\n"),
			wantLine: "line 3:",
			wantMsg:  `unknown key "extra" under models`,
		},
		{
			name:     "duplicate top-level models",
			input:    []byte("models:\nmodels:\n"),
			wantLine: "line 2:",
			wantMsg:  `duplicate key "models"`,
		},
		{
			name:     "duplicate top-level models quoted",
			input:    []byte("models:\n  aliases:\n    sol: a\n\"models\":\n"),
			wantLine: "line 4:",
			wantMsg:  `duplicate key "models"`,
		},
		{
			name:     "duplicate aliases under models",
			input:    []byte("models:\n  aliases:\n    sol: a\n  aliases:\n"),
			wantLine: "line 4:",
			wantMsg:  `duplicate key "aliases" under models`,
		},
		{
			name:     "duplicate alias name plain",
			input:    []byte("models:\n  aliases:\n    sol: a\n    sol: b\n"),
			wantLine: "line 4:",
			wantMsg:  `duplicate model alias "sol"`,
		},
		{
			name:     "duplicate alias name quoted",
			input:    []byte("models:\n  aliases:\n    sol: a\n    'sol': b\n"),
			wantLine: "line 4:",
			wantMsg:  `duplicate model alias "sol"`,
		},
		{
			name:     "scalar value for models",
			input:    []byte("models: sol\n"),
			wantLine: "line 1:",
			wantMsg:  `"models" must be a mapping`,
		},
		{
			name:     "scalar value for aliases",
			input:    []byte("models:\n  aliases: sol\n"),
			wantLine: "line 2:",
			wantMsg:  `"aliases" must be a mapping`,
		},
		{
			name:     "empty alias name quoted",
			input:    []byte("models:\n  aliases:\n    \"\": openai-codex/gpt-6.2-sol\n"),
			wantLine: "line 3:",
			wantMsg:  "must not be empty",
		},
		{
			name:     "whitespace-only alias name",
			input:    []byte("models:\n  aliases:\n    '   ': openai-codex/gpt-6.2-sol\n"),
			wantLine: "line 3:",
			wantMsg:  "must not be empty",
		},
		{
			name:     "empty alias name before colon",
			input:    []byte("models:\n  aliases:\n    : openai-codex/gpt-6.2-sol\n"),
			wantLine: "line 3:",
			wantMsg:  "must not be empty",
		},
		{
			name:     "alias name with space unquoted",
			input:    []byte("models:\n  aliases:\n    my sol: openai-codex/gpt-6.2-sol\n"),
			wantLine: "line 3:",
			wantMsg:  "must not contain whitespace",
		},
		{
			name:     "alias name with tab quoted",
			input:    []byte("models:\n  aliases:\n    \"my\\tsol\": openai-codex/gpt-6.2-sol\n"),
			wantLine: "line 3:",
			wantMsg:  "must not contain whitespace",
		},
		{
			name:     "alias name with colon unquoted",
			input:    []byte("models:\n  aliases:\n    sol:high: openai-codex/gpt-6.2-sol:high\n"),
			wantLine: "line 3:",
			wantMsg:  "must not contain ':'",
		},
		{
			name:     "alias name with colon quoted",
			input:    []byte("models:\n  aliases:\n    \"sol:high\": openai-codex/gpt-6.2-sol:high\n"),
			wantLine: "line 3:",
			wantMsg:  "must not contain ':'",
		},
		{
			name:     "missing alias target value",
			input:    []byte("models:\n  aliases:\n    sol:\n"),
			wantLine: "line 3:",
			wantMsg:  "requires a nonempty value",
		},
		{
			name:     "comment-only alias target value",
			input:    []byte("models:\n  aliases:\n    sol: # comment\n"),
			wantLine: "line 3:",
			wantMsg:  "requires a nonempty value",
		},
		{
			name:     "empty quoted alias target value",
			input:    []byte("models:\n  aliases:\n    sol: \"\"\n"),
			wantLine: "line 3:",
			wantMsg:  "requires a nonempty value",
		},
		{
			name:     "whitespace-only quoted alias target value",
			input:    []byte("models:\n  aliases:\n    sol: '   '\n"),
			wantLine: "line 3:",
			wantMsg:  "requires a nonempty value",
		},
		{
			name:     "escaped newline in alias target value",
			input:    []byte("models:\n  aliases:\n    sol: \"a\\nb\"\n"),
			wantLine: "line 3:",
			wantMsg:  "must be a single line",
		},
		{
			name:     "tab indentation on aliases",
			input:    []byte("models:\n\taliases:\n"),
			wantLine: "line 2:",
			wantMsg:  "tabs are not allowed",
		},
		{
			name:     "mixed space and tab indentation",
			input:    []byte("models:\n  aliases:\n  \tsol: foo\n"),
			wantLine: "line 3:",
			wantMsg:  "tabs are not allowed",
		},
		{
			name:     "leading indentation at root",
			input:    []byte("  models:\n"),
			wantLine: "line 1:",
			wantMsg:  "unexpected indentation",
		},
		{
			name:     "inconsistent indentation under aliases (fewer spaces)",
			input:    []byte("models:\n  aliases:\n    sol: a\n   fast: b\n"),
			wantLine: "line 4:",
			wantMsg:  "inconsistent indentation",
		},
		{
			name:     "inconsistent indentation under aliases (more spaces)",
			input:    []byte("models:\n  aliases:\n    sol: a\n      fast: b\n"),
			wantLine: "line 4:",
			wantMsg:  "inconsistent indentation",
		},
		{
			name:     "inconsistent dedent below modelsIndent",
			input:    []byte("models:\n    aliases:\n  other: x\n"),
			wantLine: "line 3:",
			wantMsg:  "inconsistent indentation",
		},
		{
			name:     "flow mapping on models",
			input:    []byte("models: {}\n"),
			wantLine: "line 1:",
			wantMsg:  "flow collections are not supported",
		},
		{
			name:     "flow sequence on aliases",
			input:    []byte("models:\n  aliases: []\n"),
			wantLine: "line 2:",
			wantMsg:  "flow collections are not supported",
		},
		{
			name:     "flow mapping at root",
			input:    []byte("{models: {}}\n"),
			wantLine: "line 1:",
			wantMsg:  "flow collections are not supported",
		},
		{
			name:     "flow sequence as alias value",
			input:    []byte("models:\n  aliases:\n    sol: [a, b]\n"),
			wantLine: "line 3:",
			wantMsg:  "flow collections are not supported",
		},
		{
			name:     "sequence under aliases",
			input:    []byte("models:\n  aliases:\n    - sol: a\n"),
			wantLine: "line 3:",
			wantMsg:  "sequences are not supported",
		},
		{
			name:     "block scalar literal",
			input:    []byte("models:\n  aliases:\n    sol: |\n      openai-codex/gpt-6.2-sol\n"),
			wantLine: "line 3:",
			wantMsg:  "multiline scalars are not supported",
		},
		{
			name:     "block scalar folded",
			input:    []byte("models:\n  aliases:\n    sol: >-\n      openai-codex/gpt-6.2-sol\n"),
			wantLine: "line 3:",
			wantMsg:  "multiline scalars are not supported",
		},
		{
			name:     "multiline double-quoted scalar",
			input:    []byte("models:\n  aliases:\n    sol: \"openai-codex/\n      gpt-6.2-sol\"\n"),
			wantLine: "line 3:",
			wantMsg:  "unterminated double-quoted scalar",
		},
		{
			name:     "multiline single-quoted scalar",
			input:    []byte("models:\n  aliases:\n    sol: 'openai-codex/\n      gpt-6.2-sol'\n"),
			wantLine: "line 3:",
			wantMsg:  "unterminated single-quoted scalar",
		},
		{
			name:     "multiline plain continuation",
			input:    []byte("models:\n  aliases:\n    sol: openai-codex/\n      gpt-6.2-sol\n"),
			wantLine: "line 4:",
			wantMsg:  "expected mapping entry",
		},
		{
			name:     "anchor on value",
			input:    []byte("models:\n  aliases:\n    sol: &base openai-codex/gpt-6.2-sol\n"),
			wantLine: "line 3:",
			wantMsg:  "anchors and aliases are not supported",
		},
		{
			name:     "alias reference on value",
			input:    []byte("models:\n  aliases:\n    fast: *base\n"),
			wantLine: "line 3:",
			wantMsg:  "anchors and aliases are not supported",
		},
		{
			name:     "tag on value",
			input:    []byte("models:\n  aliases:\n    sol: !!str openai-codex/gpt-6.2-sol\n"),
			wantLine: "line 3:",
			wantMsg:  "tags are not supported",
		},
		{
			name:     "document marker",
			input:    []byte("---\nmodels:\n"),
			wantLine: "line 1:",
			wantMsg:  "document markers are not supported",
		},
		{
			name:     "YAML directive",
			input:    []byte("%YAML 1.2\nmodels:\n"),
			wantLine: "line 1:",
			wantMsg:  "YAML directives are not supported",
		},
		{
			name:     "explicit mapping key",
			input:    []byte("? models\n"),
			wantLine: "line 1:",
			wantMsg:  "explicit mapping keys are not supported",
		},
		{
			name:     "trailing characters after quoted scalar",
			input:    []byte("models:\n  aliases:\n    sol: \"foo\"bar\n"),
			wantLine: "line 3:",
			wantMsg:  "unexpected trailing characters",
		},
		{
			name:     "hash without whitespace after quoted scalar",
			input:    []byte("models:\n  aliases:\n    sol: 'foo'#comment\n"),
			wantLine: "line 3:",
			wantMsg:  "unexpected trailing characters",
		},
		{
			name:     "inline mapping colon in plain value",
			input:    []byte("models:\n  aliases:\n    sol: foo: bar\n"),
			wantLine: "line 3:",
			wantMsg:  "unexpected ':'",
		},
		{
			name:     "trailing colon in plain value",
			input:    []byte("models:\n  aliases:\n    sol: foo:\n"),
			wantLine: "line 3:",
			wantMsg:  "unexpected ':'",
		},
		{
			name:     "missing whitespace after colon",
			input:    []byte("models:#comment\n"),
			wantLine: "line 1:",
			wantMsg:  "expected mapping entry",
		},
		{
			name:     "unsupported double-quoted escape",
			input:    []byte("models:\n  aliases:\n    sol: \"foo\\qbar\"\n"),
			wantLine: "line 3:",
			wantMsg:  "unsupported escape sequence",
		},
		{
			name:     "unknown key under terminal",
			input:    []byte("terminal:\n  bell: false\n"),
			wantLine: "line 2:",
			wantMsg:  `unknown key "bell" under terminal`,
		},
		{
			name:     "duplicate top-level terminal",
			input:    []byte("terminal:\nterminal:\n"),
			wantLine: "line 2:",
			wantMsg:  `duplicate key "terminal"`,
		},
		{
			name:     "duplicate top-level terminal quoted",
			input:    []byte("terminal:\n  status: false\n\"terminal\":\n"),
			wantLine: "line 3:",
			wantMsg:  `duplicate key "terminal"`,
		},
		{
			name:     "duplicate status under terminal",
			input:    []byte("terminal:\n  status: true\n  status: false\n"),
			wantLine: "line 3:",
			wantMsg:  `duplicate key "status" under terminal`,
		},
		{
			name:     "duplicate status under terminal quoted",
			input:    []byte("terminal:\n  status: true\n  'status': false\n"),
			wantLine: "line 3:",
			wantMsg:  `duplicate key "status" under terminal`,
		},
		{
			name:     "scalar value for terminal",
			input:    []byte("terminal: false\n"),
			wantLine: "line 1:",
			wantMsg:  `"terminal" must be a mapping`,
		},
		{
			name:     "flow mapping on terminal",
			input:    []byte("terminal: {}\n"),
			wantLine: "line 1:",
			wantMsg:  "flow collections are not supported",
		},
		{
			name:     "flow sequence on terminal status",
			input:    []byte("terminal:\n  status: []\n"),
			wantLine: "line 2:",
			wantMsg:  "flow collections are not supported",
		},
		{
			name:     "sequence under terminal",
			input:    []byte("terminal:\n  - status: false\n"),
			wantLine: "line 2:",
			wantMsg:  "sequences are not supported",
		},
		{
			name:     "missing terminal status value",
			input:    []byte("terminal:\n  status:\n"),
			wantLine: "line 2:",
			wantMsg:  "terminal.status requires a boolean value",
		},
		{
			name:     "comment-only terminal status value",
			input:    []byte("terminal:\n  status: # comment\n"),
			wantLine: "line 2:",
			wantMsg:  "terminal.status requires a boolean value",
		},
		{
			name:     "empty quoted terminal status value",
			input:    []byte("terminal:\n  status: \"\"\n"),
			wantLine: "line 2:",
			wantMsg:  "terminal.status requires a boolean value",
		},
		{
			name:     "whitespace-only quoted terminal status value",
			input:    []byte("terminal:\n  status: '   '\n"),
			wantLine: "line 2:",
			wantMsg:  "terminal.status requires a boolean value",
		},
		{
			name:     "unrecognized terminal status value yes",
			input:    []byte("terminal:\n  status: yes\n"),
			wantLine: "line 2:",
			wantMsg:  `terminal.status: invalid boolean value "yes"`,
		},
		{
			name:     "unrecognized terminal status value 2",
			input:    []byte("terminal:\n  status: 2\n"),
			wantLine: "line 2:",
			wantMsg:  `terminal.status: invalid boolean value "2"`,
		},
		{
			name:     "quoted terminal status value with spaces",
			input:    []byte("terminal:\n  status: \" true \"\n"),
			wantLine: "line 2:",
			wantMsg:  `terminal.status: invalid boolean value " true "`,
		},
		{
			name:     "escaped newline in terminal status value",
			input:    []byte("terminal:\n  status: \"false\\n\"\n"),
			wantLine: "line 2:",
			wantMsg:  `terminal.status: invalid boolean value "false\n"`,
		},
		{
			name:     "tab indentation under terminal",
			input:    []byte("terminal:\n\tstatus: false\n"),
			wantLine: "line 2:",
			wantMsg:  "tabs are not allowed",
		},
		{
			name:     "inconsistent indentation under terminal",
			input:    []byte("terminal:\n  status: false\n    nested: true\n"),
			wantLine: "line 3:",
			wantMsg:  "inconsistent indentation",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseUserConfig(tc.input)
			if err == nil {
				t.Fatalf("parseUserConfig(%q): expected error containing %q and %q, got nil", tc.input, tc.wantLine, tc.wantMsg)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.wantLine) || !strings.Contains(msg, tc.wantMsg) {
				t.Fatalf("parseUserConfig(%q): error = %q, want both %q and %q", tc.input, msg, tc.wantLine, tc.wantMsg)
			}
		})
	}
}

func TestResolveModel(t *testing.T) {
	t.Parallel()
	cfg := userConfig{
		modelAliases: map[string]string{
			"sol":          "openai-codex/gpt-6.2-sol",
			"fast":         "anthropic/claude-haiku-4-5:low",
			"local":        "ollama/llama3:8b",
			"local-low":    "ollama/llama3:8b:low",
			"chain":        "sol",
			"level-off":    "p/m:off",
			"level-min":    "p/m:minimal",
			"level-low":    "p/m:low",
			"level-med":    "p/m:medium",
			"level-high":   "p/m:high",
			"level-xhigh":  "p/m:xhigh",
			"level-max":    "p/m:max",
			"custom-colon": "p/m:custom",
		},
	}

	cases := []struct {
		in   string
		want string
	}{
		// Exact matches
		{"sol", "openai-codex/gpt-6.2-sol"},
		{"fast", "anthropic/claude-haiku-4-5:low"},
		{"local", "ollama/llama3:8b"},
		// Single-step non-chaining
		{"chain", "sol"},
		{"chain:high", "sol:high"},
		// Append thinking level
		{"sol:high", "openai-codex/gpt-6.2-sol:high"},
		{"local:high", "ollama/llama3:8b:high"},
		{"custom-colon:high", "p/m:custom:high"},
		// Override existing Pi thinking-level suffixes
		{"fast:high", "anthropic/claude-haiku-4-5:high"},
		{"local-low:high", "ollama/llama3:8b:high"},
		{"level-off:high", "p/m:high"},
		{"level-min:high", "p/m:high"},
		{"level-low:high", "p/m:high"},
		{"level-med:high", "p/m:high"},
		{"level-high:off", "p/m:off"},
		{"level-xhigh:low", "p/m:low"},
		{"level-max:minimal", "p/m:minimal"},
		// Unmatched passthrough and edge cases
		{"openai-codex/gpt-6.2-sol:high", "openai-codex/gpt-6.2-sol:high"},
		{"unknown", "unknown"},
		{"unknown:high", "unknown:high"},
		{"sol:", "sol:"},
		{":high", ":high"},
		{"sol:a:b", "sol:a:b"},
		{"", ""},
	}

	for _, tc := range cases {
		if got := cfg.resolveModel(tc.in); got != tc.want {
			t.Errorf("resolveModel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLoadUserConfigXDGAndErrors(t *testing.T) {
	t.Parallel()

	writeConfig := func(t *testing.T, path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("absolute XDG_CONFIG_HOME takes precedence over HOME", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		xdg := t.TempDir()
		writeConfig(t, filepath.Join(home, ".config", "tasks", "config.yml"), "models:\n  aliases:\n    sol: from-home\n")
		writeConfig(t, filepath.Join(xdg, "tasks", "config.yml"), "models:\n  aliases:\n    sol: from-xdg\n")

		cfg, err := loadUserConfig(IO{Env: []string{"HOME=" + home, "XDG_CONFIG_HOME=" + xdg}})
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.resolveModel("sol"); got != "from-xdg" {
			t.Fatalf("resolveModel(sol) = %q, want from-xdg", got)
		}
	})

	t.Run("absolute XDG_CONFIG_HOME missing does not fall back to HOME", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		xdg := t.TempDir()
		writeConfig(t, filepath.Join(home, ".config", "tasks", "config.yml"), "models:\n  aliases:\n    sol: from-home\n")

		cfg, err := loadUserConfig(IO{Env: []string{"HOME=" + home, "XDG_CONFIG_HOME=" + xdg}})
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.resolveModel("sol"); got != "sol" {
			t.Fatalf("resolveModel(sol) = %q, want sol", got)
		}
	})

	t.Run("relative XDG_CONFIG_HOME ignored and falls back to HOME", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		writeConfig(t, filepath.Join(home, ".config", "tasks", "config.yml"), "models:\n  aliases:\n    sol: from-home\n")

		cfg, err := loadUserConfig(IO{Env: []string{"HOME=" + home, "XDG_CONFIG_HOME=relative/xdg"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.resolveModel("sol"); got != "from-home" {
			t.Fatalf("resolveModel(sol) = %q, want from-home", got)
		}
	})

	t.Run("unset or empty XDG_CONFIG_HOME falls back to HOME", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		writeConfig(t, filepath.Join(home, ".config", "tasks", "config.yml"), "models:\n  aliases:\n    sol: from-home\n")

		for _, env := range [][]string{
			{"HOME=" + home},
			{"HOME=" + home, "XDG_CONFIG_HOME="},
		} {
			cfg, err := loadUserConfig(IO{Env: env})
			if err != nil {
				t.Fatalf("env %v: %v", env, err)
			}
			if got := cfg.resolveModel("sol"); got != "from-home" {
				t.Fatalf("env %v: resolveModel(sol) = %q, want from-home", env, got)
			}
		}
	})

	t.Run("missing config file treated as empty", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		cfg, err := loadUserConfig(IO{Env: []string{"HOME=" + home}})
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.modelAliases) != 0 {
			t.Fatalf("expected empty aliases, got %#v", cfg.modelAliases)
		}
	})

	t.Run("unset HOME and unset XDG_CONFIG_HOME treated as empty", func(t *testing.T) {
		t.Parallel()
		cfg, err := loadUserConfig(IO{Env: []string{}})
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.modelAliases) != 0 {
			t.Fatalf("expected empty aliases, got %#v", cfg.modelAliases)
		}
	})

	t.Run("config path is a directory returns error", func(t *testing.T) {
		t.Parallel()
		xdg := t.TempDir()
		dirPath := filepath.Join(xdg, "tasks", "config.yml")
		if err := os.MkdirAll(dirPath, 0700); err != nil {
			t.Fatal(err)
		}

		_, err := loadUserConfig(IO{Env: []string{"XDG_CONFIG_HOME=" + xdg}})
		if err == nil || !strings.Contains(err.Error(), dirPath) {
			t.Fatalf("expected error referencing %s, got %v", dirPath, err)
		}
	})

	t.Run("unreadable config file returns error", func(t *testing.T) {
		t.Parallel()
		if os.Geteuid() == 0 {
			t.Skip("root can read 0000 files")
		}
		xdg := t.TempDir()
		cfgPath := filepath.Join(xdg, "tasks", "config.yml")
		writeConfig(t, cfgPath, "models:\n  aliases:\n    sol: foo\n")
		if err := os.Chmod(cfgPath, 0000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(cfgPath, 0600) })

		_, err := loadUserConfig(IO{Env: []string{"XDG_CONFIG_HOME=" + xdg}})
		if err == nil || !strings.Contains(err.Error(), cfgPath) {
			t.Fatalf("expected permission error referencing %s, got %v", cfgPath, err)
		}
	})

	t.Run("symlink to valid config file loads cleanly", func(t *testing.T) {
		t.Parallel()
		xdg := t.TempDir()
		target := filepath.Join(t.TempDir(), "dotfiles-config.yml")
		writeConfig(t, target, "models:\n  aliases:\n    sol: from-symlink\n")
		link := filepath.Join(xdg, "tasks", "config.yml")
		if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}

		cfg, err := loadUserConfig(IO{Env: []string{"XDG_CONFIG_HOME=" + xdg}})
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.resolveModel("sol"); got != "from-symlink" {
			t.Fatalf("resolveModel(sol) = %q, want from-symlink", got)
		}
	})
}

func TestResolveTerminalStatus(t *testing.T) {
	t.Parallel()

	t.Run("precedence and defaults", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name string
			cfg  userConfig
			env  []string
			want bool
		}{
			{name: "default true when both unset", cfg: userConfig{}, env: []string{}, want: true},
			{name: "default true when env empty", cfg: userConfig{}, env: []string{"TASKS_TERMINAL_STATUS="}, want: true},
			{name: "config false with env unset", cfg: userConfig{terminalStatus: boolPtr(false)}, env: []string{}, want: false},
			{name: "config false with env empty", cfg: userConfig{terminalStatus: boolPtr(false)}, env: []string{"TASKS_TERMINAL_STATUS="}, want: false},
			{name: "config true with env unset", cfg: userConfig{terminalStatus: boolPtr(true)}, env: []string{}, want: true},
			{name: "config true with env empty", cfg: userConfig{terminalStatus: boolPtr(true)}, env: []string{"TASKS_TERMINAL_STATUS="}, want: true},
			{name: "env 1 overrides config false", cfg: userConfig{terminalStatus: boolPtr(false)}, env: []string{"TASKS_TERMINAL_STATUS=1"}, want: true},
			{name: "env true overrides config false", cfg: userConfig{terminalStatus: boolPtr(false)}, env: []string{"TASKS_TERMINAL_STATUS=true"}, want: true},
			{name: "env TRUE overrides config false", cfg: userConfig{terminalStatus: boolPtr(false)}, env: []string{"TASKS_TERMINAL_STATUS=TRUE"}, want: true},
			{name: "env On overrides config false", cfg: userConfig{terminalStatus: boolPtr(false)}, env: []string{"TASKS_TERMINAL_STATUS=On"}, want: true},
			{name: "env 0 overrides config true", cfg: userConfig{terminalStatus: boolPtr(true)}, env: []string{"TASKS_TERMINAL_STATUS=0"}, want: false},
			{name: "env false overrides config true", cfg: userConfig{terminalStatus: boolPtr(true)}, env: []string{"TASKS_TERMINAL_STATUS=false"}, want: false},
			{name: "env FALSE overrides config true", cfg: userConfig{terminalStatus: boolPtr(true)}, env: []string{"TASKS_TERMINAL_STATUS=FALSE"}, want: false},
			{name: "env Off overrides config true", cfg: userConfig{terminalStatus: boolPtr(true)}, env: []string{"TASKS_TERMINAL_STATUS=Off"}, want: false},
			{name: "env 0 overrides default", cfg: userConfig{}, env: []string{"TASKS_TERMINAL_STATUS=0"}, want: false},
			{name: "env off overrides default", cfg: userConfig{}, env: []string{"TASKS_TERMINAL_STATUS=off"}, want: false},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := resolveTerminalStatus(tc.cfg, IO{Env: tc.env})
				if err != nil {
					t.Fatalf("resolveTerminalStatus() unexpected error: %v", err)
				}
				if got != tc.want {
					t.Fatalf("resolveTerminalStatus() = %v, want %v", got, tc.want)
				}
			})
		}
	})

	t.Run("invalid nonempty env values return error", func(t *testing.T) {
		t.Parallel()
		for _, bad := range []string{" ", "yes", "no", "2", "flase", " true ", "false\n"} {
			t.Run(bad, func(t *testing.T) {
				t.Parallel()
				_, err := resolveTerminalStatus(userConfig{}, IO{Env: []string{"TASKS_TERMINAL_STATUS=" + bad}})
				if err == nil {
					t.Fatalf("TASKS_TERMINAL_STATUS=%q: expected error, got nil", bad)
				}
				msg := err.Error()
				if !strings.Contains(msg, "TASKS_TERMINAL_STATUS") || !strings.Contains(msg, "invalid boolean value") {
					t.Fatalf("TASKS_TERMINAL_STATUS=%q: unexpected error %q", bad, msg)
				}
			})
		}
	})
}

func TestFormatUserConfig(t *testing.T) {
	t.Parallel()

	t.Run("empty configs format as empty string", func(t *testing.T) {
		t.Parallel()
		for _, cfg := range []userConfig{
			{},
			{modelAliases: map[string]string{}},
		} {
			if got := formatUserConfig(cfg); got != "" {
				t.Fatalf("formatUserConfig(%#v) = %q, want empty", cfg, got)
			}
		}
	})

	t.Run("standard aliases are sorted and plain", func(t *testing.T) {
		t.Parallel()
		cfg := userConfig{
			modelAliases: map[string]string{
				"sol":    "openai-codex/gpt-6.2-sol",
				"fast":   "anthropic/claude-haiku-4-5:low",
				"local":  "ollama/llama3:8b",
				"tagged": "provider/model#v2",
			},
		}
		want := strings.Join([]string{
			"models:",
			"  aliases:",
			"    fast: anthropic/claude-haiku-4-5:low",
			"    local: ollama/llama3:8b",
			"    sol: openai-codex/gpt-6.2-sol",
			"    tagged: provider/model#v2",
			"",
		}, "\n")
		got := formatUserConfig(cfg)
		if got != want {
			t.Fatalf("formatUserConfig() =\n%s\nwant:\n%s", got, want)
		}
		parsed, err := parseUserConfig([]byte(got))
		if err != nil {
			t.Fatalf("round-trip parse failed: %v", err)
		}
		if !maps.Equal(parsed.modelAliases, cfg.modelAliases) {
			t.Fatalf("round-trip aliases = %#v, want %#v", parsed.modelAliases, cfg.modelAliases)
		}
	})

	t.Run("special keys and values are quoted and round-trip cleanly", func(t *testing.T) {
		t.Parallel()
		cfg := userConfig{
			modelAliases: map[string]string{
				"#hash":     "provider/model # comment",
				"---":       "foo: bar",
				"...":       "foo:",
				"-":         "- item",
				"?":         "? key",
				"%pct":      ": val",
				"'quoted'":  " leading-and-trailing ",
				`"dq"`:      "tab\there",
				"esc":       "a\x1b\"b\\c\x01\x7f",
				"unicode":   "provider/modèl-é",
				"plain-ish": "it's:low",
			},
		}
		got := formatUserConfig(cfg)
		if strings.ContainsAny(got, "\x1b\x01\x7f") {
			t.Fatalf("formatted config must not contain raw control characters: %q", got)
		}
		parsed, err := parseUserConfig([]byte(got))
		if err != nil {
			t.Fatalf("round-trip parse failed for:\n%s\nerr: %v", got, err)
		}
		if !maps.Equal(parsed.modelAliases, cfg.modelAliases) {
			t.Fatalf("round-trip aliases = %#v, want %#v", parsed.modelAliases, cfg.modelAliases)
		}
	})

	t.Run("terminal status formats as normalized boolean and round-trips", func(t *testing.T) {
		t.Parallel()
		for _, value := range []bool{false, true} {
			cfg := userConfig{terminalStatus: boolPtr(value)}
			want := "terminal:\n  status: " + map[bool]string{false: "false", true: "true"}[value] + "\n"
			got := formatUserConfig(cfg)
			if got != want {
				t.Fatalf("formatUserConfig(%v) = %q, want %q", value, got, want)
			}
			parsed, err := parseUserConfig([]byte(got))
			if err != nil {
				t.Fatalf("round-trip parse failed: %v", err)
			}
			if parsed.terminalStatus == nil || *parsed.terminalStatus != value {
				t.Fatalf("round-trip terminalStatus = %v, want %v", parsed.terminalStatus, value)
			}
		}
	})

	t.Run("models aliases and terminal status format together and round-trip", func(t *testing.T) {
		t.Parallel()
		cfg := userConfig{
			modelAliases: map[string]string{
				"sol":  "openai-codex/gpt-6.2-sol",
				"fast": "anthropic/claude-haiku-4-5:low",
			},
			terminalStatus: boolPtr(false),
		}
		want := strings.Join([]string{
			"models:",
			"  aliases:",
			"    fast: anthropic/claude-haiku-4-5:low",
			"    sol: openai-codex/gpt-6.2-sol",
			"terminal:",
			"  status: false",
			"",
		}, "\n")
		got := formatUserConfig(cfg)
		if got != want {
			t.Fatalf("formatUserConfig() =\n%s\nwant:\n%s", got, want)
		}
		parsed, err := parseUserConfig([]byte(got))
		if err != nil {
			t.Fatalf("round-trip parse failed: %v", err)
		}
		if !maps.Equal(parsed.modelAliases, cfg.modelAliases) || parsed.terminalStatus == nil || *parsed.terminalStatus != false {
			t.Fatalf("round-trip config = %#v, want %#v", parsed, cfg)
		}
	})
}

func TestConfigCommand(t *testing.T) {
	t.Parallel()

	writeConfig := func(t *testing.T, path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	runConfig := func(cwd string, env []string, args ...string) (string, string, error) {
		var out, stderr bytes.Buffer
		err := Run(context.Background(), cwd, args, IO{Out: &out, Err: &stderr, Env: env})
		return out.String(), stderr.String(), err
	}

	// Config does not require a Git repository or even an existing cwd.
	outsideRepo := filepath.Join(t.TempDir(), "missing")

	t.Run("missing config prints nothing", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		out, stderr, err := runConfig(outsideRepo, []string{"HOME=" + home}, "config")
		if err != nil || out != "" || stderr != "" {
			t.Fatalf("got out=%q stderr=%q err=%v, want empty and nil", out, stderr, err)
		}
	})

	t.Run("unset HOME and XDG_CONFIG_HOME prints nothing", func(t *testing.T) {
		t.Parallel()
		out, stderr, err := runConfig(outsideRepo, []string{}, "config")
		if err != nil || out != "" || stderr != "" {
			t.Fatalf("got out=%q stderr=%q err=%v, want empty and nil", out, stderr, err)
		}
	})

	t.Run("empty and comment-only configs print nothing", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name    string
			content string
		}{
			{"empty", ""},
			{"comments only", "# just a comment\n\n"},
			{"empty models", "models:\n"},
			{"empty aliases", "models:\n  aliases:\n"},
			{"empty terminal", "terminal:\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				xdg := t.TempDir()
				writeConfig(t, filepath.Join(xdg, "tasks", "config.yml"), tc.content)
				out, stderr, err := runConfig(outsideRepo, []string{"XDG_CONFIG_HOME=" + xdg}, "config")
				if err != nil || out != "" || stderr != "" {
					t.Fatalf("got out=%q stderr=%q err=%v, want empty and nil", out, stderr, err)
				}
			})
		}
	})

	t.Run("prints normalized parsed config from HOME", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		writeConfig(t, filepath.Join(home, ".config", "tasks", "config.yml"), strings.Join([]string{
			"# User config",
			"\"models\":",
			"   'aliases':",
			"        sol: openai-codex/gpt-6.2-sol # comment",
			"        fast: 'anthropic/claude-haiku-4-5:low'",
			"        hashed: 'provider/model # literal'",
		}, "\r\n"))

		out, stderr, err := runConfig(outsideRepo, []string{"HOME=" + home}, "config")
		want := strings.Join([]string{
			"models:",
			"  aliases:",
			"    fast: anthropic/claude-haiku-4-5:low",
			`    hashed: "provider/model # literal"`,
			"    sol: openai-codex/gpt-6.2-sol",
			"",
		}, "\n")
		if err != nil || out != want || stderr != "" {
			t.Fatalf("got out=%q stderr=%q err=%v, want out=%q", out, stderr, err, want)
		}
	})

	t.Run("respects XDG_CONFIG_HOME precedence and fallback", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		xdg := t.TempDir()
		emptyXDG := t.TempDir()
		writeConfig(t, filepath.Join(home, ".config", "tasks", "config.yml"), "models:\n  aliases:\n    sol: from-home\n")
		writeConfig(t, filepath.Join(xdg, "tasks", "config.yml"), "models:\n  aliases:\n    sol: from-xdg\n")

		out, stderr, err := runConfig(outsideRepo, []string{"HOME=" + home, "XDG_CONFIG_HOME=" + xdg}, "config")
		wantXDG := "models:\n  aliases:\n    sol: from-xdg\n"
		if err != nil || out != wantXDG || stderr != "" {
			t.Fatalf("xdg: got out=%q stderr=%q err=%v, want %q", out, stderr, err, wantXDG)
		}

		out, stderr, err = runConfig(outsideRepo, []string{"HOME=" + home, "XDG_CONFIG_HOME=" + emptyXDG}, "config")
		if err != nil || out != "" || stderr != "" {
			t.Fatalf("missing xdg: got out=%q stderr=%q err=%v, want empty", out, stderr, err)
		}

		out, stderr, err = runConfig(outsideRepo, []string{"HOME=" + home, "XDG_CONFIG_HOME=relative/xdg"}, "config")
		wantHome := "models:\n  aliases:\n    sol: from-home\n"
		if err != nil || out != wantHome || stderr != "" {
			t.Fatalf("relative xdg: got out=%q stderr=%q err=%v, want %q", out, stderr, err, wantHome)
		}
	})

	t.Run("invalid config returns error with no stdout", func(t *testing.T) {
		t.Parallel()
		xdg := t.TempDir()
		cfgPath := filepath.Join(xdg, "tasks", "config.yml")
		writeConfig(t, cfgPath, "models:\n  unknown: bad\n")

		out, stderr, err := runConfig(outsideRepo, []string{"XDG_CONFIG_HOME=" + xdg}, "config")
		if err == nil || ExitCode(err) != 1 || out != "" || stderr != "" {
			t.Fatalf("got out=%q stderr=%q err=%v (exit %d), want exit 1 error and no output", out, stderr, err, ExitCode(err))
		}
		if !strings.Contains(err.Error(), "invalid config") || !strings.Contains(err.Error(), cfgPath) || !strings.Contains(err.Error(), "line 2:") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unreadable config path returns error with no stdout", func(t *testing.T) {
		t.Parallel()
		xdg := t.TempDir()
		dirPath := filepath.Join(xdg, "tasks", "config.yml")
		if err := os.MkdirAll(dirPath, 0700); err != nil {
			t.Fatal(err)
		}

		out, stderr, err := runConfig(outsideRepo, []string{"XDG_CONFIG_HOME=" + xdg}, "config")
		if err == nil || ExitCode(err) != 1 || out != "" || stderr != "" || !strings.Contains(err.Error(), dirPath) {
			t.Fatalf("got out=%q stderr=%q err=%v, want read error referencing %s", out, stderr, err, dirPath)
		}
	})

	t.Run("rejects positional arguments and unknown options", func(t *testing.T) {
		t.Parallel()
		xdg := t.TempDir()
		for _, tc := range []struct {
			args []string
			want string
		}{
			{[]string{"config", "models"}, "`tasks config` expects 0 positional argument(s)"},
			{[]string{"config", "--json"}, "unrecognized argument: --json"},
		} {
			out, stderr, err := runConfig(outsideRepo, []string{"XDG_CONFIG_HOME=" + xdg}, tc.args...)
			if err == nil || ExitCode(err) != 2 || err.Error() != tc.want || out != "" || stderr != "" {
				t.Fatalf("%v: got out=%q stderr=%q err=%v (exit %d), want usage error %q", tc.args, out, stderr, err, ExitCode(err), tc.want)
			}
		}
	})

	t.Run("prints normalized terminal status and ignores env override", func(t *testing.T) {
		t.Parallel()
		xdg := t.TempDir()
		cfgPath := filepath.Join(xdg, "tasks", "config.yml")

		// Missing config still prints nothing even when TASKS_TERMINAL_STATUS is set.
		out, stderr, err := runConfig(outsideRepo, []string{"XDG_CONFIG_HOME=" + xdg, "TASKS_TERMINAL_STATUS=0"}, "config")
		if err != nil || out != "" || stderr != "" {
			t.Fatalf("missing config with env: got out=%q stderr=%q err=%v, want empty", out, stderr, err)
		}

		writeConfig(t, cfgPath, "terminal:\n  status: OFF\n")
		out, stderr, err = runConfig(outsideRepo, []string{"XDG_CONFIG_HOME=" + xdg, "TASKS_TERMINAL_STATUS=1"}, "config")
		wantFalse := "terminal:\n  status: false\n"
		if err != nil || out != wantFalse || stderr != "" {
			t.Fatalf("OFF config: got out=%q stderr=%q err=%v, want %q", out, stderr, err, wantFalse)
		}

		writeConfig(t, cfgPath, "terminal:\n  status: 1\nmodels:\n  aliases:\n    sol: openai-codex/gpt-6.2-sol\n")
		out, stderr, err = runConfig(outsideRepo, []string{"XDG_CONFIG_HOME=" + xdg, "TASKS_TERMINAL_STATUS=0"}, "config")
		wantBoth := "models:\n  aliases:\n    sol: openai-codex/gpt-6.2-sol\nterminal:\n  status: true\n"
		if err != nil || out != wantBoth || stderr != "" {
			t.Fatalf("combined config: got out=%q stderr=%q err=%v, want %q", out, stderr, err, wantBoth)
		}
	})

	t.Run("autocorrects typo outside repository", func(t *testing.T) {
		t.Parallel()
		xdg := t.TempDir()
		writeConfig(t, filepath.Join(xdg, "tasks", "config.yml"), "models:\n  aliases:\n    sol: openai-codex/gpt-6.2-sol\n")

		out, stderr, err := runConfig(outsideRepo, []string{"XDG_CONFIG_HOME=" + xdg}, "cofnig")
		wantOut := "models:\n  aliases:\n    sol: openai-codex/gpt-6.2-sol\n"
		wantErr := "warning: subcommand `cofnig` does not exist; using `config` instead.\n"
		if err != nil || out != wantOut || stderr != wantErr {
			t.Fatalf("got out=%q stderr=%q err=%v, want out=%q stderr=%q", out, stderr, err, wantOut, wantErr)
		}
	})
}
