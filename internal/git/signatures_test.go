package git

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func clearSignatureEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSignaturesEnvironmentOnlyAndIndependentDates(t *testing.T) {
	clearSignatureEnvironment(t)
	repo, root := fixture(t, false)
	for key, value := range map[string]string{
		"GIT_AUTHOR_NAME": "Environment Author", "GIT_AUTHOR_EMAIL": "author@example.invalid",
		"GIT_AUTHOR_DATE":    "1700000000 +0230",
		"GIT_COMMITTER_NAME": "Environment Committer", "GIT_COMMITTER_EMAIL": "committer@example.invalid",
		"GIT_COMMITTER_DATE": "1700000123 -0430",
	} {
		t.Setenv(key, value)
	}
	before := mergePreflightSnapshot(t, root)
	actors, err := repo.Signatures()
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		got         Signature
		name, email string
		seconds     int64
		offset      int
	}{
		{actors.Author, "Environment Author", "author@example.invalid", 1700000000, 9000},
		{actors.Committer, "Environment Committer", "committer@example.invalid", 1700000123, -16200},
	} {
		_, offset := check.got.When.Zone()
		if check.got.Name != check.name || check.got.Email != check.email || check.got.When.Unix() != check.seconds || offset != check.offset {
			t.Fatalf("unexpected signature: %+v", check.got)
		}
	}
	if !reflect.DeepEqual(before, mergePreflightSnapshot(t, root)) {
		t.Fatal("resolving signatures mutated repository")
	}
}

func TestSignaturesConfigFallbackAndCommonTimestamp(t *testing.T) {
	clearSignatureEnvironment(t)
	repo, _ := fixture(t, false)
	for key, value := range map[string]string{"user.name": "Config Name", "user.email": "config@example.invalid"} {
		if err := repo.SetLocalConfig(key, value); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GIT_AUTHOR_NAME", "Author Override")
	start := time.Now().Unix()
	actors, err := repo.Signatures()
	if err != nil {
		t.Fatal(err)
	}
	if actors.Author.Name != "Author Override" || actors.Committer.Name != "Config Name" || actors.Author.Email != "config@example.invalid" || actors.Committer.Email != actors.Author.Email {
		t.Fatalf("fallback/override: %+v", actors)
	}
	if !actors.Author.When.Equal(actors.Committer.When) || actors.Author.When.Unix() < start || actors.Author.When.Unix() > time.Now().Unix() {
		t.Fatalf("common current timestamp: %+v", actors)
	}
}

func TestSignaturesInvalidDateIsReadOnlyAndCommitterDoesNotRequireAuthor(t *testing.T) {
	clearSignatureEnvironment(t)
	repo, root := fixture(t, false)
	t.Setenv("GIT_AUTHOR_NAME", "Author")
	t.Setenv("GIT_AUTHOR_EMAIL", "author@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Committer")
	t.Setenv("GIT_COMMITTER_EMAIL", "committer@example.invalid")
	for _, key := range []string{"GIT_AUTHOR_DATE", "GIT_COMMITTER_DATE"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "not-a-date")
			before := mergePreflightSnapshot(t, root)
			if _, err := repo.Signatures(); err == nil {
				t.Fatal("accepted invalid date")
			}
			if !reflect.DeepEqual(before, mergePreflightSnapshot(t, root)) {
				t.Fatal("invalid date mutated repository")
			}
			if key == "GIT_AUTHOR_DATE" {
				who, err := repo.CommitterSignature()
				if err != nil || who.Name != "Committer" {
					t.Fatalf("reflog required author: %+v %v", who, err)
				}
			}
		})
	}
}

func TestSignaturesNativeDateFormats(t *testing.T) {
	for _, date := range []string{"1700000000 +0230", "2023-11-15T00:43:20+02:30", "Tue, 14 Nov 2023 22:13:20 +0000"} {
		t.Run(strings.ReplaceAll(date, "/", "_"), func(t *testing.T) {
			clearSignatureEnvironment(t)
			repo, _ := fixture(t, false)
			t.Setenv("GIT_AUTHOR_NAME", "Author")
			t.Setenv("GIT_AUTHOR_EMAIL", "author@example.invalid")
			t.Setenv("GIT_COMMITTER_NAME", "Committer")
			t.Setenv("GIT_COMMITTER_EMAIL", "committer@example.invalid")
			t.Setenv("GIT_AUTHOR_DATE", date)
			t.Setenv("GIT_COMMITTER_DATE", date)
			actors, err := repo.Signatures()
			if err != nil {
				t.Fatal(err)
			}
			if actors.Author.When.Unix() != 1700000000 || !actors.Author.When.Equal(actors.Committer.When) {
				t.Fatalf("date %q: %+v", date, actors)
			}
		})
	}
}
