package signing

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/git"
)

// Only this explicitly opted-in fixture executes Git. Protocol unit tests do not
// constitute cryptographic proof. Neither fixture uses a personal key or agent.
type cryptoFixture struct {
	t                     *testing.T
	root, home, directory string
	env                   []string
	repo                  *git.Repository
	base                  git.ID
	who                   git.Signature
	agentDenials          map[string]*int
}

func newCryptoFixture(t *testing.T, agent bool) *cryptoFixture {
	t.Helper()
	if os.Getenv("TASK_CRYPTO_ORACLE") != "1" {
		t.Skip("set TASK_CRYPTO_ORACLE=1 for disposable-key crypto/Git merge oracles")
	}
	root, err := os.MkdirTemp("/tmp", "task-crypto-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	f := &cryptoFixture{t: t, root: root, home: filepath.Join(root, "gnupg")}
	if err := os.Mkdir(f.home, 0700); err != nil {
		t.Fatal(err)
	}
	// Isolate cryptographic keys and agents in addition to the suite's home.
	// Leave system Git configuration and discovery limits intact.
	f.env = append(os.Environ(), "GNUPGHOME="+f.home,
		"SSH_AUTH_SOCK=", "SSH_AGENT_PID=", "GPG_AGENT_INFO=")
	f.write(filepath.Join(f.home, "gpg.conf"), "batch\nno-autostart\nno-auto-key-retrieve\n")
	f.write(filepath.Join(f.home, "gpgsm.conf"), "batch\nno-autostart\ndisable-crl-checks\ndisable-dirmngr\n")
	if agent {
		f.startAgent()
	}
	f.repo, err = git.Init(filepath.Join(root, "repository"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.repo.Close)
	paths, err := f.repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	f.directory = paths.WorkDir
	for key, value := range map[string]string{"commit.gpgsign": "false", "core.hookspath": "/dev/null", "user.name": "Fixture", "user.email": "fixture@example.invalid"} {
		if err := f.repo.SetLocalConfig(key, value); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := f.repo.WriteTree(nil)
	if err != nil {
		t.Fatal(err)
	}
	f.who = git.Signature{Name: "Fixture", Email: "fixture@example.invalid", When: time.Now()}
	f.base, err = f.repo.WriteCommit(tree, nil, f.who, f.who, "base\n")
	if err != nil {
		t.Fatal(err)
	}
	f.run("git", "-C", f.directory, "checkout", "--detach", f.base.String())
	return f
}

func (f *cryptoFixture) write(path, content string) {
	f.t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		f.t.Fatal(err)
	}
}
func (f *cryptoFixture) command(input []byte, name string, args ...string) (string, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := boundedCryptoCommand(ctx, name, args...)
	cmd.Env = f.env
	cmd.Dir = f.root
	cmd.Stdin = bytes.NewReader(input)
	data, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		f.t.Fatalf("crypto oracle command timed out: %s %v: %v\n%s", name, args, ctx.Err(), data)
	}
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() < 0) {
		f.t.Fatalf("crypto oracle infrastructure failure: %s %v: %v\n%s", name, args, err, data)
	}
	return string(data), err
}
func (f *cryptoFixture) run(name string, args ...string) string {
	f.t.Helper()
	out, err := f.command(nil, name, args...)
	if err != nil {
		f.t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return out
}

// A deadline or signal is an infrastructure failure, never an expected bad
// signature. Kill the entire oracle command group, including Git's verifier.
func boundedCryptoCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

// GnuPG's leave_cmd logs rejected MARKTRUSTED requests with log_error, and
// agent_exit returns 2 when that error count is nonzero. Admit only the exact
// planned denials, never an arbitrary nonzero exit or additional diagnostic.
func validateAgentShutdown(code int, output string, expectedDenials int, home string, pid int) error {
	wantCode := 0
	if expectedDenials > 0 {
		wantCode = 2
	}
	if code != wantCode {
		return fmt.Errorf("exit status %d, want %d", code, wantCode)
	}
	const denial = "command 'MARKTRUSTED' failed: Not supported"
	if expectedDenials == 0 {
		if strings.Contains(output, denial) {
			return fmt.Errorf("unexpected trust denial")
		}
		return nil
	}
	denials := 0
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimPrefix(line, fmt.Sprintf("gpg-agent[%d]: ", pid))
		switch {
		case line == denial:
			denials++
		case line == "", line == `WARNING: "--supervised" is a deprecated option`,
			line == "directory '"+filepath.Join(home, "private-keys-v1.d")+"' created",
			line == "using fd 3 for std socket ("+filepath.Join(home, "S.gpg-agent")+")",
			line == "listening on: std=3 extra=-1 browser=-1 ssh=-1",
			line == "SIGHUP received - re-reading configuration and flushing cache",
			line == "reading options from '"+filepath.Join(home, "gpg-agent.conf")+"'":
		case strings.HasPrefix(line, "gpg-agent (GnuPG) ") && strings.HasSuffix(line, " starting in supervised mode."):
		default:
			return fmt.Errorf("unexpected agent diagnostic: %q", line)
		}
	}
	if denials != expectedDenials {
		return fmt.Errorf("%d trust denials, want %d", denials, expectedDenials)
	}
	return nil
}

