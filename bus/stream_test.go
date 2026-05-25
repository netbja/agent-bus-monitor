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
