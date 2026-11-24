// Package signing implements Git's external cryptographic protocols. Repository,
// config, object and ref operations remain in the native backend.
package signing

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/process"
)

type Policy struct {
	Sign, Verify                                  bool
	Format                                        string
	Programs                                      map[string]string
	Key                                           string
	HasKey                                        bool
	DefaultKeyCommand, AllowedSigners, Revocation string
	MinimumTrust                                  int
}

var trustNames = []string{"undefined", "never", "marginal", "fully", "ultimate"}

func trust(value string) (int, error) {
	for i, name := range trustNames {
		if strings.EqualFold(value, name) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("invalid signature trust level %q", value)
}

// Load reads the signature policy of the repository's current checkout.
func Load(repo *git.Repository) (Policy, error) {
	entries, err := repo.ConfigurationEntries()
	if err != nil {
		return Policy{}, err
	}
	return ParsePolicy(entries)
}

// ParsePolicy retains traversal order: gpg.program and gpg.openpgp.program
// are aliases, not a fixed-precedence pair.
func ParsePolicy(entries []git.ConfigurationEntry) (Policy, error) {
	p := Policy{Format: "openpgp", Programs: map[string]string{"openpgp": "gpg", "x509": "gpgsm", "ssh": "ssh-keygen"}, MinimumTrust: 2}
	// Parse all assignments in order, including invalid shadowed booleans,
	// from the same snapshot as the crypto settings.
	for _, entry := range entries {
		if entry.Name != "commit.gpgsign" && entry.Name != "merge.verifysignatures" {
			continue
		}
		value, err := git.ConfigurationBool(entry)
		if err != nil {
			return p, fmt.Errorf("%s: %w", entry.Name, err)
		}
		if entry.Name == "commit.gpgsign" {
			p.Sign = value
		} else {
			p.Verify = value
		}
	}
	if !p.Sign && !p.Verify {
		return p, nil
	}
	for _, entry := range entries {
		key, value := entry.Name, entry.Value
		switch key {
		case "user.signingkey", "gpg.format", "gpg.mintrustlevel", "gpg.ssh.defaultkeycommand", "gpg.ssh.allowedsignersfile", "gpg.ssh.revocationfile", "gpg.program", "gpg.openpgp.program", "gpg.x509.program", "gpg.ssh.program":
			if !entry.HasValue {
				return p, fmt.Errorf("%s requires a value", key)
			}
		default:
			continue
		}
		var err error
		switch key {
		case "user.signingkey":
			p.Key, p.HasKey = value, true
		case "gpg.format":
			if _, ok := p.Programs[value]; !ok {
				return p, fmt.Errorf("invalid gpg.format %q", value)
			}
			p.Format = value
		case "gpg.mintrustlevel":
			p.MinimumTrust, err = trust(value)
		case "gpg.ssh.defaultkeycommand":
			p.DefaultKeyCommand = value
		case "gpg.ssh.allowedsignersfile":
			p.AllowedSigners, err = git.ConfigurationPath(value)
		case "gpg.ssh.revocationfile":
			p.Revocation, err = git.ConfigurationPath(value)
		default:
			format := strings.TrimSuffix(strings.TrimPrefix(key, "gpg."), ".program")
			if key == "gpg.program" {
				format = "openpgp"
			}
			p.Programs[format], err = git.ConfigurationPath(value)
		}
		if err != nil {
			return p, err
		}
	}
	return p, nil
}

type Adapter struct {
	Runner      process.Runner
	Directory   string
	Environment []string
	Diagnostic  io.Writer
}

func (a Adapter) run(ctx context.Context, args []string, input []byte) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	runner := a.Runner
	if runner == nil {
		runner = process.Native{}
	}
	var out, diagnostic bytes.Buffer
	result := runner.Run(ctx, process.Command{Args: args, Directory: a.Directory, Environment: a.Environment, In: bytes.NewReader(input), Out: &out, Err: &diagnostic, Group: true})
	if err := ctx.Err(); err != nil {
		return out.String(), diagnostic.String(), err
	}
	if result.Code != 0 || result.Err != nil {
		return out.String(), diagnostic.String(), process.Failure(args, result, out.String(), diagnostic.String())
	}
	return out.String(), diagnostic.String(), nil
}

// splitCommand matches Git's split_cmdline, not shell evaluation. In particular
// backslash quotes any following character inside double quotes as well.
func splitCommand(value string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote byte
	for i := 0; i < len(value); i++ {
		c := value[i]
		if quote == 0 && strings.ContainsRune(" \t\r\n\v\f", rune(c)) {
			args = append(args, word.String())
			word.Reset()
			for i+1 < len(value) && strings.ContainsRune(" \t\r\n\v\f", rune(value[i+1])) {
				i++
			}
		} else if quote == 0 && (c == '\'' || c == '"') {
			quote = c
		} else if c == quote {
			quote = 0
		} else {
			if c == '\\' && quote != '\'' {
				i++
				if i == len(value) {
					return nil, errors.New("defaultKeyCommand ends with backslash")
				}
				c = value[i]
			}
			word.WriteByte(c)
		}
	}
	if quote != 0 {
		return nil, errors.New("defaultKeyCommand has unclosed quote")
	}
	args = append(args, word.String())
	if args[0] == "" {
		return nil, errors.New("defaultKeyCommand has empty executable")
	}
	return args, nil
}

