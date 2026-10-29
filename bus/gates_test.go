package bus

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestGateMetaRoundTrip(t *testing.T) {
	cases := []struct {
		meta                     string
		challenger, subject, txt string
	}{
		{"master|review X", "master", "", "review X"},
		{"master|[subject roles-impl] review X", "master", "roles-impl", "review X"},
		{"master|[subject pr:12] look", "master", "pr:12", "look"},
		{"master|[subject] no subject word", "master", "", "[subject] no subject word"},
		{"master|[subject unclosed text", "master", "", "[subject unclosed text"},
		{"conductor|review a/m1 (t by c)", "conductor", "", "review a/m1 (t by c)"},
		{"nobar", "nobar", "", ""},
	}
	for _, c := range cases {
		g := ParseGateMeta(c.meta)
		if g.Challenger != c.challenger || g.Subject != c.subject || g.Text != c.txt {
			t.Errorf("ParseGateMeta(%q) = %+v", c.meta, g)
		}
	}
	if got := FormatGateMeta("master", "roles-impl", "review X"); got != "master|[subject roles-impl] review X" {
		t.Errorf("FormatGateMeta = %q", got)
	}
	if got := FormatGateMeta("master", "", "review X"); got != "master|review X" {
		t.Errorf("FormatGateMeta without subject = %q", got)
	}
	for _, bad := range []string{"a]b", "a b", "a\nb", ""} {
		if bad == "" {
			continue
		}
		if err := ValidGateSubject(bad); err == nil {
			t.Errorf("ValidGateSubject(%q) accepted", bad)
		}
	}
	if err := ValidGateSubject("pr:135"); err != nil {
		t.Errorf("ValidGateSubject(pr:135) = %v", err)
	}
}

// A reviewer's verdict closes the gates opened ON THAT REVIEWER for its
// subject or its ref, and nothing else: not another reviewer's gate, not a
// mission gate (the conductor closes its own), not a self-verdict.
func TestCloseReviewGates(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() {
		b.r.Del(ctx, GateKey(p, "foureyes"), GateKey(p, "ui-kimi"), StreamKey(p, "cmd"))
	})
	open := func(agent, ref, meta string) {
		t.Helper()
		if err := b.OpenChallenge(ctx, agent, ref, meta); err != nil {
			t.Fatal(err)
		}
	}
	open("foureyes", "s1", "master|[subject roles-impl] review roles")
	open("foureyes", "s2", "master|[subject other] review other")
	open("foureyes", "r1", "master|no subject stored")
	open("foureyes", "m1", "conductor|[subject roles-impl] mission review")
	open("ui-kimi", "k1", "master|[subject roles-impl] review roles")

	// r1 has no challenge on the cmd stream, so nothing says what was opened
	// before it: the verdict closes r1 alone (gate-subject-close).
	closed, err := b.CloseReviewGates(ctx, "foureyes", "coder", "roles-impl", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(closed) != 1 || closed[0] != "r1" {
		t.Fatalf("closed = %v, want [r1]", closed)
	}
	// Without a ref, the subject sweeps every gate opened for it.
	closed, err = b.CloseReviewGates(ctx, "foureyes", "coder", "roles-impl", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(closed) != 1 || closed[0] != "s1" {
		t.Fatalf("closed = %v, want [s1]", closed)
	}
	if m, _ := b.OpenChallenges(ctx, "foureyes"); len(m) != 2 || m["s2"] == "" || m["m1"] == "" {
		t.Fatalf("foureyes keeps %v, want s2 and m1", m)
	}
	if m, _ := b.OpenChallenges(ctx, "ui-kimi"); len(m) != 1 {
		t.Fatalf("another reviewer's gate closed: %v", m)
	}
	// A self-verdict (reviewer == author) closes nothing.
	if closed, _ := b.CloseReviewGates(ctx, "ui-kimi", "ui-kimi", "roles-impl", ""); len(closed) != 0 {
		t.Fatalf("self-verdict closed %v", closed)
	}
	// No subject match and no ref: nothing.
	if closed, _ := b.CloseReviewGates(ctx, "foureyes", "coder", "nothing", ""); len(closed) != 0 {
		t.Fatalf("closed %v", closed)
	}
}

// The live case (gate-subject-close): master challenged foureyes on a
// subject, then again on a newer delta of the same subject. foureyes' verdict
// on the older commit, naming its ref, must not close the newer delta's gate
// it has not read yet. It closes its ref and the same-subject gates opened
// before it; a gate whose challenge left the cmd stream is older than any
// retained one, so it goes too.
func TestCloseReviewGatesWithRefSparesNewerDeltas(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(ctx, GateKey(p, "foureyes"), StreamKey(p, "cmd")) })
	challenge := func(ref, subject string) {
		t.Helper()
		if err := b.OpenChallenge(ctx, "foureyes", ref, FormatGateMeta("master", subject, "review "+ref)); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Cmd(ctx, "master", "foureyes", CmdChallenge, ref, "review "+ref); err != nil {
			t.Fatal(err)
		}
	}
	// "trimmed": its challenge entry is gone from the stream (or never was).
	if err := b.OpenChallenge(ctx, "foureyes", "trimmed", FormatGateMeta("master", "gsc", "old")); err != nil {
		t.Fatal(err)
	}
	challenge("first", "gsc")
	challenge("other", "elsewhere")
	challenge("read", "gsc")
	challenge("delta", "gsc")

	closed, err := b.CloseReviewGates(ctx, "foureyes", "coder", "gsc", "read")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"first", "read", "trimmed"}; strings.Join(closed, " ") != strings.Join(want, " ") {
		t.Fatalf("closed = %v, want %v", closed, want)
	}
	m, _ := b.OpenChallenges(ctx, "foureyes")
	if len(m) != 2 || m["delta"] == "" || m["other"] == "" {
		t.Fatalf("foureyes keeps %v, want delta and other", m)
	}
}

