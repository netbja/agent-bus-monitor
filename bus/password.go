package bus

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The broker's password file (redis-password): one REDIS_PASSWORD="..." line,
// 0600 in a 0700 directory, that the clients read here and compose's env_file
// reads for the container. Install writes it for a fresh broker only; an
// install made before keeps the historical default until the person rotates.

// passwordKey is the file's one variable, the same name the environment uses.
const passwordKey = "REDIS_PASSWORD"

// PasswordFile is the file's path: $XDG_CONFIG_HOME/agentbus/redis.env, else
// $HOME/.config/agentbus/redis.env; "" when neither names an absolute
// directory (never a path relative to the working directory).
func PasswordFile(getenv func(string) string) string {
	if dir := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "agentbus", "redis.env")
	}
	if home := getenv("HOME"); filepath.IsAbs(home) {
		return filepath.Join(home, ".config", "agentbus", "redis.env")
	}
	return ""
}

// ReadPassword reads the file's password. A missing file is fs.ErrNotExist;
// a file with no usable REDIS_PASSWORD line is an error that never quotes the
// file's content.
func ReadPassword(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.TrimSpace(k) != passwordKey {
			continue
		}
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, `"`) {
			u, err := strconv.Unquote(v)
			if err != nil {
				return "", fmt.Errorf("%s: %s is not a quoted value", path, passwordKey)
			}
			v = u
		}
		if v == "" {
			return "", fmt.Errorf("%s: %s is empty", path, passwordKey)
		}
		return v, nil
	}
	return "", fmt.Errorf("%s: no %s line", path, passwordKey)
}

// WritePassword replaces the file with pw, atomically: 0700 directory, 0600
// file written beside it and renamed over it.
func WritePassword(path, pw string) error {
	if pw == "" || strings.ContainsAny(pw, "\"\\\n\r") {
		return errors.New("refusing a password that is empty or needs escaping")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".redis.env-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	body := "# agentbus: the Redis broker's password, read by agentbus, busmon and docker compose. Keep it 0600.\n" +
		passwordKey + "=" + strconv.Quote(pw) + "\n"
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// NewPassword is 32 random bytes, base64url: nothing in it needs quoting in
// an env file, a Redis config line or a URL.
func NewPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// resolvePassword is the credential Connect uses without REDIS_URL: the
// environment's REDIS_PASSWORD, else the file, else the historical default.
// A file that exists but cannot be read is an error, never the default.
func resolvePassword(getenv func(string) string) (string, error) {
	if pw := getenv(passwordKey); pw != "" {
		return pw, nil
	}
	path := PasswordFile(getenv)
	if path == "" {
		return LegacyPassword, nil
	}
	pw, err := ReadPassword(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return LegacyPassword, nil
	case err != nil:
		return "", fmt.Errorf("redis password: %w", err)
	}
	return pw, nil
}
