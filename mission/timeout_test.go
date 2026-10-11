package mission

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

const fourHours = int64(4 * time.Hour / time.Millisecond)

func (h *harness) acceptedAt(task string) int64 {
	h.t.Helper()
	board, _ := h.b.Board(h.ctx)
	return board[task].Request.AcceptedAt
}

func (h *harness) clockAt(ms int64) {
	h.c.Now = func(context.Context) (int64, error) { return ms, nil }
}

func TestTimeoutBlocksAndEscalates(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.b.AcceptRequest(h.ctx, "chg-t1-1", "kimi")
	h.clockAt(h.acceptedAt("chg-t1-1") + fourHours - 1)
	h.pass()
	if m := h.mission("t1"); m.State != Accepted {
		t.Fatalf("before the deadline: %s", m.State)
	}
	h.clockAt(h.acceptedAt("chg-t1-1") + fourHours + 1)
	h.pass()
	h.pass()
	if m := h.mission("t1"); m.State != Blocked || m.Reason != "no delivery within 4h0m0s of acceptance" {
		t.Fatalf("after the deadline: %+v", m)
	}
	if n := countCmds(h.cmds(), func(e bus.Event) bool { return e.Type == bus.CmdEscalation }); n != 1 {
		t.Fatalf("%d escalations", n)
	}
}

func TestLateDeliveryTimesOut(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.deliver("t1", map[string]string{"src/t1/a.go": "x"})
	key := bus.BoardKey(h.b.Project())
	raw := h.r.HGet(h.ctx, key, "chg-t1-1").Val()
	var e map[string]any
	json.Unmarshal([]byte(raw), &e)
	req := e["request"].(map[string]any)
	req["responded_at"] = req["accepted_at"].(float64) + float64(fourHours) + 1
	out, _ := json.Marshal(e)
	h.r.HSet(h.ctx, key, "chg-t1-1", string(out))
	h.pass()
	m := h.mission("t1")
	if m.State != Blocked || m.LogPath != "" {
		t.Fatalf("a late delivery was checked: %+v", m)
	}
	if _, err := os.Stat(h.c.ProjectRoot + "/chg/logs/chg-t1-1.log"); err == nil {
		t.Fatal("the late delivery's tests ran")
	}
}

func TestTimeoutMeasuredPerAttempt(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.b.AcceptRequest(h.ctx, "chg-t1-1", "kimi")
	h.b.BlockRequest(h.ctx, "chg-t1-1", "kimi", "stuck")
	h.pass()
	first := h.acceptedAt("chg-t1-1")
	h.op("retry", "chg/t1")
	time.Sleep(20 * time.Millisecond) // distinct Redis TIME for the second acceptance
	h.b.AcceptRequest(h.ctx, "chg-t1-2", "kimi")
	second := h.acceptedAt("chg-t1-2")
	if second <= first {
		t.Fatalf("acceptance times %d %d", first, second)
	}
	h.clockAt(first + fourHours + 1)
	if second > first+1 {
		h.pass()
		if m := h.mission("t1"); m.State != Accepted {
			t.Fatalf("timed out from attempt 1's acceptance: %s", m.State)
		}
	}
	h.clockAt(second + fourHours + 1)
	h.pass()
	if m := h.mission("t1"); m.State != Blocked {
		t.Fatalf("attempt 2 did not time out: %s", m.State)
	}
}
