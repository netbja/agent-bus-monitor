package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDueAttentionHonoursThresholdAndSnooze(t *testing.T) {
	now := time.Now()
	items := []attentionItem{{Key: "wait:master:1", At: now.Add(-6 * time.Minute)}}
	cache := &busmonCache{Snoozes: map[string]int64{}}
	if got := dueAttention(items, cache, now, 5*time.Minute); len(got) != 1 {
		t.Fatalf("due reminders %v, want one", got)
	}
	cache.snooze(items[0].Key, now.Add(15*time.Minute))
	if got := dueAttention(items, cache, now, 5*time.Minute); len(got) != 0 {
		t.Fatalf("snoozed reminders %v", got)
	}
	if got := dueAttention(items, cache, now.Add(16*time.Minute), 5*time.Minute); len(got) != 1 {
		t.Fatalf("expired snooze reminders %v, want one", got)
	}
	if got := dueAttention(items, cache, now, 0); len(got) != 0 {
		t.Fatalf("disabled popup reminders %v", got)
	}
}

func TestBusmonCacheWritesAtomicallyWith0600Mode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "demo.json")
	c := &busmonCache{Project: "demo", LastVisit: 123, Snoozes: map[string]int64{"x": 456}}
	if err := c.save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("cache mode %o, want 600", info.Mode().Perm())
	}
	if _, err := loadBusmonCache(path, "demo"); err != nil {
		t.Fatal(err)
	}
}
