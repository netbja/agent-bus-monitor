package main

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/models"
)

func TestRefreshModelsUnderLockWritesPrivateCacheAndSkipsFreshRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentbus", "models.json")
	now := time.Now()
	reads := 0
	reader := models.Reader{Provider: "openai", Read: func(context.Context) ([]models.Row, string, error) {
		reads++
		return []models.Row{{Provider: "openai", Client: "codex", ID: "gpt-test"}}, "test", nil
	}}
	if err := refreshModelsUnderLock(context.Background(), path, models.DefaultTiers(), []models.Reader{reader}, now); err != nil {
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
	if err := refreshModelsUnderLock(context.Background(), path, models.DefaultTiers(), []models.Reader{reader}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("fresh cache triggered another read: %d", reads)
	}
}

func TestRefreshModelsUnderLockSkipsConcurrentHostRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentbus", "models.json")
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
	reader := models.Reader{Provider: "openai", Read: func(context.Context) ([]models.Row, string, error) {
		reads++
		return []models.Row{{ID: "gpt-test"}}, "test", nil
	}}
	if err := refreshModelsUnderLock(context.Background(), path, models.DefaultTiers(), []models.Reader{reader}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if reads != 0 {
		t.Fatalf("refresh ran while host lock was held: %d", reads)
	}
}