func TestCryptoAgentShutdownClassification(t *testing.T) {
	const denial = "command 'MARKTRUSTED' failed: Not supported\n"
	for _, tc := range []struct {
		name   string
		code   int
		output string
		denied int
		ok     bool
	}{
		{"clean", 0, "", 0, true},
		{"planned denials", 2, strings.Repeat(denial, 12), 12, true},
		{"unplanned nonzero", 2, denial, 0, false},
		{"missing denials", 2, strings.Repeat(denial, 11), 12, false},
		{"extra denial", 2, strings.Repeat(denial, 13), 12, false},
		{"additional error", 2, strings.Repeat(denial, 12) + "error opening trustlist\n", 12, false},
		{"signal", -1, strings.Repeat(denial, 12), 12, false},
		{"other status", 1, strings.Repeat(denial, 12), 12, false},
		{"missing error status", 0, strings.Repeat(denial, 12), 12, false},
		{"unplanned denial", 0, denial, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAgentShutdown(tc.code, tc.output, tc.denied, "/tmp/fixture", 123)
			if (err == nil) != tc.ok {
				t.Fatalf("classification = %v, want success %v", err, tc.ok)
			}
		})
	}
}

func (f *cryptoFixture) startAgent() {
	f.t.Helper()
	home, env := f.home, append([]string{}, f.env...)
	if f.agentDenials == nil {
		f.agentDenials = make(map[string]*int)
	}
	expectedDenials := new(int)
	f.agentDenials[home] = expectedDenials
	// A missing local trust list falls back to the system file. Even when
	// GnuPG treats a missing system list as empty, its logged error makes the
	// agent exit nonzero later. Establish this fixture's empty trust store now.
	trustList, err := os.OpenFile(filepath.Join(home, "trustlist.txt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := trustList.Close(); err != nil {
		f.t.Fatal(err)
	}
	// Agent reload rereads these options. Command-line-only flags did not keep
	// scdaemon or interactive root-trust prompts disabled after SIGHUP.
	pinentry := filepath.Join(home, "refuse-pinentry")
	f.write(pinentry, "#!/bin/sh\nprintf unexpected > \"$0.attempted\"\nexit 1\n")
	if err := os.Chmod(pinentry, 0700); err != nil {
		f.t.Fatal(err)
	}
	f.write(filepath.Join(home, "gpg-agent.conf"), "disable-scdaemon\nno-allow-mark-trusted\npinentry-program "+pinentry+"\n")
	// --daemon forks even with --no-detach on the tested GnuPG. Public socket
	// activation instead gives us a directly waitable, positively identified PID.
	options := f.run("gpg-agent", "--dump-options")
	mode := "--supervised"
	if strings.Contains("\n"+options, "\n--deprecated-supervised\n") {
		mode = "--deprecated-supervised"
	}
	program, err := exec.LookPath("gpg-agent")
	if err != nil {
		f.t.Fatal(err)
	}
	socket, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(home, "S.gpg-agent"), Net: "unix"})
	if err != nil {
		f.t.Fatal(err)
	}
	socket.SetUnlinkOnClose(false)
	defer socket.Close()
	fd, err := socket.File()
	if err != nil {
		f.t.Fatal(err)
	}
	defer fd.Close()
	logPath := filepath.Join(home, "agent.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = log.Close() })
	// exec keeps the shell PID, so LISTEN_PID names the agent, not its parent.
	agent := exec.Command("/bin/sh", "-c", "trap '' PIPE; LISTEN_PID=$$; export LISTEN_PID; exec \"$@\"", "task-crypto-agent", program, "--homedir", home, mode)
	agent.Env = append(append([]string{}, env...), "LISTEN_FDS=1", "LISTEN_FDNAMES=std")
	agent.ExtraFiles = []*os.File{fd}
	agent.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	agent.Stdout, agent.Stderr = log, log
	if err := agent.Start(); err != nil {
		f.t.Fatal(err)
	}
	done := make(chan struct{})
	var agentErr error
	go func() { agentErr = agent.Wait(); close(done) }()
	f.t.Cleanup(func() {
		select {
		case <-done:
			f.t.Errorf("owned agent exited before cleanup: %v", agentErr)
		default:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := boundedCryptoCommand(ctx, "gpgconf", "--homedir", home, "--kill", "gpg-agent")
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				f.t.Errorf("owned agent shutdown: %v %s", err, out)
			}
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-agent.Process.Pid, syscall.SIGKILL)
			<-done
			f.t.Error("owned agent required forced termination")
		}
		data, readErr := os.ReadFile(logPath)
		code := -1
		if agent.ProcessState != nil {
			code = agent.ProcessState.ExitCode()
		}
		if readErr != nil {
			f.t.Errorf("owned agent log: %v", readErr)
		}
		if err := validateAgentShutdown(code, string(data), *expectedDenials, home, agent.Process.Pid); err != nil {
			f.t.Errorf("owned agent exit (%v): %v", agentErr, err)
		}
		if agentErr != nil && code == 0 {
			f.t.Errorf("owned agent wait: %v", agentErr)
		}
		if f.t.Failed() {
			f.t.Logf("owned fixture agent log:\n%s", data)
		}
		if err := syscall.Kill(-agent.Process.Pid, 0); err == nil {
			_ = syscall.Kill(-agent.Process.Pid, syscall.SIGKILL)
			f.t.Error("owned agent left child processes")
		}
		if _, err := os.Stat(pinentry + ".attempted"); !os.IsNotExist(err) {
			f.t.Errorf("fixture attempted interactive pinentry: %v", err)
		}
	})
	connection, err := net.DialTimeout("unix", filepath.Join(home, "S.gpg-agent"), 5*time.Second)
	if err != nil {
		f.t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		f.t.Fatal(err)
	}
	reader := bufio.NewReader(connection)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "OK ") {
		data, _ := os.ReadFile(logPath)
		f.t.Fatalf("owned agent greeting %q: %v\n%s", line, err, data)
	}
	if _, err := fmt.Fprint(connection, "GETINFO pid\n"); err != nil {
		f.t.Fatal(err)
	}
	line, err = reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "D "+strconv.Itoa(agent.Process.Pid) {
		f.t.Fatalf("agent is not the directly owned process: %q %v", line, err)
	}
	line, err = reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "OK") {
		f.t.Fatalf("owned agent PID query: %q %v", line, err)
	}
	if _, err := fmt.Fprint(connection, "BYE\n"); err != nil {
		f.t.Fatal(err)
	}
	// Drain the closing reply before closing our socket. Supervised mode does
	// not install the daemon branch's SIGPIPE ignore, so reproduce that normal
	// daemon disposition in the exec wrapper as well.
	line, err = reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "OK") {
		f.t.Fatalf("owned agent closing reply: %q %v", line, err)
	}
}
func fingerprint(listing string) string {
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) > 9 && fields[0] == "fpr" {
			return fields[9]
		}
	}
	return ""
}
func (f *cryptoFixture) policy(format string) Policy {
	return Policy{Sign: true, Verify: true, Format: format, Programs: map[string]string{"openpgp": "gpg", "x509": "gpgsm", "ssh": "ssh-keygen"}, HasKey: true, MinimumTrust: 2}
}
func (f *cryptoFixture) adapter() Adapter { return Adapter{Directory: f.directory, Environment: f.env} }
func (f *cryptoFixture) payload() []byte {
	f.t.Helper()
	tree, err := f.repo.WriteTree(nil)
	if err != nil {
		f.t.Fatal(err)
	}
	data, err := f.repo.CommitBuffer(tree, []git.ID{f.base}, f.who, f.who, "signed incoming\n")
	if err != nil {
		f.t.Fatal(err)
	}
	return data
}
func (f *cryptoFixture) sign(p Policy, data []byte) []byte {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sig, err := f.adapter().Sign(ctx, p, data, f.who)
	if err != nil {
		f.t.Fatal(err)
	}
	return sig
}

