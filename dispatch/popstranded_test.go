package dispatch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// board-drift §6 (step 1): a pop that is closed (no terminal, its process
// gone) but kept because it owns open requests tells master once per
// episode, with the line that brings it back; the dispatcher never
// relaunches it (the person's Q4).

func TestPopStrandedTellsMasterOncePerEpisode(t *testing.T) {
	ctx := context.Background()
	w := newPopWorld(t, "pop")
	if _, err := w.b.Request(ctx, "board-drift-spec", "master", "security", "", "write it", time.Hour); err != nil {
		t.Fatal(err)
	}
	session := "c2bf3f3b-1111-2222-3333-444455556666"
	if err := w.r.HSet(ctx, bus.ResumeKey(w.b.Project()), "security", `{"session":"`+session+`","profile":"codex","at":1}`).Err(); err != nil {
		t.Fatal(err)
	}
	w.closePop()
	w.pass()
	w.pass()
	got := w.directives()
	want := "agentbus --project " + w.b.Project() + " pop security --resume " + session
	if len(got) != 1 || !strings.HasPrefix(got[0], "|") || !strings.Contains(got[0], "security has no terminal and owns open requests: board-drift-spec (requested)") ||
		!strings.Contains(got[0], "Bring it back: "+want) {
		t.Fatalf("directives %q, want one waking notice with %q", got, want)
	}
	marks, _ := w.r.HGetAll(ctx, bus.QuotaKey(w.b.Project())).Result()
	if marks[bus.PopStrandedMark("security")] == "" {
		t.Fatalf("no mark: %v", marks)
	}
}

// Without a resume record, the line is plain pop <role>.
func TestPopStrandedWithoutASession(t *testing.T) {
	ctx := context.Background()
	w := newPopWorld(t, "pop")
	if _, err := w.b.Request(ctx, "t1", "master", "security", "", "x", time.Hour); err != nil {
		t.Fatal(err)
	}
	w.closePop()
	w.pass()
	got := w.directives()
	if len(got) != 1 || !strings.HasSuffix(got[0], "Bring it back: agentbus --project "+w.b.Project()+" pop security") {
		t.Fatalf("directives %q", got)
	}
}

// A live pop that owns a request, and a closed pop that owns nothing (the
// pop-done notice is the one it gets), are not told stranded.
func TestPopStrandedNotForALivePopOrOneOwningNothing(t *testing.T) {
	ctx := context.Background()
	w := newPopWorld(t, "pop")
	if _, err := w.b.Request(ctx, "t1", "master", "security", "", "x", time.Hour); err != nil {
		t.Fatal(err)
	}
	w.pass() // its pane runs, its process lives: not closed
	if got := w.directives(); len(got) != 0 {
		t.Fatalf("a live pop: %q", got)
	}
	w = newPopWorld(t, "pop")
	w.closePop()
	w.pass()
	for _, d := range w.directives() {
		if strings.Contains(d, "owns open requests") {
			t.Fatalf("a closed pop owning nothing: %q", d)
		}
	}
}
