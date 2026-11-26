package signing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/process"
)

type runnerFunc func(context.Context, process.Command) process.Result

func (f runnerFunc) Run(ctx context.Context, c process.Command) process.Result { return f(ctx, c) }
func policy(format string) Policy {
	return Policy{Sign: true, Format: format, Programs: map[string]string{"openpgp": "gpg fixture with spaces", "x509": "gpgsm fixture", "ssh": "ssh fixture"}, MinimumTrust: 2}
}
func armor(format string) string {
	name := map[string]string{"openpgp": "PGP SIGNATURE", "x509": "SIGNED MESSAGE", "ssh": "SSH SIGNATURE"}[format]
	return "-----BEGIN " + name + "-----\nc2ln\n-----END " + name + "-----\n"
}

const payload = "tree 0000000000000000000000000000000000000000\ncommitter C <c@example.invalid> 1700000000 +0000\n\nmessage\n"

func TestSigningProtocolsAndFailureContracts(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"openpgp", "x509", "ssh"} {
		for _, failure := range []string{"", "exit", "missing", "signal", "cancel", "empty", "malformed", "status"} {
			t.Run(format+"/"+failure, func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				p := policy(format)
				p.HasKey = true
				p.Key = "key with spaces"
				called := 0
				var temp string
				a := Adapter{Directory: t.TempDir(), Environment: []string{"KEEP=exact"}, Runner: runnerFunc(func(ctx context.Context, c process.Command) process.Result {
					called++
					if c.Directory == "" || !reflect.DeepEqual(c.Environment, []string{"KEEP=exact"}) || !c.Group || c.Args[0] != p.Programs[format] {
						t.Fatalf("command: %+v", c)
					}
					if failure == "exit" {
						return process.Result{Code: 7}
					}
					if failure == "missing" {
						return process.Result{Code: 127, Err: os.ErrNotExist}
					}
					if failure == "signal" {
						return process.Result{Code: -15}
					}
					if failure == "cancel" {
						cancel()
					}
					result := armor(format)
					if failure == "empty" {
						result = ""
					}
					if failure == "malformed" {
						result = "not armor"
					}
					if format == "ssh" {
						temp = c.Args[len(c.Args)-1]
						data, err := os.ReadFile(temp)
						if err != nil || string(data) != payload {
							t.Fatalf("payload: %q %v", data, err)
						}
						if !reflect.DeepEqual(c.Args[1:7], []string{"-Y", "sign", "-n", "git", "-f", p.Key}) {
							t.Fatal(c.Args)
						}
						if err := os.WriteFile(temp+".sig", []byte(result), 0600); err != nil {
							t.Fatal(err)
						}
					} else {
						data, err := io.ReadAll(c.In)
						if err != nil || string(data) != payload {
							t.Fatalf("stdin: %q %v", data, err)
						}
						if !reflect.DeepEqual(c.Args[1:], []string{"--status-fd=2", "-bsau", p.Key}) {
							t.Fatal(c.Args)
						}
						fmt.Fprint(c.Out, result)
						if failure != "status" {
							fmt.Fprintln(c.Err, "[GNUPG:] SIG_CREATED D 1 10 00 1 FINGERPRINT")
						}
					}
					return process.Result{}
				})}
				signature, err := a.Sign(ctx, p, []byte(payload), git.Signature{})
				wantError := failure != "" && !(failure == "status" && format == "ssh")
				if (err != nil) != wantError || called != 1 {
					t.Fatalf("signature=%q err=%v calls=%d", signature, err, called)
				}
				if temp != "" {
					if _, err := os.Stat(filepath.Dir(temp)); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("temporary not removed: %v", err)
					}
				}
			})
		}
	}
}