// Assert both real verification commands, not verify-commit alone: Git accepts
// EXPKEYSIG ('Y') there, but merge accepts only GOODSIG ('G'). Always use --no-ff
// and --no-commit so successful comparisons do not sign or advance HEAD.
func (f *cryptoFixture) compare(p Policy, data, sig []byte, wantMerge, wantVerify bool) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := f.adapter().VerifyMerge(ctx, p, data, sig)
	if ctx.Err() != nil {
		f.t.Fatalf("crypto verification timed out, not an expected signature rejection: %v", err)
	}
	if (err == nil) != wantMerge {
		f.t.Fatalf("adapter merge accepted=%v want=%v: %v", err == nil, wantMerge, err)
	}
	id, err := f.repo.WriteSignedCommit(data, sig)
	if err != nil {
		f.t.Fatal(err)
	}
	args := []string{"-C", f.directory, "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "gpg.minTrustLevel=" + trustNames[p.MinimumTrust]}
	for _, format := range []string{"openpgp", "x509", "ssh"} {
		args = append(args, "-c", "gpg."+format+".program="+p.Programs[format])
	}
	if p.AllowedSigners != "" {
		args = append(args, "-c", "gpg.ssh.allowedSignersFile="+p.AllowedSigners)
	}
	if p.Revocation != "" {
		args = append(args, "-c", "gpg.ssh.revocationFile="+p.Revocation)
	}
	out, err := f.command(nil, "git", append(append([]string{}, args...), "verify-commit", id.String())...)
	if (err == nil) != wantVerify {
		f.t.Fatalf("Git verify-commit accepted=%v want=%v: %v\n%s", err == nil, wantVerify, err, out)
	}
	out, err = f.command(nil, "git", append(append([]string{}, args...), "merge", "--verify-signatures", "--no-ff", "--no-commit", id.String())...)
	if (err == nil) != wantMerge {
		f.t.Fatalf("Git merge accepted=%v want=%v: %v\n%s", err == nil, wantMerge, err, out)
	}
	f.run("git", "-C", f.directory, "-c", "core.hooksPath=/dev/null", "reset", "--hard", f.base.String())
}

