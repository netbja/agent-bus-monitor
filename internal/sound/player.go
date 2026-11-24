package sound

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// PlayTimeout bounds every play: a player still running then is killed and
// reaped (herdr's lesson: mpg123 left at 100 % CPU).
const PlayTimeout = 5 * time.Second

// Runner runs a program until it exits or ctx ends; notify.RunQuiet's shape.
type Runner func(ctx context.Context, bin string, args ...string) error

type kind struct {
	name string
	exts []string // formats it reads natively, exact extensions
	argv func(bin, file string, vol int) []string
}

func frac(vol int) string { return fmt.Sprintf("%.2f", float64(vol)/100) }

// Linux candidates in order; never aplay (it plays an MP3 as noise).
var linuxKinds = []kind{
	{"paplay", []string{".wav", ".ogg"}, func(b, f string, v int) []string {
		return []string{b, fmt.Sprintf("--volume=%d", v*65536/100), f}
	}},
	{"pw-play", []string{".wav", ".ogg"}, func(b, f string, v int) []string {
		return []string{b, "--volume", frac(v), f}
	}},
	{"ffplay", []string{".wav", ".ogg", ".mp3"}, func(b, f string, v int) []string {
		return []string{b, "-nodisp", "-autoexit", "-loglevel", "quiet", "-volume", fmt.Sprint(v), f}
	}},
	{"mpg123", []string{".mp3"}, func(b, f string, v int) []string {
		return []string{b, "-q", "-f", fmt.Sprint(v * 32768 / 100), f}
	}},
	{"mpv", []string{".wav", ".ogg", ".mp3"}, func(b, f string, v int) []string {
		return []string{b, "--no-video", "--really-quiet", fmt.Sprintf("--volume=%d", v), f}
	}},
}

var darwinKinds = []kind{
	{"afplay", []string{".wav", ".mp3"}, func(b, f string, v int) []string {
		return []string{b, "-v", frac(v), f}
	}},
}

// SoundPlayer reads WAV only and has no volume: the file's own level plays.
var windowsKinds = []kind{
	{"powershell", []string{".wav"}, func(b, f string, _ int) []string {
		q := strings.ReplaceAll(f, "'", "''")
		return []string{b, "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command",
			"(New-Object System.Media.SoundPlayer '" + q + "').PlaySync()"}
	}},
}

// PlayerBin is one player found on PATH.
type PlayerBin struct {
	Name string
	bin  string
	k    kind
}

// Argv is the command that plays file at vol (0..100, clamped).
func (p PlayerBin) Argv(file string, vol int) []string {
	return p.k.argv(p.bin, file, min(max(vol, 0), 100))
}

// Players are the players found, in preference order.
type Players []PlayerBin

// Find looks up the platform's candidate players with lookPath.
func Find(goos string, lookPath func(string) (string, error)) Players {
	var ks []kind
	switch goos {
	case "linux", "freebsd", "openbsd", "netbsd":
		ks = linuxKinds
	case "darwin":
		ks = darwinKinds
	case "windows":
		ks = windowsKinds
	}
	var ps Players
	for _, k := range ks {
		if bin, err := lookPath(k.name); err == nil {
			ps = append(ps, PlayerBin{Name: k.name, bin: bin, k: k})
		}
	}
	return ps
}

// Any reports whether a player was found.
func (ps Players) Any() bool { return len(ps) > 0 }

// For is the first player that reads file's format.
func (ps Players) For(file string) (PlayerBin, bool) {
	ext := strings.ToLower(filepath.Ext(file))
	for _, p := range ps {
		if slices.Contains(p.k.exts, ext) {
			return p, true
		}
	}
	return PlayerBin{}, false
}

// PlayAsync plays file with p in the background, bounded (PlayTimeout when
// bound <= 0); it returns at once and never blocks busmon's Update.
func PlayAsync(run Runner, p PlayerBin, file string, vol int, bound time.Duration) {
	if bound <= 0 {
		bound = PlayTimeout
	}
	argv := p.Argv(file, vol)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), bound)
		defer cancel()
		_ = run(ctx, argv[0], argv[1:]...)
	}()
}

// ExecRunner is the real runner. Under go test it panics: no test may play a
// real sound, every test injects a fake.
func ExecRunner(ctx context.Context, bin string, args ...string) error {
	if testing.Testing() {
		panic("sound: a test started a real player " + bin + "; inject a fake Runner")
	}
	return runBounded(ctx, bin, args...)
}

// runBounded runs the command until it exits or ctx ends, then kills it and
// waits for it (reaped, no zombie); output discarded.
func runBounded(ctx context.Context, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = time.Second
	return cmd.Run()
}
