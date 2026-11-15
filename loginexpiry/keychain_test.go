package loginexpiry

import (
	"testing"
	"time"
)

// A fake security: its calls counted, its answer given.
type fakeSecurity struct {
	out   string
	exit  int
	calls [][]string
}

func (f *fakeSecurity) run(name string, args ...string) ([]byte, int) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return []byte(f.out), f.exit
}

func verified(t *testing.T) {
	t.Helper()
	keychainVerified = true
	t.Cleanup(func() { keychainVerified = false })
}

// Until the service name is verified on a Mac, nothing is run.
func TestKeychainUnverified(t *testing.T) {
	f := &fakeSecurity{out: creds(itoa(now.Add(time.Hour).UnixMilli()))}
	if got, why := ReadKeychain(f.run, "", now); why != KeychainUnverified || !got.IsZero() || len(f.calls) != 0 {
		t.Fatalf("%v %q %v", got, why, f.calls)
	}
	verified(t)
	// Another config directory: its service name is not known.
	if _, why := ReadKeychain(f.run, "/srv/claude-b", now); why != KeychainUnverified || len(f.calls) != 0 {
		t.Fatalf("%q %v", why, f.calls)
	}
}

// Verified: the one field read from security's output; the command is a
// read (find-generic-password -w), never a write.
func TestKeychainReads(t *testing.T) {
	verified(t)
	want := now.Add(60 * time.Hour).Truncate(time.Millisecond)
	f := &fakeSecurity{out: creds(itoa(want.UnixMilli())) + "\n"}
	got, why := ReadKeychain(f.run, "", now)
	if why != OK || !got.Equal(want) {
		t.Fatalf("%v %q", got, why)
	}
	if len(f.calls) != 1 || f.calls[0][0] != "security" || f.calls[0][1] != "find-generic-password" || f.calls[0][len(f.calls[0])-1] != "-w" {
		t.Fatalf("calls %v", f.calls)
	}
}

// security that fails or prints nothing: keychain: exit <n>; its output
// never reaches the reason.
func TestKeychainReasons(t *testing.T) {
	verified(t)
	for _, c := range []struct {
		name string
		f    fakeSecurity
		want Reason
	}{
		{"exits non-zero", fakeSecurity{out: creds(`"` + sentRefresh + `"`), exit: 44}, KeychainExit(44)},
		{"prints nothing", fakeSecurity{out: "  \n"}, KeychainExit(0)},
		{"cannot run", fakeSecurity{exit: -1}, KeychainExit(-1)},
		{"a string", fakeSecurity{out: creds(`"` + sentRefresh + `"`)}, NotANumber},
		{"no field", fakeSecurity{out: `{"claudeAiOauth":{"accessToken":"` + sentAccess + `"}}`}, NoField},
		{"seconds", fakeSecurity{out: creds(itoa(now.Add(time.Hour).Unix()))}, NotATime},
	} {
		f := c.f
		got, why := ReadKeychain(f.run, "", now)
		if why != c.want || !got.IsZero() || !Known(why) {
			t.Errorf("%s: %v %q, want %q", c.name, got, why, c.want)
		}
		noSentinel(t, c.name, string(why))
	}
}

// Read picks the Keychain on darwin, the file elsewhere.
func TestReadByPlatform(t *testing.T) {
	f := &fakeSecurity{}
	if _, why := Read("darwin", "", "/home/u", f.run, now); why != KeychainUnverified {
		t.Fatalf("darwin: %q", why)
	}
	if _, why := Read("linux", "", "/nonexistent-home", f.run, now); why != NoFile || len(f.calls) != 0 {
		t.Fatalf("linux: %q %v", why, f.calls)
	}
}
