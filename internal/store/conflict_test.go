package store

import (
	"strings"
	"testing"
)

func TestStatusConflictDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name, content string
		conflict      bool
	}{
		{"status", "<<<<<<< HEAD\nStatus: open\n=======\nStatus: blocked\n>>>>>>> incoming\n", true},
		{"body", "Status: open\n<<<<<<< HEAD\nleft\n=======\nright\n>>>>>>> incoming\n", true},
		{"diff3", "Status: open\n<<<<<<< HEAD\nleft\n||||||| base\nbase\n=======\nright\n>>>>>>> incoming", true},
		{"custom-width-crlf", "Status: open\n<<<<<<<<<< ours\r\nleft\r\n==========\r\nright\r\n>>>>>>>>>> theirs\r\n", true},
		{"bare-markers", "Status: open\n<<<<<<<\nleft\n=======\nright\n>>>>>>>\n", true},
		{"fenced-example", "Status: open\n```diff\n<<<<<<< HEAD\nleft\n=======\nright\n>>>>>>> incoming\n```\n", false},
		{"tilde-example", "Status: open\n   ~~~~text\n<<<<<<< HEAD\nleft\n=======\nright\n>>>>>>> incoming\n   ~~~~\n", false},
		{"after-example", "Status: open\n```\nexample\n```\n<<<<<<< HEAD\nleft\n=======\nright\n>>>>>>> incoming\n", true},
		{"body-opens-fence", "Status: open\n<<<<<<< HEAD\n```\nleft\n=======\nright\n>>>>>>> incoming\n", true},
		{"quoted-example", "Status: open\n> <<<<<<< HEAD\n> left\n> =======\n> right\n> >>>>>>> incoming\n", false},
		{"prose", "Status: open\nUse <<<<<<< and >>>>>>> in examples.\n=======\n", false},
		{"heading", "Status: open\nHeading\n=======\n", false},
		{"mismatched-width", "Status: open\n<<<<<<< HEAD\nleft\n========\nright\n>>>>>>> incoming\n", false},
		{"incomplete", "Status: open\n<<<<<<< HEAD\nleft\n=======\nright\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, err := Status(test.content)
			if test.conflict {
				if err == nil || status != "" || !strings.Contains(err.Error(), "unresolved merge conflict") {
					t.Fatalf("%q %v", status, err)
				}
				if _, err := WithStatus(test.content, "active"); err == nil {
					t.Fatal("mutation accepted conflicted metadata")
				}
			} else if err != nil || status != "open" {
				t.Fatalf("literal text misclassified: %q %v", status, err)
			}
		})
	}
	for _, content := range []string{"Status: invalid\n", "Status: open\nStatus: blocked\n", "<<<<<<< HEAD\nStatus: invalid\n"} {
		_, err := Status(content)
		if err == nil || strings.Contains(err.Error(), "merge conflict") || !strings.Contains(err.Error(), "expected exactly one Status:") {
			t.Fatalf("ordinary parse error misdiagnosed: %v", err)
		}
	}
}
