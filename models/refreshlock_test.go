package models

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRefreshModelsUnderLockWritesPrivateCacheAndSkipsFreshRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentbus", "json")
	now := time.Now()
	reads := 0
	reader := Reader{Provider: "openai", Read: func(context.Context) ([]Row, string, error) {
		reads++
		return []Row{{Provider: "openai", Client: "codex", ID: "gpt-test"}}, "test", nil
	}}
	if err := RefreshUnderLock(context.Background(), path, DefaultTiers(), []Reader{reader}, now); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("reader calls=%d", reads)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("cache mode=%o", st.Mode().Perm())
	}
	if err := RefreshUnderLock(context.Background(), path, DefaultTiers(), []Reader{reader}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("fresh cache triggered another read: %d", reads)
	}
}

func TestRefreshModelsUnderLockSkipsConcurrentHostRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentbus", "json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	reads := 0
	reader := Reader{Provider: "openai", Read: func(context.Context) ([]Row, string, error) {
		reads++
		return []Row{{ID: "gpt-test"}}, "test", nil
	}}
	if err := RefreshUnderLock(context.Background(), path, DefaultTiers(), []Reader{reader}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if reads != 0 {
		t.Fatalf("refresh ran while host lock was held: %d", reads)
	}
}

// foureyes (step 2, P2): a budget overrun still saves what was read: the
// providers that answered keep their new rows, and the attempt time moves,
// so the next `agentbus models` does not pay the budget again.
func TestRefreshTableSavesWhatWasReadOnOverrun(t *testing.T) {
	now := time.Now()
	cache := filepath.Join(t.TempDir(), "models.json")
	at := now.Add(-25 * time.Hour).UnixMilli() // a day old: due
	tbl := &Table{Providers: map[string]*Provider{
		"openai": {OK: true, At: at, ReadAt: at, Rows: []Row{{ID: "gpt-6.1-sol"}}},
		"xai":    {OK: true, At: at, ReadAt: at, Rows: []Row{{ID: "grok-4.7"}}},
	}}
	if err := tbl.Save(cache); err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	defer close(block)
	readers := []Reader{
		{Provider: "xai", Read: func(context.Context) ([]Row, string, error) { <-block; return nil, "", nil }},
		{Provider: "openai", Read: func(context.Context) ([]Row, string, error) {
			return []Row{{Provider: "openai", ID: "gpt-7-sol"}}, "codex", nil
		}},
	}
	failed, held, err := RefreshTable(context.Background(), cache, nil, readers, now, 100*time.Millisecond)
	if err != nil || held || failed["xai"] == "" {
		t.Fatalf("failed %v held %v err %v", failed, held, err)
	}
	tbl, err = Load(cache)
	if err != nil {
		t.Fatal(err)
	}
	if p := tbl.Providers["openai"]; p.Rows[0].ID != "gpt-7-sol" || p.ReadAt != now.UnixMilli() {
		t.Fatalf("the answered provider was not saved: %+v", p)
	}
	if tbl.RefreshedAt() != now.UnixMilli() {
		t.Fatalf("attempt time %d, want %d", tbl.RefreshedAt(), now.UnixMilli())
	}
}

// LoadConfigWithSettings: models.toml, with models.source_max_age from the
// settings (its env here), even when models.toml is absent.
func TestLoadConfigWithSettings(t *testing.T) {
	dir := t.TempDir()
	getenv := func(k string) string {
		switch k {
		case "HOME", "XDG_CONFIG_HOME":
			return dir
		case "AGENT_BUS_MODELS_SOURCE_MAX_AGE":
			return "72h"
		}
		return ""
	}
	cfg, err := LoadConfigWithSettings(filepath.Join(dir, "absent.toml"), getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SourceMaxAge != 72*time.Hour {
		t.Errorf("SourceMaxAge = %v, want 72h", cfg.SourceMaxAge)
	}
}
