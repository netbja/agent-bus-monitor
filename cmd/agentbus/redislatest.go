package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

// redis-watch: the dispatch service checks the latest Redis release of each
// major once a day per host, for the health check redis-version (which
// compares the broker within its own major). It
// only reads and reports: nothing here restarts or upgrades the broker.

// redisReleasesURL is GitHub's recent releases of Redis: Redis ships patches
// of several majors side by side, so /releases/latest alone would name only
// the newest major.
const redisReleasesURL = "https://api.github.com/repos/redis/redis/releases?per_page=50"

var redisTagPattern = regexp.MustCompile(`^v?[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}$`)

// redisLatest is the host's cache: CheckedAt the last attempt (the daily
// clock), FoundAt and Latest (major -> its newest release) the last successful
// one. A failed attempt moves only CheckedAt, so an offline host ages towards
// unknown, never towards ok.
type redisLatest struct {
	CheckedAt time.Time      `json:"checked_at"`
	FoundAt   time.Time      `json:"found_at,omitzero"`
	Latest    map[int]string `json:"latest,omitempty"`
}

// redisLatestPath is ${XDG_CACHE_HOME:-~/.cache}/agentbus/redis-latest.json.
func redisLatestPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agentbus", "redis-latest.json"), nil
}

func readRedisLatest(path string) (redisLatest, error) {
	var c redisLatest
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}

// fetchRedisLatest asks GitHub for Redis's recent releases, in 5 s, and keeps
// per major the newest plain version that is neither a draft nor a prerelease.
func fetchRedisLatest(ctx context.Context, client *http.Client) (map[int]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, redisReleasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Redis releases: HTTP %d", resp.StatusCode)
	}
	var list []struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&list); err != nil {
		return nil, err
	}
	out := map[int]string{}
	for _, r := range list {
		major, ok := redisMajor(r.TagName)
		if r.Draft || r.Prerelease || !ok {
			continue
		}
		if cur, has := out[major]; !has || versionAtLeast(r.TagName, cur) {
			out[major] = r.TagName
		}
	}
	if len(out) == 0 {
		return nil, errors.New("Redis releases: no version")
	}
	return out, nil
}

// redisMajor is the major of a plain x.y.z tag.
func redisMajor(tag string) (int, bool) {
	if !redisTagPattern.MatchString(tag) {
		return 0, false
	}
	major, err := strconv.Atoi(strings.TrimPrefix(tag[:strings.Index(tag, ".")], "v"))
	return major, err == nil
}

// refreshRedisLatestUnderLock fetches at most once a day per host (the cache
// file and its lock, like the model table). A failed or unreadable fetch is
// silent: it is recorded as an attempt and changes nothing else.
func refreshRedisLatestUnderLock(ctx context.Context, path string, fetch func(context.Context) (map[int]string, error), now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil
		}
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	c, _ := readRedisLatest(path) // a missing or corrupt cache is a first check
	if age := now.Sub(c.CheckedAt); age >= 0 && age < 24*time.Hour {
		return nil
	}
	c.CheckedAt = now
	if latest, err := fetch(ctx); err == nil && validRedisLatest(latest) {
		c.Latest, c.FoundAt = latest, now
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// redisRefresh is the dispatch service's daily chore, only for an installed
// project (like the model refresh).
func redisRefresh(b *bus.Bus) func(context.Context) error {
	return func(ctx context.Context) error {
		installed, err := b.ServicesInstalled(ctx)
		if err != nil || !installed {
			return err
		}
		path, err := redisLatestPath()
		if err != nil {
			return err
		}
		fetch := func(ctx context.Context) (map[int]string, error) { return fetchRedisLatest(ctx, http.DefaultClient) }
		return refreshRedisLatestUnderLock(ctx, path, fetch, time.Now())
	}
}

// redisVersionOutcome is the check redis-version: the broker's version
// against the host's cache.
func redisVersionOutcome(running string, runErr error, cache redisLatest, cacheErr error, now time.Time) health.Outcome {
	if runErr != nil {
		running = ""
	}
	if cacheErr != nil {
		cache = redisLatest{}
	}
	return health.EvaluateRedisVersion(running, cache.Latest, cache.FoundAt, now)
}

// validRedisLatest: at least one major, each filed under its own major as a
// plain version.
func validRedisLatest(latest map[int]string) bool {
	for major, v := range latest {
		if m, ok := redisMajor(v); !ok || m != major {
			return false
		}
	}
	return len(latest) > 0
}
