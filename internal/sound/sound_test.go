package sound

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
)

// Every notify event is a sound event, plus done and merged: one vocabulary.
func TestEventsReuseNotifyVocabulary(t *testing.T) {
	want := append(append([]string{}, config.NotifyEvents...), "done", "merged")
	if !reflect.DeepEqual(Events, want) {
		t.Errorf("Events %q, want %q", Events, want)
	}
	for _, e := range Events {
		if Rank(e) == 0 {
			t.Errorf("%s: no rank", e)
		}
	}
	if Rank("nonsense") != 0 {
		t.Error("an unknown event ranks")
	}
}

// fail/stall > blocked > decision/prompt > for-you > done/merged.
func TestPickHighestEventWins(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"nonsense"}, ""},
		{[]string{"done", "merged"}, "done"},
		{[]string{"merged", "for-you"}, "for-you"},
		{[]string{"for-you", "prompt"}, "prompt"},
		{[]string{"prompt", "decision"}, "prompt"},
		{[]string{"decision", "blocked"}, "blocked"},
		{[]string{"blocked", "stall"}, "stall"},
		{[]string{"blocked", "fail", "done"}, "fail"},
		{[]string{"stall", "fail"}, "stall"},
	}
	for _, c := range cases {
		if got := Pick(c.in); got != c.want {
			t.Errorf("Pick(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Default sound per event, from the spec's table.
func TestDefaultFile(t *testing.T) {
	want := map[string]string{
		"decision": "request", "prompt": "request",
		"blocked": "alert", "stall": "alert", "fail": "alert",
		"for-you": "soft", "done": "done", "merged": "done",
	}
	for _, e := range Events {
		if DefaultFile(e) != want[e] {
			t.Errorf("%s: %q, want %q", e, DefaultFile(e), want[e])
		}
	}
	if !reflect.DeepEqual(Builtins, []string{"request", "alert", "soft", "done"}) {
		t.Errorf("Builtins %q", Builtins)
	}
	if !reflect.DeepEqual(DefaultEvents, []string{"decision", "prompt", "blocked", "stall", "fail", "for-you"}) {
		t.Errorf("DefaultEvents %q: done and merged are off by default", DefaultEvents)
	}
}

func TestResolveMode(t *testing.T) {
	cases := []struct {
		name string
		in   ModeInput
		want Mode
	}{
		{"default player", ModeInput{HavePlayer: true}, Player},
		{"explicit player", ModeInput{Mode: "player", HavePlayer: true}, Player},
		{"no player: bell", ModeInput{Mode: "player"}, Bell},
		{"bell", ModeInput{Mode: "bell", HavePlayer: true}, Bell},
		{"off", ModeInput{Mode: "off", HavePlayer: true}, Off},
		{"legacy waiting.bell=false", ModeInput{LegacyBellOff: true, HavePlayer: true}, Off},
		{"explicit mode beats legacy", ModeInput{Mode: "player", LegacyBellOff: true, HavePlayer: true}, Player},
		{"env off beats the file", ModeInput{Mode: "player", Env: "off", HavePlayer: true}, Off},
		{"env OFF", ModeInput{Env: "OFF", HavePlayer: true}, Off},
		{"env other: ignored", ModeInput{Env: "on", HavePlayer: true}, Player},
		{"ssh: bell", ModeInput{SSH: true, HavePlayer: true}, Bell},
		{"ssh off stays off", ModeInput{Mode: "off", SSH: true, HavePlayer: true}, Off},
		{"unknown mode: player", ModeInput{Mode: "loud", HavePlayer: true}, Player},
	}
	for _, c := range cases {
		if got := ResolveMode(c.in); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func lookIn(found ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, f := range found {
			if f == name {
				return "/usr/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
}

// The first player found, in order, that reads the file's format; never aplay.
func TestChoosePlayer(t *testing.T) {
	cases := []struct {
		goos  string
		found []string
		file  string
		want  string
	}{
		{"linux", []string{"paplay", "pw-play", "ffplay", "mpg123", "mpv"}, "a.wav", "paplay"},
		{"linux", []string{"pw-play", "ffplay"}, "a.wav", "pw-play"},
		{"linux", []string{"mpg123", "mpv"}, "a.wav", "mpv"},
		{"linux", []string{"paplay", "pw-play", "mpg123"}, "a.mp3", "mpg123"},
		{"linux", []string{"paplay", "ffplay", "mpg123"}, "a.MP3", "ffplay"},
		{"linux", []string{"paplay"}, "a.ogg", "paplay"},
		{"linux", []string{"mpg123"}, "a.ogg", ""},
		{"linux", []string{"aplay"}, "a.wav", ""},
		{"linux", []string{"paplay"}, "a.flac", ""},
		{"linux", []string{"paplay", "mpv"}, "/x/alert.", ""},
		{"linux", []string{"paplay", "mpv"}, "a.wa", ""},
		{"linux", []string{"mpg123"}, "a.mp", ""},
		{"darwin", []string{"afplay", "paplay"}, "a.wav", "afplay"},
		{"darwin", []string{"afplay"}, "a.mp3", "afplay"},
		{"windows", []string{"powershell"}, "a.wav", "powershell"},
		{"windows", []string{"powershell"}, "a.mp3", ""},
		{"plan9", []string{"paplay"}, "a.wav", ""},
	}
	for _, c := range cases {
		ps := Find(c.goos, lookIn(c.found...))
		p, ok := ps.For(c.file)
		got := ""
		if ok {
			got = p.Name
		}
		if got != c.want {
			t.Errorf("%s %v %s: %q, want %q", c.goos, c.found, c.file, got, c.want)
		}
	}
	if Find("linux", lookIn()).Any() {
		t.Error("no player found, Any true")
	}
	if !Find("linux", lookIn("mpv")).Any() {
		t.Error("mpv found, Any false")
	}
}

// Volume 0..100 maps onto each player's own scale.
func TestPlayerArgv(t *testing.T) {
	cases := []struct {
		goos, name, file string
		vol              int
		want             []string
	}{
		{"linux", "paplay", "/c/a.wav", 70, []string{"/usr/bin/paplay", "--volume=45875", "/c/a.wav"}},
		{"linux", "paplay", "/c/a.wav", 100, []string{"/usr/bin/paplay", "--volume=65536", "/c/a.wav"}},
		{"linux", "pw-play", "/c/a.wav", 70, []string{"/usr/bin/pw-play", "--volume", "0.70", "/c/a.wav"}},
		{"linux", "ffplay", "/c/a.wav", 70, []string{"/usr/bin/ffplay", "-nodisp", "-autoexit", "-loglevel", "quiet", "-volume", "70", "/c/a.wav"}},
		{"linux", "mpg123", "/c/a.mp3", 70, []string{"/usr/bin/mpg123", "-q", "-f", "22937", "/c/a.mp3"}},
		{"linux", "mpv", "/c/a.wav", 70, []string{"/usr/bin/mpv", "--no-video", "--really-quiet", "--volume=70", "/c/a.wav"}},
		{"darwin", "afplay", "/c/a.wav", 70, []string{"/usr/bin/afplay", "-v", "0.70", "/c/a.wav"}},
		{"linux", "paplay", "/c/a.wav", 0, []string{"/usr/bin/paplay", "--volume=0", "/c/a.wav"}},
		{"linux", "mpv", "/c/a.wav", 150, []string{"/usr/bin/mpv", "--no-video", "--really-quiet", "--volume=100", "/c/a.wav"}},
		{"linux", "mpv", "/c/a.wav", -5, []string{"/usr/bin/mpv", "--no-video", "--really-quiet", "--volume=0", "/c/a.wav"}},
		{"windows", "powershell", `C:\c\it's.wav`, 70, []string{"/usr/bin/powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command",
			`(New-Object System.Media.SoundPlayer 'C:\c\it''s.wav').PlaySync()`}},
	}
	for _, c := range cases {
		p, ok := Find(c.goos, lookIn(c.name)).For(c.file)
		if !ok || p.Name != c.name {
			t.Fatalf("%s: not chosen", c.name)
		}
		if got := p.Argv(c.file, c.vol); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s vol %d: %q, want %q", c.name, c.vol, got, c.want)
		}
	}
}

type fakeRunner struct {
	mu    sync.Mutex
	calls [][]string
	block bool
	left  []time.Duration
	done  chan struct{}
}

func (f *fakeRunner) run(ctx context.Context, bin string, args ...string) error {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{bin}, args...))
	if dl, ok := ctx.Deadline(); ok {
		f.left = append(f.left, time.Until(dl))
	}
	f.mu.Unlock()
	if f.block {
		<-ctx.Done()
	}
	if f.done != nil {
		f.done <- struct{}{}
	}
	return ctx.Err()
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// Play is async and bounded at 5 s: the runner gets a deadline, and a player
// that never exits does not hold the caller.
func TestPlayAsyncBounded(t *testing.T) {
	f := &fakeRunner{block: true, done: make(chan struct{}, 1)}
	p, _ := Find("linux", lookIn("paplay")).For("/c/a.wav")
	start := time.Now()
	PlayAsync(f.run, p, "/c/a.wav", 70, 50*time.Millisecond)
	if time.Since(start) > 20*time.Millisecond {
		t.Fatal("PlayAsync blocked")
	}
	select {
	case <-f.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the bounded play never returned")
	}
	if f.count() != 1 {
		t.Fatalf("%d plays", f.count())
	}

	g := &fakeRunner{done: make(chan struct{}, 1)}
	PlayAsync(g.run, p, "/c/a.wav", 70, 0)
	<-g.done
	if l := g.left[0]; l <= 4*time.Second || l > PlayTimeout {
		t.Errorf("default bound %v, want %v", l, PlayTimeout)
	}
	if PlayTimeout != 5*time.Second {
		t.Errorf("PlayTimeout %v", PlayTimeout)
	}
}

// The real runner kills and reaps a process that outlives its bound.
func TestRunBoundedKillsAndReaps(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := runBounded(ctx, "sleep", "10")
	if err == nil {
		t.Fatal("killed process: no error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("not killed: %v", time.Since(start))
	}
	if err := runBounded(context.Background(), "true"); err != nil {
		t.Errorf("true: %v", err)
	}
}

// Under go test the real runner refuses to start a player: a test that would
// play a real sound fails.
func TestExecRunnerRefusesUnderTest(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("ExecRunner ran a real player under go test")
		}
	}()
	_ = ExecRunner(context.Background(), "true")
}

// At least 3 s between two sounds.
func TestThrottle(t *testing.T) {
	th := Throttle{}
	t0 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	if !th.Allow(t0) {
		t.Fatal("first sound refused")
	}
	if th.Allow(t0.Add(2999 * time.Millisecond)) {
		t.Error("sound within 3 s allowed")
	}
	if !th.Allow(t0.Add(3 * time.Second)) {
		t.Error("sound at 3 s refused")
	}
	if th.Allow(t0.Add(4 * time.Second)) {
		t.Error("the refused attempt did not count, but the allowed one must")
	}
	if MinGap != 3*time.Second {
		t.Errorf("MinGap %v", MinGap)
	}
}

// Embedded data is written once to the cache dir and played from there.
func TestMaterialize(t *testing.T) {
	dir := t.TempDir()
	p, err := Materialize(dir, "request", []byte("RIFF1"))
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(dir, "agentbus", "sounds", "request.wav") {
		t.Errorf("path %s", p)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "RIFF1" {
		t.Errorf("content %q", b)
	}
	st1, _ := os.Stat(p)
	time.Sleep(10 * time.Millisecond)
	if _, err := Materialize(dir, "request", []byte("RIFF1")); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(p)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Error("same content rewritten")
	}
	if _, err := Materialize(dir, "request", []byte("RIFF2")); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	if string(b) != "RIFF2" {
		t.Errorf("changed asset not rewritten: %q", b)
	}
	if _, err := Materialize(dir, "../x", []byte("y")); err == nil {
		t.Error("a name with a path accepted")
	}
}

// A file setting is a builtin, none, or a path; relative paths resolve from
// the config file's directory.
func TestResolveFile(t *testing.T) {
	cases := []struct {
		event, val string
		kind       FileKind
		want       string
	}{
		{"decision", "", Builtin, "request"},
		{"fail", "", Builtin, "alert"},
		{"done", "soft", Builtin, "soft"},
		{"done", " none ", None, ""},
		{"done", "mine.wav", Path, "/home/u/.config/agentbus/mine.wav"},
		{"done", "sounds/x.ogg", Path, "/home/u/.config/agentbus/sounds/x.ogg"},
		{"done", "/abs/x.mp3", Path, "/abs/x.mp3"},
		{"done", "nonsense", Builtin, "done"},
		{"done", "x.flac", Builtin, "done"},
		{"done", "beep.mp", Builtin, "done"},
		{"done", "beep.og", Builtin, "done"},
		{"done", "beep.wa", Builtin, "done"},
		{"done", "alert.", Builtin, "done"},
	}
	for _, c := range cases {
		k, got := ResolveFile(c.event, c.val, "/home/u/.config/agentbus")
		if k != c.kind || got != c.want {
			t.Errorf("%s=%q: (%v, %q), want (%v, %q)", c.event, c.val, k, got, c.kind, c.want)
		}
	}
	if !reflect.DeepEqual(PlayableExts, []string{".wav", ".ogg", ".mp3"}) {
		t.Errorf("PlayableExts %q", PlayableExts)
	}
}
