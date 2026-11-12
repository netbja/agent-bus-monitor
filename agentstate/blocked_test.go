package agentstate

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Herdr exit step 2b, spec 4.3: a Blocked signature per client, pinned by a
// captured screen. Only the menus a fixture holds have one: agy's approval
// menus and Claude Code's question menu. Codex, Kimi and Grok wait for step
// 3's recordings (their blocked source is "to verify", 4.9).
func TestBlockedSignatures(t *testing.T) {
	sigs := Signatures()
	for _, c := range []struct{ client, screen, what string }{
		{"agy", "agy153-approval", "touch ap1"},
		{"agy", "agy-approval", "sleep 8"},
		{"claude", "claude-modal", "Pick red or blue?"},
	} {
		blocked, what := sigs[c.client].BlockedOn(readScreen(t, c.screen+".txt"))
		if !blocked || what != c.what {
			t.Errorf("%s: blocked=%v what=%q; want true %q", c.screen, blocked, what, c.what)
		}
	}
	entries, err := os.ReadDir("testdata/screens")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".txt")
		if name == "agy153-approval" || name == "agy-approval" || name == "claude-modal" || name == "README.md" {
			continue
		}
		client := strings.SplitN(name, "-", 2)[0]
		if client == "agy153" {
			client = "agy"
		}
		if sig, ok := sigs[client]; ok {
			if blocked, _ := sig.BlockedOn(readScreen(t, e.Name())); blocked {
				t.Errorf("%s reads as blocked", e.Name())
			}
		}
	}
	// A menu right above a ready input box is an answered one (DERIVED).
	if b, _ := sigs["claude"].BlockedOn(readScreen(t, "claude-modal-accepted-close.txt")); b {
		t.Error("a menu above a ready input box read as blocked")
	}
	// A menu far above the bottom is an answered one.
	if b, _ := sigs["agy"].BlockedOn(readScreen(t, "agy153-approval.txt") + strings.Repeat("later output\n", 20)); b {
		t.Error("a menu 20 lines above the bottom read as blocked")
	}
	for _, c := range []string{"codex", "kimi", "grok"} {
		if len(sigs[c].Blocked) != 0 {
			t.Errorf("%s has a Blocked signature no fixture pins", c)
		}
	}
}

// The menu's own words are data: one line, no control characters, at most
// 120 characters, quoted.
func TestScreenLabel(t *testing.T) {
	for in, want := range map[string]string{
		"touch ap1":                 `screen: "touch ap1"`,
		"rm -rf x\n\x1b[31mred\x07": `screen: "rm -rf x red"`,
		`say "hi"`:                  `screen: "say \"hi\""`,
		strings.Repeat("é", 130):    `screen: "` + strings.Repeat("é", 119) + `…"`,
		"  \t  ":                    "",
		"tab\there":                 `screen: "tab here"`,
		"‮evil":                     `screen: "evil"`,
		strings.Repeat("a", 120):    `screen: "` + strings.Repeat("a", 120) + `"`,
	} {
		if got := ScreenLabel(in); got != want {
			t.Errorf("ScreenLabel(%q) = %q; want %q", in, got, want)
		}
	}
}

