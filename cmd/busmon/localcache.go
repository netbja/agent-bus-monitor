package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type busmonCache struct {
	Project          string           `json:"project"`
	LastVisit        int64            `json:"last_visit,omitempty"`
	Snoozes          map[string]int64 `json:"snoozes,omitempty"`
	SummaryRequestID string           `json:"summary_request_id,omitempty"`
	SummaryThread    string           `json:"summary_thread,omitempty"`
}

func busmonCachePath(project string) string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "agentbus", "busmon", project+".json")
}

func loadBusmonCache(path, project string) (*busmonCache, error) {
	c := &busmonCache{Project: project, Snoozes: map[string]int64{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, c); err != nil || c.Project != project {
		return &busmonCache{Project: project, Snoozes: map[string]int64{}}, fmt.Errorf("invalid busmon cache")
	}
	if c.Snoozes == nil {
		c.Snoozes = map[string]int64{}
	}
	return c, nil
}

func (c *busmonCache) save(path string) error {
	if c == nil {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".busmon-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return nil
}

func (c *busmonCache) snooze(key string, until time.Time) {
	if c == nil {
		return
	}
	if c.Snoozes == nil {
		c.Snoozes = map[string]int64{}
	}
	c.Snoozes[key] = until.UnixMilli()
}

func (c *busmonCache) snoozed(key string, now time.Time) bool {
	return c != nil && c.Snoozes[key] > now.UnixMilli()
}