func TestRealCryptoGitParity(t *testing.T) {
	for _, format := range []string{"openpgp", "ssh", "x509"} {
		t.Run(format, func(t *testing.T) {
			f := newCryptoFixture(t, format != "ssh")
			p := f.policy(format)
			switch format {
			case "openpgp":
				f.run("gpg", "--batch", "--pinentry-mode", "loopback", "--passphrase", "", "--quick-generate-key", "Fixture <fixture@example.invalid>", "ed25519", "sign", "0")
				p.Key = fingerprint(f.run("gpg", "--batch", "--with-colons", "--list-secret-keys"))
			case "ssh":
				p.Key = filepath.Join(f.root, "ssh-key")
				f.run("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", p.Key)
				public, err := os.ReadFile(p.Key + ".pub")
				if err != nil {
					t.Fatal(err)
				}
				p.AllowedSigners = filepath.Join(f.root, "allowed")
				f.write(p.AllowedSigners, "fixture@example.invalid "+string(public))
			case "x509":
				p.Key = f.x509Key()
			}
			if p.Key == "" {
				t.Fatal("missing fixture key")
			}
			data := f.payload()
			sig := f.sign(p, data)
			f.compare(p, data, sig, true, true)
			bad := append([]byte{}, data...)
			bad[len(bad)-2] ^= 1
			f.compare(p, bad, sig, false, false)
			if format == "ssh" {
				public, err := os.ReadFile(p.Key + ".pub")
				if err != nil {
					t.Fatal(err)
				}
				p.Revocation = filepath.Join(f.root, "revoked")
				f.write(p.Revocation, string(public))
				f.compare(p, data, sig, false, false)
			}
			if format == "openpgp" {
				// GnuPG automatically creates a revocation certificate for this fixture key.
				rev, err := os.ReadFile(filepath.Join(f.home, "openpgp-revocs.d", p.Key+".rev"))
				if err != nil {
					t.Fatal(err)
				}
				marker := bytes.Index(rev, []byte(":-----BEGIN PGP PUBLIC KEY BLOCK-----"))
				if marker < 0 {
					t.Fatal("missing fixture revocation certificate")
				}
				out, err := f.command(rev[marker+1:], "gpg", "--batch", "--import")
				if err != nil {
					t.Fatalf("import revocation: %v %s", err, out)
				}
				path := filepath.Join(f.root, "revoked.sig")
				f.write(path, string(sig))
				status, _ := f.command(data, "gpg", "--status-fd=1", "--verify", path, "-")
				if !strings.Contains(status, "[GNUPG:] REVKEYSIG ") {
					t.Fatalf("missing real revocation status: %s", status)
				}
				f.compare(p, data, sig, false, false)
			}
			if format == "x509" {
				f.x509TrustMatrix(p, data, sig)
			}
		})
	}
}

