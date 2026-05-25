package bus

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestStreamKeyNaming(t *testing.T) {
	if got := StreamKey("busmon", "status"); got != "busmon:status" {
		t.Fatalf("StreamKey = %q, want busmon:status", got)
	}
	if got := PilotKey("busmon"); got != "busmon:pilot" {
		t.Fatalf("PilotKey = %q, want busmon:pilot", got)
	}
	if got := GateKey("busmon", "dev"); got != "busmon:gate:dev" {
		t.Fatalf("GateKey = %q, want busmon:gate:dev", got)
	}
}

func TestValidName(t *testing.T) {
	valid := []string{"dev", "busmon", "dev-1", "hermes", "x", "a_b-2"}
	invalid := []string{"", "Dev", "1dev", "a:b", "dev ", "trading/dev",
		"this-name-is-way-too-long-to-be-accepted-okay"}
	for _, s := range valid {
		if !ValidName(s) {
			t.Errorf("ValidName(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if ValidName(s) {
			t.Errorf("ValidName(%q) = true, want false", s)
		}
	}
}

func TestParseEntry(t *testing.T) {
	cases := []struct {
		name, stream string
		fields       map[string]string
		want         Event
	}{
		{"status", "busmon:status",
			map[string]string{"agent": "dev", "state": "working", "message": "hi"},
			Event{ID: "1-0", Project: "busmon", Kind: "status", Agent: "dev", State: "working", Message: "hi"}},
		{"report", "busmon:report",
			map[string]string{"agent": "dev", "kind": "note", "message": "bug fixed"},
			Event{ID: "1-0", Project: "busmon", Kind: "report", Agent: "dev", RKind: "note", Message: "bug fixed"}},
		{"notify", "trading:notify",
			map[string]string{"from": "hermes", "message": "soak running"},
			Event{ID: "1-0", Project: "trading", Kind: "notify", From: "hermes", Message: "soak running"}},
		{"cmd", "busmon:cmd",
			map[string]string{"from": "review", "target": "dev", "type": "challenge", "ref": "C1", "command": "justify X"},
			Event{ID: "1-0", Project: "busmon", Kind: "cmd", From: "review", Target: "dev", Type: "challenge", Ref: "C1", Message: "justify X"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseEntry(c.stream, "1-0", c.fields); got != c.want {
				t.Fatalf("ParseEntry = %+v, want %+v", got, c.want)
			}
		})
	}
}

// dialTest connects to the dev broker and returns a Bus on a unique throwaway
// project; it skips the test if Redis is down. All four streams + the pilot key
// are deleted on cleanup (gate keys are per-agent — tests clean their own).
func dialTest(t *testing.T) *Bus {
	t.Helper()
	r, err := Connect("")
	if err != nil {
		t.Skipf("Redis unavailable (run docker compose up -d): %v", err)
	}
	project := "t" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, err := Open(r, project)
	if err != nil {
		t.Fatalf("Open(%q): %v", project, err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		r.Del(ctx, StreamKey(project, "status"), StreamKey(project, "report"),
			StreamKey(project, "notify"), StreamKey(project, "cmd"), PilotKey(project))
		r.Close()
	})
	return b
}

func TestOpenRejectsBadProject(t *testing.T) {
	// nil client is intentional: Open validates the project before it ever
	// touches the client, so a bad project must error without dialing Redis.
	if _, err := Open(nil, "Bad:Project"); err == nil {
		t.Fatal("Open accepted an invalid project, want error")
	}
}

func TestPublishValidation(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Status(ctx, "dev", "flying", "x"); err == nil {
		t.Error("Status accepted invalid state, want error")
	}
	if _, err := b.Status(ctx, "Bad Agent", "working", "x"); err == nil {
		t.Error("Status accepted invalid agent, want error")
	}
	if _, err := b.Cmd(ctx, "hermes", "dev", "shout", "", "x"); err == nil {
		t.Error("Cmd accepted invalid type, want error")
	}
	if _, err := b.Status(ctx, "dev", "working", "ok"); err != nil {
		t.Errorf("valid Status failed: %v", err)
	}
}

func TestTailRoundTrip(t *testing.T) {
	b := dialTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := b.Status(ctx, "dev", "working", "hello"); err != nil {
		t.Fatalf("Status: %v", err)
	}

	got := make(chan Event, 1)
	go func() {
		_ = b.Tail(ctx, "0", []string{"status"}, func(e Event) {
			select {
			case got <- e:
			default:
			}
		})
	}()

	select {
	case e := <-got:
		if e.Kind != "status" || e.Agent != "dev" || e.State != "working" || e.Message != "hello" {
			t.Fatalf("Tail event = %+v, want status/dev/working/hello", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Tail produced no event within 3s")
	}
}

func TestPilotLease(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()

	if d, err := b.PilotDriver(ctx); err != nil || d != "" {
		t.Fatalf("PilotDriver before claim = (%q, %v), want (\"\", nil)", d, err)
	}
	if err := b.Pilot(ctx, "hermes", 90*time.Second); err != nil {
		t.Fatalf("Pilot: %v", err)
	}
	if d, err := b.PilotDriver(ctx); err != nil || d != "hermes" {
		t.Fatalf("PilotDriver after claim = (%q, %v), want (\"hermes\", nil)", d, err)
	}
	if ttl := b.r.TTL(ctx, PilotKey(b.Project())).Val(); ttl <= 0 {
		t.Fatalf("pilot key TTL = %v, want > 0 (lease must expire)", ttl)
	}
	if err := b.ReleasePilot(ctx); err != nil {
		t.Fatalf("ReleasePilot: %v", err)
	}
	if d, err := b.PilotDriver(ctx); err != nil || d != "" {
		t.Fatalf("PilotDriver after release = (%q, %v), want (\"\", nil)", d, err)
	}
}

func TestWatchCmdDelivers(t *testing.T) {
	b := dialTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := StreamKey(b.Project(), "cmd")
	// Pre-create dev's group at "0" so the test deterministically replays the
	// entries published next (WatchCmd's own MKSTREAM at "$" is then a no-op).
	if err := b.r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	if _, err := b.Cmd(ctx, "hermes", "other", CmdDirective, "", "not for dev"); err != nil {
		t.Fatalf("Cmd other: %v", err)
	}
	if _, err := b.Cmd(ctx, "review", "dev", CmdChallenge, "C1", "justify X"); err != nil {
		t.Fatalf("Cmd dev: %v", err)
	}

	got := make(chan Event, 1)
	go func() {
		_ = b.WatchCmd(ctx, "dev", "test-consumer", func(e Event) bool {
			got <- e
			return true // one-shot: stop on first entry addressed to dev
		})
	}()

	select {
	case e := <-got:
		if e.Target != "dev" || e.Type != CmdChallenge || e.Ref != "C1" || e.Message != "justify X" {
			t.Fatalf("WatchCmd delivered %+v, want the dev/challenge/C1 entry", e)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("WatchCmd delivered nothing for dev within 4s")
	}
}
