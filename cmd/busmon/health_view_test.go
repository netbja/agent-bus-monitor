package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	guardian "github.com/netbja/agent-bus-monitor/health"
)

func TestGuardianSummaryNeverCallsUnknownHealthy(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	n := len(guardian.Definitions())
	if got := summarizeGuardian(nil, now); got.Unmeasured != n || got.Fine != 0 {
		t.Fatalf("empty records summarized as %+v", got)
	}
	if got := guardianSeg(summarizeGuardian(nil, now), 60).text(); got != "?"+strconv.Itoa(n) { // unknown only: "?", not a problem count
		t.Fatalf("unknown health pill=%q", got)
	}
	rows := allOK(now)
	if got := summarizeGuardian(rows, now); got.Fine != n || got.Unmeasured != 0 {
		t.Fatalf("healthy records summarized as %+v", got)
	}
	if got := guardianSeg(summarizeGuardian(rows, now), 60).text(); got != GlyphDone {
		t.Fatalf("healthy health pill=%q", got)
	}
	rows["services"] = guardian.Record{ID: "services", Status: guardian.OK, ObservedAt: now.Add(-3 * time.Minute).UnixMilli()}
	if got := summarizeGuardian(rows, now); got.Unmeasured != 1 || got.Fine != n-1 {
		t.Fatalf("stale record summarized as %+v", got)
	}
}

func allOK(now time.Time) map[string]guardian.Record {
	rows := map[string]guardian.Record{}
	for _, d := range guardian.Definitions() {
		rows[d.ID] = guardian.Record{ID: d.ID, Status: guardian.OK, ObservedAt: now.UnixMilli()}
	}
	return rows
}

// The header counts only what the person must do (Bernard: "in hand" is not
// counted there): to do first, then not measured, then in hand without a
// number, and the tick only when every check is measured ok.
func TestHealthPillSaysWhatThePersonMustDo(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	rec := func(id string, s guardian.Status) guardian.Record {
		return guardian.Record{ID: id, Status: s, ObservedAt: now.UnixMilli(), Since: now.UnixMilli()}
	}
	rows := allOK(now)
	rows["context"] = rec("context", guardian.Warn) // in hand
	if got := guardianSeg(summarizeGuardian(rows, now), wideMinWidth); got.text() != "health "+GlyphWorking || got[0].Role != RoleMuted {
		t.Errorf("in hand only: pill %q role %v", got.text(), got[0].Role)
	}
	delete(rows, "model-drift") // not measured wins over in hand
	if got := guardianSeg(summarizeGuardian(rows, now), wideMinWidth).text(); got != "health ?1" {
		t.Errorf("unmeasured: pill %q", got)
	}
	rows["redis-version"] = rec("redis-version", guardian.Warn) // for you
	rows["redis-password"] = rec("redis-password", guardian.Warn)
	s := summarizeGuardian(rows, now)
	if s.ToDo != 2 || s.InHand != 1 || s.Unmeasured != 1 {
		t.Fatalf("summary %+v", s)
	}
	for w, want := range map[int]string{wideMinWidth: "health " + GlyphBlocked + " 2 to do (F5)", 100: "health " + GlyphBlocked + "2", 60: GlyphBlocked + "2"} {
		if got := guardianSeg(s, w); got.text() != want || got[0].Role != RoleAttention {
			t.Errorf("width %d: pill %q role %v, want %q", w, got.text(), got[0].Role, want)
		}
	}
	rows["tracked-secrets"] = rec("tracked-secrets", guardian.Fail)
	if got := guardianSeg(summarizeGuardian(rows, now), wideMinWidth); got.text() != "health "+GlyphBlocked+" 3 to do (F5)" || got[0].Role != RoleError {
		t.Errorf("a failing to-do: pill %q role %v", got.text(), got[0].Role)
	}
	rows = allOK(now)
	rows["services"] = rec("services", guardian.Fail) // in hand, even failing
	if got := guardianSeg(summarizeGuardian(rows, now), wideMinWidth).text(); got != "health "+GlyphWorking {
		t.Errorf("failing service in hand: pill %q", got)
	}
}

func TestHealthOverlayShowsGuidanceAndEscapesText(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	body := healthOverlayText(map[string]guardian.Record{"tracked-secrets": {ID: "tracked-secrets", Status: guardian.Fail, Detail: "tracked private-key path: [candidate]", ObservedAt: now.UnixMilli(), Since: now.UnixMilli()}},
		map[string]guardian.Note{"tracked-secrets": {ID: "tracked-secrets", Author: "sentinel", Text: "removed from tracking"}}, now)
	if !strings.Contains(plainOf(body), "Tracked secret-like files") || !strings.Contains(plainOf(body), "remediation:") || !strings.Contains(plainOf(body), "removed from tracking") {
		t.Fatalf("health overlay missing fields: %q", plainOf(body))
	}
	if !strings.Contains(drawnText(body), "[candidate]") {
		t.Fatalf("health detail injected a tview tag: %q", drawnText(body))
	}
}

func TestHealthTransitionsAppearInRecap(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	rows := healthRecapRows([]guardian.Transition{{ID: "services", Previous: guardian.OK, Status: guardian.Fail, At: now.UnixMilli(), StreamID: "1-0"}})
	if len(rows) != 1 || !strings.Contains(rows[0].Title, "ok → fail") {
		t.Fatalf("health recap rows = %+v", rows)
	}
}

func TestHealthPillFitsSupportedHeaderWidths(t *testing.T) {
	now := time.Now()
	for _, width := range []int{30, 40, 60, 79, 80, 100, 119, 120, 160} {
		tier := TierNarrow
		if width >= 120 {
			tier = TierWide
		} else if width >= 80 {
			tier = TierMedium
		}
		for _, g := range []guardianSummary{{Unmeasured: 13, Total: 13}, {ToDo: 2, Unmeasured: 11, Total: 13}} {
			want := "?13"
			if g.ToDo > 0 {
				want = GlyphBlocked + "2"
				if width >= wideMinWidth {
					want = GlyphBlocked + " 2 to do"
				}
			}
			lines := headerLines(headerData{project: "demo", health: health{ok: true}, guardian: g, dispatch: "down", now: now}, width, tier)
			if len(lines) == 0 || !strings.Contains(lines[0].text(), want) {
				t.Errorf("width %d lost health pill %q: %q", width, want, lines[0].text())
			}
		}
	}
}