func literalKey(key string) (string, bool) {
	if strings.HasPrefix(key, "key::") {
		return strings.TrimPrefix(key, "key::"), true
	}
	return key, strings.HasPrefix(key, "ssh-")
}

func temporary() (string, error) { return os.MkdirTemp("", "task-signature-*") }
func writeTemporary(dir, name string, data []byte) (string, error) {
	path := filepath.Join(dir, name)
	return path, os.WriteFile(path, data, 0600)
}

func (a Adapter) Sign(ctx context.Context, p Policy, payload []byte, committer git.Signature) ([]byte, error) {
	key := p.Key
	if !p.HasKey {
		if p.Format == "ssh" {
			if p.DefaultKeyCommand == "" {
				return nil, errors.New("either user.signingKey or gpg.ssh.defaultKeyCommand is required")
			}
			args, err := splitCommand(p.DefaultKeyCommand)
			if err != nil {
				return nil, err
			}
			output, _, err := a.run(ctx, args, nil)
			if err != nil {
				return nil, err
			}
			key = strings.SplitN(output, "\n", 2)[0]
			if _, ok := literalKey(key); !ok {
				return nil, errors.New("gpg.ssh.defaultKeyCommand returned no public key")
			}
		} else {
			key = committer.Name + " <" + committer.Email + ">"
		}
	}
	var output string
	if p.Format == "ssh" {
		if key == "" {
			return nil, errors.New("user.signingKey needs to be set for ssh signing")
		}
		dir, err := temporary()
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		keyPath := key
		literal, isLiteral := literalKey(key)
		if isLiteral {
			keyPath, err = writeTemporary(dir, "key", []byte(literal))
		} else {
			keyPath, err = git.ConfigurationPath(key)
		}
		if err != nil {
			return nil, err
		}
		path, err := writeTemporary(dir, "payload", payload)
		if err != nil {
			return nil, err
		}
		args := []string{p.Programs[p.Format], "-Y", "sign", "-n", "git", "-f", keyPath}
		if isLiteral {
			args = append(args, "-U")
		}
		args = append(args, path)
		if _, _, err = a.run(ctx, args, nil); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path + ".sig")
		if err != nil {
			return nil, err
		}
		output = string(data)
	} else {
		var diagnostic string
		var err error
		output, diagnostic, err = a.run(ctx, []string{p.Programs[p.Format], "--status-fd=2", "-bsau", key}, payload)
		if err != nil {
			return nil, err
		}
		created := false
		for _, line := range strings.Split(diagnostic, "\n") {
			if strings.HasPrefix(line, "[GNUPG:] SIG_CREATED ") {
				created = true
			}
		}
		if !created {
			return nil, fmt.Errorf("signer did not report SIG_CREATED: %s", diagnostic)
		}
	}
	output = strings.ReplaceAll(output, "\r\n", "\n")
	if err := validateArmor(output, p.Format); err != nil {
		return nil, err
	}
	return []byte(output), nil
}

func signatureFormat(signature string) string {
	for format, headers := range map[string][]string{"openpgp": {"PGP SIGNATURE", "PGP MESSAGE"}, "x509": {"SIGNED MESSAGE"}, "ssh": {"SSH SIGNATURE"}} {
		for _, header := range headers {
			if strings.HasPrefix(signature, "-----BEGIN "+header+"-----") {
				return format
			}
		}
	}
	return ""
}

func validateArmor(signature, format string) error {
	if strings.ContainsRune(signature, 0) || signatureFormat(signature) != format {
		return errors.New("signer returned an empty or incompatible signature")
	}
	lines := strings.Split(strings.TrimSuffix(signature, "\n"), "\n")
	if len(lines) < 3 || lines[len(lines)-1] != strings.Replace(lines[0], "BEGIN", "END", 1) {
		return errors.New("signer returned malformed signature armor")
	}
	var encoded strings.Builder
	for _, line := range lines[1 : len(lines)-1] {
		if line == "" || strings.Contains(line, ":") || strings.HasPrefix(line, "=") {
			continue
		}
		encoded.WriteString(line)
	}
	data, err := base64.StdEncoding.DecodeString(encoded.String())
	if err != nil || len(data) == 0 {
		return errors.New("signer returned malformed signature data")
	}
	return nil
}

