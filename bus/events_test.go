package bus

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// {p}:events (spec 2026-11-10-herdr-exit-design.md 6.5): one entry per
// terminal-lifecycle fact, {kind, agent, term, by, detail}, its time the
// entry id; a timeline, never a source of truth.
func TestAppendEventAndRecent(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	for _, e := range []TermEvent{
		{Kind: EventWakeTyped, Agent: "coder", Term: "t1", By: "dispatch"},
		{Kind: EventBlocked, Agent: "coder", Term: "t1", By: "dispatch", Detail: "screen: \"touch ap1\""},
		{Kind: EventAttach, Agent: "coder", Term: "t1", By: "person", Detail: "observer"},
	} {
		if _, err := b.AppendEvent(ctx, e); err != nil {
			t.Fatalf("%+v: %v", e, err)
		}
	}
	got, err := b.RecentEvents(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != EventAttach || got[1].Kind != EventBlocked || got[1].Detail != "screen: \"touch ap1\"" ||
		got[0].By != "person" || got[0].Term != "t1" || got[0].Agent != "coder" || got[0].At.IsZero() || got[0].ID == "" {
		t.Fatalf("recent %+v", got)
	}
	all, _ := b.RecentEvents(ctx, 50)
	if len(all) != 3 || all[2].Kind != EventWakeTyped {
		t.Fatalf("all %+v", all)
	}
}

// What AppendEvent refuses, and what it cleans: an unknown kind, an agent or
// term that is no name; the detail is the agent's output at times, so one
// line, control characters dropped, capped.
func TestAppendEventValidates(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	for name, e := range map[string]TermEvent{
		"unknown kind": {Kind: "party", Agent: "coder"},
		"no kind":      {Agent: "coder"},
		"bad agent":    {Kind: EventRelaunch, Agent: "Bad Agent"},
		"bad term":     {Kind: EventRelaunch, Term: "t 1"},
		"bad by":       {Kind: EventRelaunch, By: "a\nb"},
	} {
		if _, err := b.AppendEvent(ctx, e); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := b.AppendEvent(ctx, TermEvent{Kind: EventHolderGC}); err != nil {
		t.Fatalf("an event of no agent: %v", err)
	}
	long := "line one\nline\x1b[31m two\x07" + strings.Repeat("x", 1000)
	if _, err := b.AppendEvent(ctx, TermEvent{Kind: EventDraftWaiting, Agent: "coder", Detail: long}); err != nil {
		t.Fatal(err)
	}
	got, _ := b.RecentEvents(ctx, 1)
	if d := got[0].Detail; strings.ContainsAny(d, "\n\x1b\x07") || len([]rune(d)) > eventDetailMax || !strings.HasPrefix(d, "line one line[31m two") {
		t.Fatalf("detail %q", d)
	}
}

// --reset trims {p}:events with the activity streams and keeps {p}:resume
// (a reset trims activity, it must not forget how to resume agents).
func TestResetTrimsEventsAndKeepsResume(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	b.AppendEvent(ctx, TermEvent{Kind: EventRelaunch, Agent: "coder"})
	b.r.HSet(ctx, b.project+":resume", "coder", `{"argv":["agent-launch"],"session":"0f1e2d3c-aaaa","at":1}`)
	if _, err := b.Purge(ctx, ResetKinds); err != nil {
		t.Fatal(err)
	}
	if n, _ := b.r.XLen(ctx, EventsKey(b.project)).Result(); n != 0 {
		t.Fatalf("events kept %d", n)
	}
	if n, _ := b.r.HLen(ctx, b.project+":resume").Result(); n != 1 {
		t.Fatal("reset forgot the resume record")
	}
}

// The guard (spec 6.5): every key kind of a project is either reset or
// survives a reset, named on purpose, so the next key cannot be forgotten
// the way events once could. Only streams can be reset (XTRIM).
func TestEveryProjectKindIsResetOrSurvives(t *testing.T) {
	for kind := range projectKinds {
		if slices.Contains(ResetKinds, kind) == slices.Contains(SurvivesReset, kind) {
			t.Errorf("%s: in both lists or in neither", kind)
		}
	}
	for _, kind := range ResetKinds {
		if !projectKinds[kind] {
			t.Errorf("%s is reset but no project kind", kind)
		}
	}
	for _, k := range []string{"status", "report", "notify", "cmd", "events"} {
		if !slices.Contains(ResetKinds, k) {
			t.Errorf("%s not reset", k)
		}
	}
	if !slices.Contains(SurvivesReset, "resume") {
		t.Error("resume does not survive a reset")
	}
	if slices.Contains(activityKinds, "events") {
		t.Error("an event dates the project as active")
	}
	if c, kind := ClassOf("demo", EventsKey("demo")); c != KeyRecord || kind != "events" {
		t.Errorf("events key class %v %q", c, kind)
	}
}

// Persistence reads CONFIG GET appendonly and save.
func TestPersistenceReadsTheConfig(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	aof, save, err := b.Persistence(ctx)
	if err != nil || (aof != "yes" && aof != "no") {
		t.Fatalf("persistence %q %q %v", aof, save, err)
	}
	oldSave := save
	t.Cleanup(func() { b.r.ConfigSet(context.Background(), "save", oldSave) })
	if err := b.r.ConfigSet(ctx, "save", "").Err(); err != nil {
		t.Skipf("CONFIG SET refused by this broker: %v", err)
	}
	if _, save, _ := b.Persistence(ctx); save != "" {
		t.Fatalf("save %q after clearing it", save)
	}
}

// A broker that refuses CONFIG answers ErrConfigRefused; a broker that cannot
// be reached answers its own error, never a refusal (reviewer-codex).
func TestPersistenceTellsARefusalFromAFailure(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if err := b.r.Do(ctx, "ACL", "SETUSER", "noconfig", "on", ">pw", "~*", "&*", "+@all", "-config").Err(); err != nil {
		t.Skipf("ACL refused by this broker: %v", err)
	}
	t.Cleanup(func() { b.r.Do(context.Background(), "ACL", "DELUSER", "noconfig") })
	opt := b.r.Options()
	limited := redis.NewClient(&redis.Options{Network: opt.Network, Addr: opt.Addr, DB: opt.DB, Username: "noconfig", Password: "pw"})
	defer limited.Close()
	lb, err := Open(limited, b.project)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := lb.Persistence(ctx); !errors.Is(err, ErrConfigRefused) {
		t.Fatalf("a refused CONFIG: %v", err)
	}
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := b.Persistence(gone); err == nil || errors.Is(err, ErrConfigRefused) {
		t.Fatalf("a failed read read as a refusal: %v", err)
	}
}

// replyErr is a server error reply, as go-redis types one.
type replyErr string

func (e replyErr) Error() string { return string(e) }
func (replyErr) RedisError()     {}

// Only the replies that mean "CONFIG is not for you" are a refusal: an
// unknown or renamed command, NOPERM. A broker loading its dataset or busy
// running a script answers an error the check must see (reviewer-codex).
func TestConfigErrClassifiesOnlyRefusals(t *testing.T) {
	for reply, refused := range map[string]bool{
		"NOPERM User noconfig has no permissions to run the 'config|get' command": true,
		"ERR unknown command 'CONFIG', with args beginning with: 'GET' ":          true,
		"ERR unknown subcommand 'GET'. Try CONFIG HELP.":                          true,
		"LOADING Redis is loading the dataset in memory":                          false,
		"BUSY Redis is busy running a script. You can only call SCRIPT KILL":      false,
		"MASTERDOWN Link with MASTER is down":                                     false,
		"ERR max number of clients reached":                                       false,
	} {
		err := configErr(replyErr(reply))
		if errors.Is(err, ErrConfigRefused) != refused {
			t.Errorf("%q: refused=%v, want %v", reply, !refused, refused)
		}
		if !refused && err.Error() != reply {
			t.Errorf("%q passed on as %q", reply, err)
		}
	}
	if err := configErr(context.Canceled); errors.Is(err, ErrConfigRefused) {
		t.Error("a context error read as a refusal")
	}
}
