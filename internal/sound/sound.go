// Package sound plays a distinct sound per busmon event (busmon-sounds,
// proposition-sons-evenements.md): the events, the choice between a native
// player, the terminal bell and silence, the player argv per platform, and a
// bounded asynchronous play through an injected runner. It never reads the
// environment or the config itself: busmon hands it the values.
package sound

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
)

// Events are the notify events (config.NotifyEvents, one vocabulary) plus
// done (an agent back to idle after work) and merged (the merge queue pushed
// a PR), both off by default.
var Events = append(append([]string{}, config.NotifyEvents...), "done", "merged")

// DefaultEvents are the events that sound unless the person says otherwise.
var DefaultEvents = []string{"decision", "prompt", "blocked", "stall", "fail", "for-you"}

// Builtins are the embedded sounds a sound.file.<event> setting may name.
var Builtins = []string{"request", "alert", "soft", "done"}

var rank = map[string]int{
	"fail": 5, "stall": 5,
	"blocked":  4,
	"decision": 3, "prompt": 3,
	"for-you": 2,
	"done":    1, "merged": 1,
}

var defaultFile = map[string]string{
	"decision": "request", "prompt": "request",
	"blocked": "alert", "stall": "alert", "fail": "alert",
	"for-you": "soft",
	"done":    "done", "merged": "done",
}

// Rank orders events for the one sound a poll may play: fail/stall >
// blocked > decision/prompt > for-you > done/merged; 0 for an unknown event.
func Rank(event string) int { return rank[event] }

// Pick is the event that sounds when several fire in one poll: the highest
// rank, the first given on a tie; "" when none is known.
func Pick(events []string) string {
	best := ""
	for _, e := range events {
		if Rank(e) > Rank(best) {
			best = e
		}
	}
	return best
}

// DefaultFile is the builtin an event plays when sound.file.<event> is unset.
func DefaultFile(event string) string { return defaultFile[event] }

// Mode is how busmon makes a sound.
type Mode int

const (
	Off Mode = iota
	Bell
	Player
)

func (m Mode) String() string {
	switch m {
	case Bell:
		return "bell"
	case Player:
		return "player"
	}
	return "off"
}

// ModeInput is what decides the mode: the sound.mode setting ("" unset),
// the legacy waiting.bell=false, the AGENT_BUS_SOUND value, whether busmon
// runs over SSH, and whether a player was found.
type ModeInput struct {
	Mode          string
	LegacyBellOff bool
	Env           string
	SSH           bool
	HavePlayer    bool
}

// ResolveMode: the env "off" beats the file; sound.mode off (or, unset, the
// legacy waiting.bell=false) is off; bell is bell; otherwise player, which
// falls back to the bell over SSH (a player would play on the remote host)
// or when no player is found.
func ResolveMode(in ModeInput) Mode {
	if strings.EqualFold(strings.TrimSpace(in.Env), "off") {
		return Off
	}
	switch strings.TrimSpace(in.Mode) {
	case "off":
		return Off
	case "bell":
		return Bell
	case "":
		if in.LegacyBellOff {
			return Off
		}
	}
	if in.SSH || !in.HavePlayer {
		return Bell
	}
	return Player
}

// PlayableExts are the formats a custom sound file may have.
var PlayableExts = []string{".wav", ".ogg", ".mp3"}

// FileKind is what a sound.file.<event> setting names.
type FileKind int

const (
	Builtin FileKind = iota
	None
	Path
)

// ResolveFile reads a sound.file.<event> value: a builtin name, "none", or
// a path to a .wav/.ogg/.mp3 file, a relative one resolved from configDir.
// An empty or unreadable value is the event's default builtin.
func ResolveFile(event, val, configDir string) (FileKind, string) {
	v := strings.TrimSpace(val)
	if v == "none" {
		return None, ""
	}
	for _, b := range Builtins {
		if v == b {
			return Builtin, b
		}
	}
	if slices.Contains(PlayableExts, strings.ToLower(filepath.Ext(v))) {
		if !filepath.IsAbs(v) {
			v = filepath.Join(configDir, v)
		}
		return Path, v
	}
	return Builtin, DefaultFile(event)
}

// Materialize writes an embedded sound to <cacheDir>/agentbus/sounds/<name>.wav
// once (rewritten only when its content changed) and returns that path.
func Materialize(cacheDir, name string, data []byte) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\.`) {
		return "", errors.New("sound: bad name " + name)
	}
	dir := filepath.Join(cacheDir, "agentbus", "sounds")
	p := filepath.Join(dir, name+".wav")
	if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, data) {
		return p, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return "", errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return p, nil
}

// MinGap is the least time between two sounds.
const MinGap = 3 * time.Second

// Throttle lets a sound through at most once per MinGap; the zero value is
// ready. Not safe for concurrent use: busmon calls it from Update.
type Throttle struct{ last time.Time }

// Allow reports whether a sound may play at now, and if so records it.
func (t *Throttle) Allow(now time.Time) bool {
	if !t.last.IsZero() && now.Sub(t.last) < MinGap {
		return false
	}
	t.last = now
	return true
}