func TestSigningKeyFallbackAndSSHDefaultCommand(t *testing.T) {
	t.Parallel()
	p := policy("openpgp")
	a := Adapter{Runner: runnerFunc(func(_ context.Context, c process.Command) process.Result {
		if c.Args[3] != "Committer <c@example.invalid>" {
			t.Fatal(c.Args)
		}
		fmt.Fprint(c.Out, armor("openpgp"))
		fmt.Fprintln(c.Err, "[GNUPG:] SIG_CREATED D")
		return process.Result{}
	})}
	if _, err := a.Sign(context.Background(), p, []byte(payload), git.Signature{Name: "Committer", Email: "c@example.invalid"}); err != nil {
		t.Fatal(err)
	}
	p = policy("ssh")
	p.DefaultKeyCommand = "'key program' \"a\\qb\" '$HOME;echo'"
	count := 0
	a.Runner = runnerFunc(func(_ context.Context, c process.Command) process.Result {
		count++
		if count == 1 {
			if !reflect.DeepEqual(c.Args, []string{"key program", "aqb", "$HOME;echo"}) {
				t.Fatal(c.Args)
			}
			fmt.Fprintln(c.Out, "key::ssh-ed25519 AAAA fixture")
			fmt.Fprintln(c.Out, "ignored")
			return process.Result{}
		}
		if c.Args[len(c.Args)-2] != "-U" {
			t.Fatal(c.Args)
		}
		key, err := os.ReadFile(c.Args[6])
		if err != nil || string(key) != "ssh-ed25519 AAAA fixture" {
			t.Fatalf("%q %v", key, err)
		}
		if err := os.WriteFile(c.Args[len(c.Args)-1]+".sig", []byte(armor("ssh")), 0600); err != nil {
			t.Fatal(err)
		}
		return process.Result{}
	})
	if _, err := a.Sign(context.Background(), p, []byte(payload), git.Signature{}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "'bad", "trailing\\", " leading"} {
		if _, err := splitCommand(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestMergeVerificationGPGAndX509TrustStatuses(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"openpgp", "x509"} {
		for _, status := range []string{"GOODSIG", "BADSIG", "ERRSIG", "EXPSIG", "EXPKEYSIG", "REVKEYSIG", "absent", "duplicate"} {
			for _, level := range trustNames {
				t.Run(format+"/"+status+"/"+level, func(t *testing.T) {
					p := policy(format)
					a := Adapter{Runner: runnerFunc(func(_ context.Context, c process.Command) process.Result {
						if c.Args[0] != p.Programs[format] {
							t.Fatal(c.Args)
						}
						data, err := io.ReadAll(c.In)
						if err != nil || string(data) != payload {
							t.Fatalf("payload %q %v", data, err)
						}
						if status != "absent" {
							fmt.Fprintf(c.Out, "[GNUPG:] %s KEY User\n", status)
						}
						if status == "duplicate" {
							fmt.Fprintln(c.Out, "[GNUPG:] GOODSIG KEY User\n[GNUPG:] GOODSIG KEY User")
						}
						fmt.Fprintf(c.Out, "[GNUPG:] TRUST_%s\n", strings.ToUpper(level))
						return process.Result{}
					})}
					err := a.VerifyMerge(context.Background(), p, []byte(payload), []byte(armor(format)))
					rank, _ := trust(level)
					if (err == nil) != (status == "GOODSIG" && rank >= p.MinimumTrust) {
						t.Fatalf("err=%v", err)
					}
					p.MinimumTrust = 0
					err = a.VerifyMerge(context.Background(), p, []byte(payload), []byte(armor(format)))
					if (err == nil) != (status == "GOODSIG") {
						t.Fatalf("configured minimum: %v", err)
					}
				})
			}
		}
	}
}

func TestSSHVerificationPrincipalsTimeRevocationAndTrust(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"good", "bad", "unknown", "revoked", "expired", "ultimate", "missing-revocation", "malformed", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			p := policy("ssh")
			p.AllowedSigners = "allowed signers"
			p.Revocation = "revoked"
			root := t.TempDir()
			if mode != "missing-revocation" {
				if err := os.WriteFile(filepath.Join(root, p.Revocation), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "ultimate" {
				p.MinimumTrust = 4
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			count := 0
			var diagnostic strings.Builder
			a := Adapter{Directory: root, Diagnostic: &diagnostic, Runner: runnerFunc(func(_ context.Context, c process.Command) process.Result {
				count++
				if !strings.Contains(strings.Join(c.Args, " "), "-Overify-time=") {
					t.Fatal(c.Args)
				}
				if c.Args[2] == "find-principals" {
					if mode == "cancel" {
						cancel()
					}
					if mode != "unknown" && mode != "expired" {
						fmt.Fprintln(c.Out, "first\r\nsecond")
					}
					return process.Result{}
				}
				if c.Args[2] == "check-novalidate" {
					return process.Result{}
				}
				revoked := strings.Contains(strings.Join(c.Args, " "), " -r revoked")
				if revoked != (mode != "missing-revocation") {
					t.Fatal(c.Args)
				}
				if mode == "bad" || mode == "revoked" || count == 2 {
					return process.Result{Code: 1}
				}
				if mode == "malformed" {
					fmt.Fprintln(c.Out, "Good but not a signature")
				} else {
					fmt.Fprintln(c.Out, "Good \"git\" signature for second with ED25519 key SHA256:fixture")
				}
				return process.Result{}
			})}
			err := a.VerifyMerge(ctx, p, []byte(payload), []byte(armor("ssh")))
			if (err == nil) != (mode == "good" || mode == "missing-revocation") {
				t.Fatalf("error=%v", err)
			}
			if mode == "missing-revocation" && !strings.Contains(diagnostic.String(), "not found") {
				t.Fatal("missing warning")
			}
		})
	}
}

func loadPolicy(repo *git.Repository) (Policy, error) {
	entries, err := repo.ConfigurationEntries()
	if err != nil {
		return Policy{}, err
	}
	return ParsePolicy(entries)
}

func TestSigningPolicyFileFreshnessAndFrozenCommandIncludes(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "local.config")
	command := filepath.Join(root, "command.config")
	if err := os.WriteFile(local, []byte("[gpg]\nprogram = original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("[user]\nsigningKey = frozen\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "include.path")
	t.Setenv("GIT_CONFIG_VALUE_0", command)
	repo, err := git.Init(filepath.Join(root, "repo"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.SetLocalConfig("commit.gpgsign", "true"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetLocalConfig("include.path", local); err != nil {
		t.Fatal(err)
	}
	p, err := loadPolicy(repo)
	if err != nil || p.Key != "frozen" || p.Programs["openpgp"] != "original" {
		t.Fatalf("%+v %v", p, err)
	}
	if err := os.WriteFile(local, []byte("[gpg]\nprogram = updated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("[user]\nsigningKey = changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err = loadPolicy(repo)
	if err != nil || p.Key != "frozen" || p.Programs["openpgp"] != "updated" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestSigningPolicySnapshotBooleanAssignments(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", " = true", " = false", " = 0", " = -2", " ="} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			repo, err := git.Init(filepath.Join(t.TempDir(), "repo"), "", false)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			include := filepath.Join(t.TempDir(), "policy.config")
			if err := os.WriteFile(include, []byte("[commit]\ngpgSign"+value+"\n[merge]\nverifySignatures = false\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := repo.SetLocalConfig("include.path", include); err != nil {
				t.Fatal(err)
			}
			p, err := loadPolicy(repo)
			want := value == "" || value == " = true" || value == " = -2"
			if err != nil || p.Sign != want || p.Verify {
				t.Fatalf("policy=%+v err=%v", p, err)
			}
			// Like Git's merge callback, reject even an overridden invalid entry.
			if err := os.WriteFile(include, []byte("[commit]\ngpgSign = invalid\ngpgSign = false\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadPolicy(repo); err == nil {
				t.Fatal("accepted shadowed invalid boolean")
			}
		})
	}
}

func TestSigningPolicyOrderedAliasesAndInvalidSettings(t *testing.T) {
	t.Parallel()
	repo, err := git.Init(filepath.Join(t.TempDir(), "repo"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	for _, entry := range [][2]string{{"commit.gpgsign", "true"}, {"gpg.program", "legacy"}, {"gpg.openpgp.program", "modern"}, {"gpg.format", "x509"}, {"gpg.x509.program", "x509 program"}, {"gpg.mintrustlevel", "ULTIMATE"}, {"user.signingkey", "identity"}} {
		if err := repo.SetLocalConfig(entry[0], entry[1]); err != nil {
			t.Fatal(err)
		}
	}
	p, err := loadPolicy(repo)
	if err != nil || p.Programs["openpgp"] != "modern" || p.Format != "x509" || p.MinimumTrust != 4 || p.Key != "identity" {
		t.Fatalf("%+v %v", p, err)
	}
	// An included later legacy alias must override the earlier format alias.
	include := filepath.Join(t.TempDir(), "later.config")
	if err := os.WriteFile(include, []byte("[gpg]\nprogram = later\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetLocalConfig("include.path", include); err != nil {
		t.Fatal(err)
	}
	p, err = loadPolicy(repo)
	if err != nil || p.Programs["openpgp"] != "later" {
		t.Fatalf("%+v %v", p, err)
	}
	for _, entry := range [][2]string{{"commit.gpgsign", "invalid"}, {"merge.verifysignatures", "invalid"}, {"gpg.format", "unknown"}, {"gpg.mintrustlevel", "unknown"}} {
		original, e := repo.Config(entry[0])
		if git.HasCode(e, git.NotFound) {
			original = "false"
		} else if e != nil {
			t.Fatal(e)
		}
		if err := repo.SetLocalConfig(entry[0], entry[1]); err != nil {
			t.Fatal(err)
		}
		if _, err := loadPolicy(repo); err == nil {
			t.Fatalf("accepted %v", entry)
		}
		if err := repo.SetLocalConfig(entry[0], original); err != nil {
			t.Fatal(err)
		}
	}
}