func TestRealCryptoOpenPGPExpirationGitMergeParity(t *testing.T) {
	for _, kind := range []string{"signature", "key"} {
		t.Run(kind, func(t *testing.T) {
			f := newCryptoFixture(t, true)
			p := f.policy("openpgp")
			// Generate/sign in the past, then verify at the real current time. No machine
			// clock changes or sleeps, and expiration is proved by the real status stream.
			past := time.Now().Add(-72 * time.Hour)
			expiry := "0"
			if kind == "key" {
				expiry = "1d"
			}
			f.run("gpg", "--batch", "--faked-system-time", fmt.Sprint(past.Unix()), "--pinentry-mode", "loopback", "--passphrase", "", "--quick-generate-key", "Expired Fixture <expired@example.invalid>", "ed25519", "sign", expiry)
			p.Key = fingerprint(f.run("gpg", "--batch", "--with-colons", "--list-secret-keys"))
			if p.Key == "" {
				t.Fatal("missing expired fixture key")
			}
			data := f.payload()
			// GnuPG's offset clock can advance during key generation. Sign
			// after the resulting creation time, but before the fake expiry.
			args := []string{"--batch", "--faked-system-time", fmt.Sprint(past.Add(time.Hour).Unix()), "--pinentry-mode", "loopback", "--passphrase", "", "--armor", "--detach-sign", "--local-user", p.Key}
			if kind == "signature" {
				args = append(args, "--default-sig-expire", "1d")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "gpg", args...)
			cmd.Env = f.env
			cmd.Stdin = bytes.NewReader(data)
			var sig, diagnostic bytes.Buffer
			cmd.Stdout = &sig
			cmd.Stderr = &diagnostic
			if err := cmd.Run(); err != nil {
				t.Fatalf("past signature: %v %s", err, diagnostic.String())
			}
			path := filepath.Join(f.root, "expired.sig")
			f.write(path, sig.String())
			status, _ := f.command(data, "gpg", "--status-fd=1", "--verify", path, "-")
			want := "EXPSIG"
			if kind == "key" {
				want = "EXPKEYSIG"
			}
			if !strings.Contains(status, "[GNUPG:] "+want+" ") {
				t.Fatalf("missing real %s: %s", want, status)
			}
			// Expired keys no longer carry current ownertrust. Explicit undefined
			// isolates Git's Y-versus-G distinction from the independent trust gate.
			p.MinimumTrust = 0
			f.compare(p, data, sig.Bytes(), false, kind == "key")
		})
	}
}

