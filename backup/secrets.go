package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/netbja/agent-bus-monitor/config"
)

// SecretsName is the member holding the secrets, encrypted whole with age
// (spec "Secrets: only with --secrets, only encrypted").
const SecretsName = "secrets.age"

// secretMax is the largest secret file: a password or env file is far
// smaller; one larger is refused at backup and at restore, never cut.
const secretMax = 1 << 20

// ValidRecipient reports whether r is an age public key (age1...): not a
// secret, the only form --secrets accepts (config.ValidAgeRecipient).
func ValidRecipient(r string) bool { return config.ValidAgeRecipient(r) }

// Secrets lists the secret files present for projects: the broker's
// password file and each project's services env file, as members of the
// encrypted tar (redis.env, services/<p>.env). They are only stat'ed here.
func Secrets(projects []string, loc Locations) []Source {
	var out []Source
	if exists(loc.PasswordFile) {
		out = append(out, Source{Member: "redis.env", Path: loc.PasswordFile, Required: true})
	}
	for _, p := range projects {
		if f := loc.EnvFile(p); exists(f) {
			out = append(out, Source{Member: "services/" + p + ".env", Path: f, Required: true})
		}
	}
	return out
}

// SealSecrets encrypts srcs to recipient with the age binary: a tar of the
// files is streamed straight into `age -r <recipient>`'s stdin, so the clear
// text never touches the disk, the archive or a temporary file; the result
// (ciphertext only) is returned for the archive. agentbus never holds the
// private key.
func SealSecrets(ctx context.Context, age, recipient string, srcs []Source) ([]byte, error) {
	if !ValidRecipient(recipient) {
		return nil, errors.New("the age recipient is not an age1... public key")
	}
	cmd := exec.CommandContext(ctx, age, "-r", recipient)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("age: %w", err)
	}
	werr := writeSecretsTar(in, srcs)
	cerr := in.Close()
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("age: %w: %s", err, firstLine(stderr.String()))
	}
	if werr != nil {
		return nil, werr
	}
	if cerr != nil {
		return nil, cerr
	}
	return out.Bytes(), nil
}

func writeSecretsTar(w io.Writer, srcs []Source) error {
	tw := tar.NewWriter(w)
	for _, s := range srcs {
		f, err := os.Open(s.Path)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Member, err)
		}
		st, err := f.Stat()
		if err == nil && !st.Mode().IsRegular() {
			err = fmt.Errorf("not a regular file")
		}
		if err == nil && st.Size() > secretMax {
			err = fmt.Errorf("%d bytes, over the %d a secret file may hold", st.Size(), secretMax)
		}
		if err == nil {
			err = tw.WriteHeader(&tar.Header{Name: s.Member, Mode: 0o600, Size: st.Size(), Typeflag: tar.TypeReg})
		}
		if err == nil {
			_, err = io.Copy(tw, io.LimitReader(f, st.Size()))
		}
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", s.Member, err)
		}
	}
	return tw.Close()
}

// OpenSecrets decrypts a secrets.age body with `age -d -i <identity>` (the
// person's key file, which agentbus passes to age and never reads), reads
// every secret whole first (each one its tar header's size, none over
// secretMax: a secret is refused, never cut), and only then hands each to fn
// by its member name, so a refusal hands over nothing.
func OpenSecrets(ctx context.Context, age, identity string, sealed io.Reader, fn func(name string, body []byte) error) error {
	cmd := exec.CommandContext(ctx, age, "-d", "-i", identity)
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stderr = sealed, &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("age: %w", err)
	}
	type secret struct {
		name string
		body []byte
	}
	var secrets []secret
	tr := tar.NewReader(out)
	var rerr error
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			rerr = fmt.Errorf("secrets: %w", err)
			break
		}
		if hdr.Size > secretMax {
			rerr = fmt.Errorf("secret %s: %d bytes, over the %d a secret file may hold: refused, nothing restored", hdr.Name, hdr.Size, secretMax)
			break
		}
		body, err := io.ReadAll(io.LimitReader(tr, secretMax+1))
		if err == nil && int64(len(body)) != hdr.Size {
			err = fmt.Errorf("%d bytes read, %d announced", len(body), hdr.Size)
		}
		if err != nil {
			rerr = fmt.Errorf("secret %s: %w: refused, nothing restored", hdr.Name, err)
			break
		}
		secrets = append(secrets, secret{hdr.Name, body})
	}
	io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("age -d: %w: %s", err, firstLine(stderr.String()))
	}
	if rerr != nil {
		return rerr
	}
	for _, s := range secrets {
		if err := fn(s.name, s.body); err != nil {
			return err
		}
	}
	return nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