// VerifyMerge implements merge's stricter result policy: only GOODSIG qualifies,
// not expired signatures/keys, even when generic Git verification accepts Y.
func (a Adapter) VerifyMerge(ctx context.Context, p Policy, payload, signature []byte) error {
	format := signatureFormat(string(signature))
	if format == "" {
		return errors.New("commit has no recognized signature")
	}
	dir, err := temporary()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path, err := writeTemporary(dir, "signature", signature)
	if err != nil {
		return err
	}
	if format == "ssh" {
		return a.verifySSH(ctx, p, payload, path)
	}
	args := []string{p.Programs[format]}
	if format == "openpgp" {
		args = append(args, "--keyid-format=long")
	}
	args = append(args, "--status-fd=1", "--verify", path, "-")
	output, _, err := a.run(ctx, args, payload)
	if err != nil {
		return err
	}
	good, exclusive, level := false, 0, 0
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "[GNUPG:] ") {
			continue
		}
		status := strings.TrimPrefix(line, "[GNUPG:] ")
		for _, name := range []string{"GOODSIG ", "BADSIG ", "ERRSIG ", "EXPSIG ", "EXPKEYSIG ", "REVKEYSIG "} {
			if strings.HasPrefix(status, name) {
				exclusive++
				good = name == "GOODSIG "
			}
		}
		if strings.HasPrefix(status, "TRUST_") {
			fields := strings.Fields(strings.TrimPrefix(status, "TRUST_"))
			if len(fields) == 0 {
				return errors.New("invalid signer trust status")
			}
			level, err = trust(fields[0])
			if err != nil {
				return err
			}
		}
	}
	if !good || exclusive != 1 {
		return errors.New("commit has a bad, absent, expired, revoked or ambiguous signature")
	}
	if level < p.MinimumTrust {
		return fmt.Errorf("commit signature trust %s is below %s", trustNames[level], trustNames[p.MinimumTrust])
	}
	return nil
}

func commitTime(payload []byte) (string, error) {
	header := strings.SplitN(string(payload), "\n\n", 2)[0]
	for _, line := range strings.Split(header, "\n") {
		if !strings.HasPrefix(line, "committer ") {
			continue
		}
		end := strings.LastIndex(line, "> ")
		if end < 0 {
			break
		}
		fields := strings.Fields(line[end+2:])
		if len(fields) != 2 {
			break
		}
		timestamp, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return "", err
		}
		if timestamp == 0 {
			return "", nil
		}
		return "-Overify-time=" + time.Unix(timestamp, 0).Local().Format("20060102150405"), nil
	}
	return "", errors.New("signed commit has invalid committer timestamp")
}

func (a Adapter) verifySSH(ctx context.Context, p Policy, payload []byte, path string) error {
	if p.AllowedSigners == "" {
		return errors.New("gpg.ssh.allowedSignersFile needs to be configured and exist for ssh signature verification")
	}
	when, err := commitTime(payload)
	if err != nil {
		return err
	}
	principals, _, findErr := a.run(ctx, []string{p.Programs["ssh"], "-Y", "find-principals", "-f", p.AllowedSigners, "-s", path, when}, nil)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if findErr != nil || principals == "" {
		// Git runs check-novalidate for diagnostics, but never accepts unknown keys.
		_, _, checkErr := a.run(ctx, []string{p.Programs["ssh"], "-Y", "check-novalidate", "-n", "git", "-s", path, when}, payload)
		return errors.Join(errors.New("SSH signature has no allowed principal"), findErr, checkErr)
	}
	revocation := p.Revocation
	if revocation != "" {
		absolute := revocation
		if !filepath.IsAbs(absolute) {
			absolute = filepath.Join(a.Directory, absolute)
		}
		if _, err := os.Stat(absolute); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if a.Diagnostic != nil {
				fmt.Fprintf(a.Diagnostic, "warning: ssh signing revocation file configured but not found: %s\n", revocation)
			}
			revocation = ""
		}
	}
	var last error
	for _, principal := range strings.Split(principals, "\n") {
		principal = strings.TrimSuffix(principal, "\r")
		if principal == "" {
			continue
		}
		args := []string{p.Programs["ssh"], "-Y", "verify", "-n", "git", "-f", p.AllowedSigners, "-I", principal, "-s", path, when}
		if revocation != "" {
			args = append(args, "-r", revocation)
		}
		output, _, err := a.run(ctx, args, payload)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			last = err
			continue
		}
		line := strings.SplitN(output, "\n", 2)[0]
		prefix := "Good \"git\" signature for "
		if !strings.HasPrefix(line, prefix) || strings.LastIndex(line, " with ") < len(prefix) || !strings.Contains(line[strings.LastIndex(line, " with "):], "key ") {
			last = errors.New("malformed SSH verification output")
			continue
		}
		if p.MinimumTrust > 3 {
			return errors.New("SSH signature trust fully is below ultimate")
		}
		return nil
	}
	return errors.Join(errors.New("SSH signature verification failed"), last)
}