// The episode (4.3a): a hook enters at once; the Blocked signature enters
// on two settled screens in a row; an unsettled read neither counts nor
// resets. Leaving needs no source still saying blocked. One pair per
// episode; a new pid ends the old one.
func TestBlockedEpisode(t *testing.T) {
	t0 := time.Date(2026, 11, 12, 14, 3, 7, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	type step struct {
		obs  Observation
		want string // the event's line, "" for none
	}
	cases := map[string][]step{
		"screen needs two settled reads": {
			{Observation{PID: 7, At: at(0), Settled: true, Screen: true, What: "touch ap1"}, ""},
			{Observation{PID: 7, At: at(1), Settled: false, Screen: false}, ""}, // unsettled: ignored
			{Observation{PID: 7, At: at(2), Settled: true, Screen: true, What: "touch ap1"}, `blocked: coder (agy) since 14:03:07 — screen: "touch ap1"`},
			{Observation{PID: 7, At: at(3), Settled: true, Screen: true, What: "touch ap1"}, ""}, // one enter per episode
			{Observation{PID: 7, At: at(4), Settled: false, Screen: false}, ""},                  // unsettled: no leave
			{Observation{PID: 7, At: at(195), Settled: true, Screen: false}, "unblocked: coder after 3m15s"},
			{Observation{PID: 7, At: at(196), Settled: true, Screen: false}, ""},
		},
		"a settled clear read resets the count": {
			{Observation{PID: 7, At: at(0), Settled: true, Screen: true}, ""},
			{Observation{PID: 7, At: at(1), Settled: true, Screen: false}, ""},
			{Observation{PID: 7, At: at(2), Settled: true, Screen: true}, ""},
			{Observation{PID: 7, At: at(3), Settled: true, Screen: true}, "blocked: coder (agy) since 14:03:09"},
		},
		"the hook enters at once and holds without a screen signature": {
			{Observation{PID: 7, At: at(5), Hook: true, HookSince: at(4), Settled: true}, "blocked: coder (agy) since 14:03:11"},
			{Observation{PID: 7, At: at(9), Hook: true, HookSince: at(4), Settled: true}, ""},
			{Observation{PID: 7, At: at(64), Hook: false, Settled: true}, "unblocked: coder after 1m0s"},
		},
		"the hook's end does not leave while the screen still shows the menu": {
			{Observation{PID: 7, At: at(0), Hook: true, HookSince: at(0), Settled: true, Screen: true}, "blocked: coder (agy) since 14:03:07"},
			{Observation{PID: 7, At: at(5), Settled: true, Screen: true}, ""},
			{Observation{PID: 7, At: at(6), Settled: true}, "unblocked: coder after 6s"},
		},
		"a new pid ends the episode": {
			{Observation{PID: 7, At: at(0), Hook: true, HookSince: at(0)}, "blocked: coder (agy) since 14:03:07"},
			{Observation{PID: 8, At: at(30), Hook: true, HookSince: at(30)}, "unblocked: coder after 30s"},
		},
		"no process opens nothing": {
			{Observation{PID: 0, At: at(0), Hook: true, HookSince: at(0), Settled: true, Screen: true}, ""},
			{Observation{PID: 0, At: at(1), Hook: true, HookSince: at(0), Settled: true, Screen: true}, ""},
		},
		"a gone agent ends the episode": {
			{Observation{PID: 7, At: at(0), Hook: true, HookSince: at(0)}, "blocked: coder (agy) since 14:03:07"},
			{Observation{PID: 0, At: at(12)}, "unblocked: coder after 12s"},
		},
	}
	for name, steps := range cases {
		t.Run(name, func(t *testing.T) {
			e := Episode{Agent: "coder", Client: "agy"}
			for i, s := range steps {
				var ev *BlockedEvent
				e, ev = e.Step(s.obs)
				got := ""
				if ev != nil {
					got = ev.Line()
				}
				if got != s.want {
					t.Fatalf("step %d: event %q; want %q", i, got, s.want)
				}
			}
		})
	}
}

// The new pid's own hook opens its own episode on the next observation: the
// old episode's leave and the new one's enter are two events, never merged.
func TestBlockedEpisodeAfterANewPid(t *testing.T) {
	t0 := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	e := Episode{Agent: "coder", Client: "claude"}
	e, _ = e.Step(Observation{PID: 7, At: t0, Hook: true, HookSince: t0})
	e, ev := e.Step(Observation{PID: 8, At: t0.Add(time.Minute), Hook: true, HookSince: t0.Add(time.Minute)})
	if ev == nil || ev.Kind != Unblocked || e.Open {
		t.Fatalf("pid change: %+v %+v", ev, e)
	}
	e, ev = e.Step(Observation{PID: 8, At: t0.Add(61 * time.Second), Hook: true, HookSince: t0.Add(time.Minute)})
	if ev == nil || ev.Kind != Blocked || ev.Line() != "blocked: coder (claude) since 09:01:00" {
		t.Fatalf("new episode: %+v", ev)
	}
}
