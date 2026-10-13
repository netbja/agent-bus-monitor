package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/netbja/agent-bus-monitor/buildinfo"
)

// latestReleaseURL is the GitHub API answer busmon reads once a day.
const latestReleaseURL = "https://api.github.com/repos/netbja/agent-bus-monitor/releases/latest"

// updateCache is what latestRelease keeps between runs: the last check and
// what it found ("" when it failed).
type updateCache struct {
	CheckedAt time.Time `json:"checked_at"`
	Tag       string    `json:"tag"`
}

// latestRelease returns the newest release tag, checking the network at most
// once every 24 hours (cache file) with a 3 s budget. Any failure is silent and
// recorded, so it is not retried before the next day.
func latestRelease(ctx context.Context, url, cachePath string, now time.Time) string {
	var c updateCache
	if b, err := os.ReadFile(cachePath); err == nil && json.Unmarshal(b, &c) == nil && now.Sub(c.CheckedAt) < 24*time.Hour {
		return c.Tag
	}
	c = updateCache{CheckedAt: now, Tag: c.Tag}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil); err == nil {
		req.Header.Set("Accept", "application/vnd.github+json")
		if resp, err := http.DefaultClient.Do(req); err == nil {
			var v struct {
				TagName string `json:"tag_name"`
			}
			if resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&v) == nil {
				c.Tag = v.TagName
			}
			resp.Body.Close()
		}
	}
	if b, err := json.Marshal(c); err == nil {
		_ = os.MkdirAll(filepath.Dir(cachePath), 0o700)
		_ = os.WriteFile(cachePath, b, 0o600)
	}
	return c.Tag
}

// updateNotice is the tag to show, or "": only for a released build, when the
// latest tag is newer, and unless AGENT_BUS_NO_UPDATE_CHECK=1.
func updateNotice(version, latest, optOut string) string {
	if optOut == "1" || !buildinfo.Newer(latest, version) {
		return ""
	}
	return latest
}

// updateCachePath is ${XDG_CACHE_HOME:-~/.cache}/agentbus/latest.json.
func updateCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "agentbus", "latest.json")
}

// checkForUpdate runs once in the background at startup; it never blocks the
// UI and changes nothing but the header's last, lowest-priority item.
func checkForUpdate(ctx context.Context, st *shared) {
	if buildinfo.Version == "dev" || os.Getenv("AGENT_BUS_NO_UPDATE_CHECK") == "1" {
		return
	}
	path := updateCachePath()
	if path == "" {
		return
	}
	tag := updateNotice(buildinfo.Version, latestRelease(ctx, latestReleaseURL, path, time.Now()), os.Getenv("AGENT_BUS_NO_UPDATE_CHECK"))
	st.mu.Lock()
	st.update = tag
	st.mu.Unlock()
}
