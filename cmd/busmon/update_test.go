package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLatestReleaseChecksOncePerDay(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"tag_name":"v0.12.0"}`))
	}))
	defer srv.Close()
	cache := filepath.Join(t.TempDir(), "agentbus", "latest.json")
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	if tag := latestRelease(context.Background(), srv.URL, cache, now); tag != "v0.12.0" {
		t.Fatalf("tag = %q", tag)
	}
	if tag := latestRelease(context.Background(), srv.URL, cache, now.Add(23*time.Hour)); tag != "v0.12.0" || hits.Load() != 1 {
		t.Fatalf("within 24h: tag %q, %d hits (want the cache)", tag, hits.Load())
	}
	latestRelease(context.Background(), srv.URL, cache, now.Add(25*time.Hour))
	if hits.Load() != 2 {
		t.Fatalf("%d hits after 25h, want a fresh check", hits.Load())
	}
}

func TestLatestReleaseFailsSilentlyAndWaits(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	cache := filepath.Join(t.TempDir(), "latest.json")
	now := time.Now()
	if tag := latestRelease(context.Background(), srv.URL, cache, now); tag != "" {
		t.Fatalf("a failure produced %q", tag)
	}
	latestRelease(context.Background(), srv.URL, cache, now.Add(time.Hour))
	if hits.Load() != 1 {
		t.Fatalf("a failure was retried within the day (%d hits)", hits.Load())
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatal("the failed check was not recorded")
	}
}

func TestUpdateNoticeGate(t *testing.T) {
	for _, c := range []struct {
		version, latest, optOut, want string
	}{
		{"v0.11.0", "v0.11.1", "", "v0.11.1"},
		{"v0.11.1", "v0.11.1", "", ""},
		{"v0.11.0", "v0.11.1", "1", ""},
		{"dev", "v9.0.0", "", ""},
	} {
		if got := updateNotice(c.version, c.latest, c.optOut); got != c.want {
			t.Errorf("updateNotice(%q, %q, opt-out %q) = %q, want %q", c.version, c.latest, c.optOut, got, c.want)
		}
	}
}

func TestHeaderShowsTheUpdateLast(t *testing.T) {
	now := time.Now()
	text := func(width int, tier Tier) string {
		var s string
		for _, l := range headerLines(headerData{project: "demo", self: "charlie", update: "v0.11.1", now: now, health: health{ok: true, since: now}}, width, tier) {
			for _, seg := range l {
				s += seg.Text
			}
		}
		return s
	}
	if got := text(160, TierWide); !strings.Contains(got, GlyphUp+" v0.11.1") {
		t.Fatalf("wide header %q lacks the update", got)
	}
	if got := text(40, TierNarrow); strings.Contains(got, "v0.11.1") && !strings.Contains(got, "Charlie") {
		t.Fatalf("narrow header kept the update over the person: %q", got)
	}
}
