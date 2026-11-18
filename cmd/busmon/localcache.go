package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/netbja/agent-bus-monitor/internal/files"
)

type busmonCache struct {
	Project   string           `json:"project"`
	LastVisit int64            `json:"last_visit,omitempty"`
	Snoozes   map[string]int64 `json:"snoozes,omitempty"`
	Read      []string         `json:"read,omitempty"` // entries for the person that they opened (unread.go)
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
	if path == "" {
		return fmt.Errorf("empty busmon cache path")
	}
	now := time.Now().UnixMilli()
	for key, until := range c.Snoozes { // expired snoozes would only grow the file
		if until <= now {
			delete(c.Snoozes, key)
		}
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(b, '\n'))
}

// writeFileAtomic writes b to path through files.WriteAtomic, mode 0600, and
// first narrows an existing directory to 0700 (the cache is private).
func writeFileAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	return files.WriteAtomic(path, b)
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