// A gate re-opened under the same ref (a re-challenge on its thread
// overwrites the gate) holds the newer delta: it is dated by its last
// challenge, so a verdict anchored between the two leaves it open (foureyes'
// probe on PR #144).
func TestCloseReviewGatesDatesAReopenedGateByItsLastChallenge(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(ctx, GateKey(p, "foureyes"), StreamKey(p, "cmd")) })
	for i, ref := range []string{"old", "cur", "old"} {
		if err := b.OpenChallenge(ctx, "foureyes", ref, FormatGateMeta("master", "s", fmt.Sprintf("v%d", i+1))); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Cmd(ctx, "master", "foureyes", CmdChallenge, ref, fmt.Sprintf("v%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	closed, err := b.CloseReviewGates(ctx, "foureyes", "coder", "s", "cur")
	if err != nil {
		t.Fatal(err)
	}
	if len(closed) != 1 || closed[0] != "cur" {
		t.Fatalf("closed = %v, want [cur]: the re-opened old gate holds the unread v3", closed)
	}
}

// CloseGate deletes one gate only while it still holds the meta that was
// read, and leaves a wake=no reply on the gated agent's thread in the same
// script: an audit trace that is not a verdict.
func TestCloseGate(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(ctx, GateKey(p, "foureyes"), StreamKey(p, "cmd"), VerdictsKey(p)) })
	if err := b.OpenChallenge(ctx, "foureyes", "g1", "master|review it"); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.CloseGate(ctx, "foureyes", "g1", "master|changed", "master", "stale", false); err != nil || ok {
		t.Fatalf("CloseGate with a stale meta = %v, %v; want false", ok, err)
	}
	ok, err := b.CloseGate(ctx, "foureyes", "g1", "master|review it", "master", "stale, merged as #135", false)
	if err != nil || !ok {
		t.Fatalf("CloseGate = %v, %v", ok, err)
	}
	if m, _ := b.OpenChallenges(ctx, "foureyes"); len(m) != 0 {
		t.Fatalf("gate still open: %v", m)
	}
	evs, err := b.Thread(ctx, "g1")
	if err != nil || len(evs) != 1 {
		t.Fatalf("thread g1 = %v, %v", evs, err)
	}
	e := evs[0]
	if e.From != "master" || e.Target != "foureyes" || e.Type != CmdReply || !e.NoWake || e.Human {
		t.Fatalf("trace = %+v", e)
	}
	if e.Message != "gate g1 closed without a verdict by master: stale, merged as #135" {
		t.Fatalf("trace text = %q", e.Message)
	}
	if vs, _ := b.Verdicts(ctx, ""); len(vs) != 0 {
		t.Fatalf("closing a gate wrote a verdict: %v", vs)
	}
	// A person's close carries human=yes.
	if err := b.OpenChallenge(ctx, "foureyes", "g2", "master|other"); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.CloseGate(ctx, "foureyes", "g2", "master|other", "charlie", "not needed", true); err != nil || !ok {
		t.Fatalf("person CloseGate = %v, %v", ok, err)
	}
	if evs, _ := b.Thread(ctx, "g2"); len(evs) != 1 || !evs[0].Human {
		t.Fatalf("person trace = %+v", evs)
	}
}

