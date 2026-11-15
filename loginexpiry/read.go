// Package loginexpiry reads one field, Claude Code's refresh-token expiry
// (claudeAiOauth.refreshTokenExpiresAt, Unix milliseconds), from an agent's
// credentials (login-updates spec r9 §3). Nothing else is read: what leaves
// a reader is a time and a reason from one closed set. The file's bytes, any
// token, any other field and the decoder's error stay in the function.
package loginexpiry

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Reason is why no expiry was read; OK when one was. The set is closed
// (§3.3): each case maps to exactly one reason, and none carries anything
// read from the file or the Keychain.
type Reason string

const (
	OK                 Reason = ""
	InvalidPath        Reason = "invalid path"
	NoFile             Reason = "no file"
	NotRegular         Reason = "not a regular file"
	NotOwned           Reason = "not owned"
	Unreadable         Reason = "unreadable"
	TooLarge           Reason = "too large"
	NoField            Reason = "no field"
	NotANumber         Reason = "not a number"
	NotATime           Reason = "not a time"
	KeychainUnverified Reason = "keychain entry not verified"
)

const keychainExitPrefix = "keychain: exit "

// KeychainExit is the reason for a `security` that exits n or prints nothing.
func KeychainExit(n int) Reason { return Reason(keychainExitPrefix + strconv.Itoa(n)) }

// Known reports a reason of the closed set.
func Known(r Reason) bool {
	switch r {
	case InvalidPath, NoFile, NotRegular, NotOwned, Unreadable, TooLarge, NoField, NotANumber, NotATime, KeychainUnverified:
		return true
	}
	if n, ok := strings.CutPrefix(string(r), keychainExitPrefix); ok {
		_, err := strconv.Atoi(n)
		return err == nil
	}
	return false
}

// MaxSize is the most read: a larger file is TooLarge.
const MaxSize = 64 << 10

// floor is the earliest plausible expiry; a value before it, or more than
// two years after now, is not milliseconds (a seconds-sized one, should the
// CLI ever write seconds): NotATime, never an expired login.
var floor = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// CredentialsFile is the credentials file of an agent's Claude config
// directory; "" is the default, <home>/.claude.
func CredentialsFile(configDir, home string) string {
	if configDir == "" {
		configDir = filepath.Join(home, ".claude")
	}
	return configDir + string(filepath.Separator) + ".credentials.json"
}

// ownerOK: the file is owned by this process's user (a variable: a test
// cannot chown).
var ownerOK = ownedByMe

// ReadFile reads the expiry from the credentials file at path.
func ReadFile(path string, now time.Time) (time.Time, Reason) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return time.Time{}, InvalidPath
	}
	fi, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
		return time.Time{}, NoFile
	case err != nil:
		return time.Time{}, Unreadable
	case !fi.Mode().IsRegular():
		return time.Time{}, NotRegular
	case !ownerOK(fi):
		return time.Time{}, NotOwned
	case fi.Size() > MaxSize:
		return time.Time{}, TooLarge
	}
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, Unreadable
	}
	defer f.Close()
	// The file opened is the one checked: no symlink swapped in between.
	if got, err := f.Stat(); err != nil || !os.SameFile(fi, got) {
		return time.Time{}, NotRegular
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return time.Time{}, Unreadable
	}
	if len(b) > MaxSize {
		return time.Time{}, TooLarge
	}
	return parse(b, now)
}

// parse takes the one field from the credentials' JSON.
func parse(b []byte, now time.Time) (time.Time, Reason) {
	var doc struct {
		OAuth *struct {
			Expiry json.RawMessage `json:"refreshTokenExpiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(b, &doc) != nil || doc.OAuth == nil || len(doc.OAuth.Expiry) == 0 {
		return time.Time{}, NoField
	}
	n, ok := new(big.Int).SetString(string(bytes.TrimSpace(doc.OAuth.Expiry)), 10)
	if !ok {
		return time.Time{}, NotANumber // a string, a bool, null, an object, a fraction, an exponent
	}
	if !n.IsInt64() {
		return time.Time{}, NotATime
	}
	t := time.UnixMilli(n.Int64()).UTC()
	if t.Before(floor) || t.After(now.AddDate(2, 0, 0)) {
		return time.Time{}, NotATime
	}
	return t, OK
}