func (f *cryptoFixture) x509Key() string {
	// Root -> intermediate -> signing leaf. Missing-chain verification uses a
	// fresh home so GnuPG cannot satisfy the chain from an earlier verification.
	root := filepath.Join(f.root, "root.pem")
	rootKey := filepath.Join(f.root, "root.key")
	f.run("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", rootKey, "-out", root, "-days", "2", "-subj", "/CN=Fixture Root", "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=critical,keyCertSign,cRLSign")
	for _, name := range []string{"intermediate", "leaf"} {
		key := filepath.Join(f.root, name+".key")
		csr := filepath.Join(f.root, name+".csr")
		cert := filepath.Join(f.root, name+".pem")
		ext := filepath.Join(f.root, name+".ext")
		f.run("openssl", "req", "-new", "-newkey", "rsa:2048", "-nodes", "-keyout", key, "-out", csr, "-subj", "/CN=Fixture "+name+"/emailAddress=fixture@example.invalid")
		parent, parentKey := root, rootKey
		extensions := "basicConstraints=critical,CA:TRUE,pathlen:0\nkeyUsage=critical,keyCertSign,cRLSign\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid,issuer\n"
		if name == "leaf" {
			parent = filepath.Join(f.root, "intermediate.pem")
			parentKey = filepath.Join(f.root, "intermediate.key")
			extensions = "basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=emailProtection\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid,issuer\n"
		}
		f.write(ext, extensions)
		f.run("openssl", "x509", "-req", "-in", csr, "-CA", parent, "-CAkey", parentKey, "-CAcreateserial", "-out", cert, "-days", "1", "-extfile", ext)
	}
	bundle := filepath.Join(f.root, "leaf.p12")
	f.run("openssl", "pkcs12", "-export", "-legacy", "-inkey", filepath.Join(f.root, "leaf.key"), "-in", filepath.Join(f.root, "leaf.pem"), "-out", bundle, "-passout", "pass:")
	f.run("gpgsm", "--batch", "--pinentry-mode", "loopback", "--passphrase", "", "--import", bundle)
	f.run("gpgsm", "--batch", "--import", root, filepath.Join(f.root, "intermediate.pem"))
	rootFingerprint := fingerprint(f.run("gpgsm", "--with-colons", "--list-keys", "Fixture Root"))
	if rootFingerprint == "" {
		f.t.Fatal("missing root fingerprint")
	}
	f.write(filepath.Join(f.home, "trustlist.txt"), rootFingerprint+" S\n")
	f.run("gpgconf", "--homedir", f.home, "--reload", "gpg-agent")
	return fingerprint(f.run("gpgsm", "--with-colons", "--list-secret-keys"))
}
func (f *cryptoFixture) x509TrustMatrix(p Policy, data, sig []byte) {
	// GPGSM reports TRUST_FULLY for a trusted chain, never TRUST_ULTIMATE.
	for _, level := range []int{0, 1, 2, 3, 4} {
		p.MinimumTrust = level
		f.compare(p, data, sig, level <= 3, level <= 3)
	}
	f.write(filepath.Join(f.home, "trustlist.txt"), "")
	f.run("gpgconf", "--homedir", f.home, "--reload", "gpg-agent")
	for _, level := range []int{0, 2, 3, 4} {
		p.MinimumTrust = level
		// Adapter verification, Git verify-commit and Git merge each ask the
		// agent to trust this unknown root. Our noninteractive policy denies all
		// three; GnuPG logs these errors and consequently exits 2 at shutdown.
		*f.agentDenials[f.home] += 3
		f.compare(p, data, sig, false, false)
	}
	// Sign only the leaf into CMS (include-certs=1) so the missing intermediate
	// cannot arrive embedded in the signature. Signing home still has the chain.
	rootFingerprint := fingerprint(f.run("gpgsm", "--with-colons", "--list-keys", "Fixture Root"))
	f.write(filepath.Join(f.home, "trustlist.txt"), rootFingerprint+" S\n")
	f.run("gpgconf", "--homedir", f.home, "--reload", "gpg-agent")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gpgsm", "--include-certs", "1", "--armor", "--detach-sign", "--local-user", p.Key)
	cmd.Env = f.env
	cmd.Stdin = bytes.NewReader(data)
	var missing, diag bytes.Buffer
	cmd.Stdout = &missing
	cmd.Stderr = &diag
	if err := cmd.Run(); err != nil {
		f.t.Fatalf("leaf-only CMS: %v %s", err, diag.String())
	}
	missingHome := filepath.Join(f.root, "missing")
	if err := os.Mkdir(missingHome, 0700); err != nil {
		f.t.Fatal(err)
	}
	f.write(filepath.Join(missingHome, "gpgsm.conf"), "batch\nno-autostart\ndisable-crl-checks\ndisable-dirmngr\n")
	oldHome, oldEnv := f.home, f.env
	defer func() { f.home, f.env = oldHome, oldEnv }()
	f.home = missingHome
	f.env = append(append([]string{}, f.env...), "GNUPGHOME="+missingHome)
	// Give this verifier its own agent too: rejection must be the absent
	// issuer, not inability to consult a configured trust store.
	f.startAgent()
	f.run("gpgsm", "--batch", "--import", filepath.Join(f.root, "root.pem"), filepath.Join(f.root, "leaf.pem"))
	f.write(filepath.Join(missingHome, "trustlist.txt"), rootFingerprint+" S\n")
	for _, level := range []int{0, 2, 4} {
		p.MinimumTrust = level
		f.compare(p, data, missing.Bytes(), false, false)
	}
}