func TestAllGates(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(ctx, GateKey(p, "foureyes"), GateKey(p, "ui-kimi")) })
	_ = b.OpenChallenge(ctx, "foureyes", "a", "master|x")
	_ = b.OpenChallenge(ctx, "ui-kimi", "b", "coder|y")
	all, err := b.AllGates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all["foureyes"]["a"] != "master|x" || all["ui-kimi"]["b"] != "coder|y" {
		t.Fatalf("AllGates = %v", all)
	}
}

// Only gates opened within the horizon hold an auto-clear (lifecycle part B,
// rule 4): the open time is the gate's latest challenge entry in :cmd; a gate
// whose challenge left the capped stream is older than everything retained,
// so it is stale only when the oldest retained entry is past the horizon.
func TestReviewsOfSinceRedis(t *testing.T) {
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour).UnixMilli()
	for name, c := range map[string]struct {
		challenges []string // refs whose challenge entry is retained, opened 48 h ago
		recent     []string // refs whose challenge is recent
		gates      []string // refs gating coder
		want       int
	}{
		"a recent gate holds":                       {recent: []string{"r1"}, gates: []string{"r1"}, want: 1},
		"a gate opened two days ago does not":       {challenges: []string{"o1"}, gates: []string{"o1"}, want: 0},
		"a trimmed gate behind an old stream":       {challenges: []string{"x"}, gates: []string{"gone"}, want: 0},
		"a trimmed gate, the stream all recent":     {recent: []string{"x"}, gates: []string{"gone"}, want: 1},
		"a re-opened gate is dated by its last one": {challenges: []string{"re"}, recent: []string{"re"}, gates: []string{"re"}, want: 1},
	} {
		t.Run(name, func(t *testing.T) {
			b := dialTest(t)
			key := StreamKey(b.project, "cmd")
			seq := 0
			add := func(ms int64, ref string) {
				seq++
				if err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: key, ID: fmt.Sprintf("%d-%d", ms, seq),
					Values: map[string]any{"from": "foureyes", "target": "coder", "type": CmdChallenge, "ref": ref, "command": "review"}}).Err(); err != nil {
					t.Fatal(err)
				}
			}
			for _, r := range c.challenges {
				add(old, r)
			}
			for _, r := range c.recent {
				add(time.Now().UnixMilli(), r)
			}
			for _, r := range c.gates {
				if err := b.OpenChallenge(ctx, "coder", r, "foureyes|review"); err != nil {
					t.Fatal(err)
				}
			}
			got, err := b.ReviewsOfSince(ctx, "coder", DefaultHorizon)
			if err != nil || len(got) != c.want {
				t.Fatalf("ReviewsOfSince = %v %v, want %d", got, err, c.want)
			}
			all, _ := b.ReviewsOf(ctx, "coder")
			if len(all) != len(c.gates) {
				t.Fatalf("ReviewsOf changed: %v", all)
			}
		})
	}
}
