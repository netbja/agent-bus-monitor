package loginexpiry

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// The sentinels stand for the tokens beside the one field read: none may
// ever leave the reader (spec §3.3, §6).
const (
	sentAccess  = "SENTINEL-ACCESS-7f3a"
	sentRefresh = "SENTINEL-REFRESH-91bc"
)

func creds(expiry string) string {
	return `{"claudeAiOauth":{"accessToken":"` + sentAccess + `","refreshToken":"` + sentRefresh +
		`","expiresAt":1763300000000,"refreshTokenExpiresAt":` + expiry + `,"scopes":["user:inference"]},"other":"` + sentAccess + `"}`
}

func write(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func noSentinel(t *testing.T, what string, s string) {
	t.Helper()
	if strings.Contains(s, sentAccess) || strings.Contains(s, sentRefresh) || strings.Contains(s, "1763300000000") {
		t.Fatalf("%s leaks a token or another field: %q", what, s)
	}
}

// The value is Unix milliseconds.
func TestReadFileMilliseconds(t *testing.T) {
	want := now.Add(60 * time.Hour).Truncate(time.Millisecond)
	got, why := ReadFile(write(t, creds(itoa(want.UnixMilli()))), now)
	if why != OK || !got.Equal(want) {
		t.Fatalf("%v %q", got, why)
	}
}

// Each row of §3.3's table, one case each; the reason is exactly the row's,
// and nothing read leaves the reader.
func TestReadFileReasons(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(write(t, creds(itoa(now.Add(time.Hour).UnixMilli()))), link); err != nil {
		t.Fatal(err)
	}
	big := write(t, creds(itoa(now.Add(time.Hour).UnixMilli()))+strings.Repeat(" ", 65*1024))
	unreadable := write(t, creds(itoa(now.Add(time.Hour).UnixMilli())))
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path string
		want       Reason
	}{
		{"relative path", ".credentials.json", InvalidPath},
		{"unclean path", dir + "/../" + filepath.Base(dir) + "/.credentials.json", InvalidPath},
		{"no file", filepath.Join(dir, "none.json"), NoFile},
		{"a directory", dir, NotRegular},
		{"a symlink", link, NotRegular},
		{"too large", big, TooLarge},
		{"not JSON", write(t, "not json "+sentAccess), NoField},
		{"no field", write(t, `{"claudeAiOauth":{"accessToken":"`+sentAccess+`"}}`), NoField},
		{"no oauth object", write(t, `{"other":"`+sentRefresh+`"}`), NoField},
		{"a string", write(t, creds(`"`+sentRefresh+`"`)), NotANumber},
		{"a bool", write(t, creds(`true`)), NotANumber},
		{"null", write(t, creds(`null`)), NotANumber},
		{"a fraction", write(t, creds(`1763300000000.5`)), NotANumber},
		{"seconds-sized", write(t, creds(itoa(now.Add(60*time.Hour).Unix()))), NotATime},
		{"before 2020", write(t, creds(itoa(time.Date(2019, 12, 31, 0, 0, 0, 0, time.UTC).UnixMilli()))), NotATime},
		{"over two years ahead", write(t, creds(itoa(now.AddDate(2, 0, 1).UnixMilli()))), NotATime},
		{"beyond int64", write(t, creds(`99999999999999999999999`)), NotATime},
		{"negative", write(t, creds(`-5`)), NotATime},
	}
	if os.Getuid() != 0 { // root reads a 0000 file
		cases = append(cases, struct {
			name, path string
			want       Reason
		}{"unreadable", unreadable, Unreadable})
	}
	for _, c := range cases {
		got, why := ReadFile(c.path, now)
		if why != c.want || !got.IsZero() {
			t.Errorf("%s: %v %q, want %q", c.name, got, why, c.want)
		}
		noSentinel(t, c.name, string(why))
	}
}

// Owned by another user: unknown, not owned (the owner is injected: a test
// cannot chown).
func TestReadFileNotOwned(t *testing.T) {
	p := write(t, creds(itoa(now.Add(time.Hour).UnixMilli())))
	old := ownerOK
	ownerOK = func(os.FileInfo) bool { return false }
	defer func() { ownerOK = old }()
	if got, why := ReadFile(p, now); why != NotOwned || !got.IsZero() {
		t.Fatalf("%v %q", got, why)
	}
}

// The reasons are one closed set, and none carries anything read.
func TestReasonsAreClosed(t *testing.T) {
	for _, r := range []Reason{InvalidPath, NoFile, NotRegular, NotOwned, Unreadable, TooLarge, NoField, NotANumber, NotATime, KeychainUnverified} {
		if !Known(r) {
			t.Errorf("%q not in the set", r)
		}
	}
	if !Known(KeychainExit(3)) || KeychainExit(3) != "keychain: exit 3" {
		t.Fatalf("%q", KeychainExit(3))
	}
	if Known("keychain: exit x") || Known("no file: /home/x") || Known("") {
		t.Fatal("an unknown reason is known")
	}
}

// The credentials file of a config directory; "" is the default ~/.claude.
func TestCredentialsFile(t *testing.T) {
	if got := CredentialsFile("", "/home/u"); got != "/home/u/.claude/.credentials.json" {
		t.Fatal(got)
	}
	if got := CredentialsFile("/srv/claude-b", "/home/u"); got != "/srv/claude-b/.credentials.json" {
		t.Fatal(got)
	}
	if got := CredentialsFile("rel", "/home/u"); got != "rel/.credentials.json" { // ReadFile refuses it: invalid path
		t.Fatal(got)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
