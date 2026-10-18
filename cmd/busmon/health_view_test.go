package main

import (
	"strings"
	"testing"
	"time"

	guardian "github.com/netbja/agent-bus-monitor/health"
)

func TestGuardianSummaryNeverCallsUnknownHealthy(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	if got := summarizeGuardian(nil, now); got.Unhealthy != got.Total || got.Worst != guardian.Unknown {
		t.Fatalf("empty records summarized as %+v", got)
	}
	if got := guardianSeg(summarizeGuardian(nil, now)).text(); got != "!13" {
		t.Fatalf("unknown health pill=%q", got)
	}
	rows := map[string]guardian.Record{}
	for _, d := range guardian.Definitions() {
		rows[d.ID] = guardian.Record{ID: d.ID, Status: guardian.OK, ObservedAt: now.UnixMilli()}
	}
	if got := summarizeGuardian(rows, now); got.Unhealthy != 0 || got.Worst != guardian.OK {
		t.Fatalf("healthy records summarized as %+v", got)
	}
	if got := guardianSeg(summarizeGuardian(rows, now)).text(); got != GlyphDone {
		t.Fatalf("healthy health pill=%q", got)
	}
	rows["services"] = guardian.Record{ID: "services", Status: guardian.OK, ObservedAt: now.Add(-3 * time.Minute).UnixMilli()}
	if got := summarizeGuardian(rows, now); got.Unhealthy == 0 || got.Worst != guardian.Unknown {
		t.Fatalf("stale record summarized as %+v", got)
	}
}

func TestHealthOverlayShowsGuidanceAndEscapesText(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	body := healthOverlayText(map[string]guardian.Record{"tracked-secrets": {ID: "tracked-secrets", Status: guardian.Fail, Detail: "tracked private-key path: [candidate]", ObservedAt: now.UnixMilli(), Since: now.UnixMilli()}},
		map[string]guardian.Note{"tracked-secrets": {ID: "tracked-secrets", Author: "sentinel", Text: "removed from tracking"}}, now)
	if !strings.Contains(body, "Tracked secret-like files") || !strings.Contains(body, "remediation:") || !strings.Contains(body, "removed from tracking") {
		t.Fatalf("health overlay missing fields: %q", body)
	}
	if strings.Contains(body, "[candidate]") {
		t.Fatalf("health detail injected a tview tag: %q", body)
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
		lines := headerLines(headerData{project: "demo", health: health{ok: true}, guardian: guardianSummary{Unhealthy: 13, Total: 13, Worst: guardian.Unknown}, dispatch: "down", now: now}, width, tier)
		if len(lines) == 0 || !strings.Contains(lines[0].text(), "!13") {
			t.Errorf("width %d lost health pill: %q", width, lines[0].text())
		}
	}
}
