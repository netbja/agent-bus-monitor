package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// redis-watch: the latest release of each Redis major is fetched at most once
// a day per host, cached 0600, and a failed fetch is silent: it keeps the last
// releases found and when they were found, so the health check reads them as
// aging, never as a fresh answer.
func TestRefreshRedisLatestDailyAndSilent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentbus", "redis-latest.json")
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	calls := 0
	answer, fail := map[int]string{8: "8.2.2", 7: "7.4.6"}, error(nil)
	fetch := func(context.Context) (map[int]string, error) { calls++; return answer, fail }

	if err := refreshRedisLatestUnderLock(context.Background(), path, fetch, now); err != nil {
		t.Fatal(err)
	}
	c, err := readRedisLatest(path)
	if err != nil || c.Latest[8] != "8.2.2" || c.Latest[7] != "7.4.6" || !c.FoundAt.Equal(now) || !c.CheckedAt.Equal(now) {
		t.Fatalf("after a fetch: %+v, %v", c, err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("cache mode: %v %v", st, err)
	}
	// Within a day: no fetch.
	if err := refreshRedisLatestUnderLock(context.Background(), path, fetch, now.Add(23*time.Hour)); err != nil || calls != 1 {
		t.Fatalf("a fresh check fetched again: calls %d, %v", calls, err)
	}
	// A day later, offline: no error, the releases and their date are kept,
	// the attempt is recorded so it is not retried before the next day.
	fail = errors.New("offline")
	later := now.Add(25 * time.Hour)
	if err := refreshRedisLatestUnderLock(context.Background(), path, fetch, later); err != nil {
		t.Fatalf("an offline fetch is an error: %v", err)
	}
	c, _ = readRedisLatest(path)
	if calls != 2 || c.Latest[8] != "8.2.2" || !c.FoundAt.Equal(now) || !c.CheckedAt.Equal(later) {
		t.Fatalf("after a failed fetch: calls %d, %+v", calls, c)
	}
	if err := refreshRedisLatestUnderLock(context.Background(), path, fetch, later.Add(time.Hour)); err != nil || calls != 2 {
		t.Fatalf("a failed check was retried within the day: calls %d", calls)
	}
	// An answer with a version that is not one, or filed under another major,
	// is a failure, never stored; so is an empty one.
	for _, bad := range []map[int]string{{8: "<b>8.3.0</b>"}, {9: "8.3.0"}, {}} {
		fail, answer = nil, bad
		later = later.Add(25 * time.Hour)
		if err := refreshRedisLatestUnderLock(context.Background(), path, fetch, later); err != nil {
			t.Fatal(err)
		}
		if c, _ = readRedisLatest(path); c.Latest[8] != "8.2.2" || len(c.Latest) != 2 || !c.FoundAt.Equal(now) {
			t.Fatalf("%v was stored: %+v", bad, c)
		}
	}
}

func TestRefreshRedisLatestSkipsConcurrentHostRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentbus", "redis-latest.json")
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
	calls := 0
	fetch := func(context.Context) (map[int]string, error) { calls++; return map[int]string{8: "8.2.2"}, nil }
	if err := refreshRedisLatestUnderLock(context.Background(), path, fetch, time.Now()); err != nil || calls != 0 {
		t.Fatalf("fetched while another process held the host lock: calls %d, %v", calls, err)
	}
}

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The real fetcher reads GitHub's recent releases of redis/redis and keeps,
// per major, the newest plain version that is neither a draft nor a
// prerelease; tested on a fake transport.
func TestFetchRedisLatest(t *testing.T) {
	reply := func(code int, body string) *http.Client {
		return &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != redisReleasesURL {
				t.Errorf("fetched %s", r.URL)
			}
			return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		})}
	}
	list := `[{"tag_name":"9.0.0","prerelease":true},{"tag_name":"8.2.2"},{"tag_name":"8.10.1","draft":true},` +
		`{"tag_name":"7.4.6"},{"tag_name":"8.2.10"},{"tag_name":"8.0.4"},{"tag_name":"unstable"},{"tag_name":"7.2.11"}]`
	got, err := fetchRedisLatest(context.Background(), reply(200, list))
	if err != nil || len(got) != 2 || got[8] != "8.2.10" || got[7] != "7.4.6" {
		t.Errorf("ok reply: %v, %v (want 8: 8.2.10, 7: 7.4.6)", got, err)
	}
	for name, c := range map[string]*http.Client{
		"rate limited":            reply(403, `{"message":"API rate limit exceeded"}`),
		"server error with a tag": reply(500, `[{"tag_name":"8.2.2"}]`),
		"not json":                reply(200, `<html>`),
		"no version":              reply(200, `[{"tag_name":"unstable"}]`),
		"down": {Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("no route")
		})},
	} {
		if v, err := fetchRedisLatest(context.Background(), c); err == nil {
			t.Errorf("%s: %v without an error", name, v)
		}
	}
}

func TestRedisVersionOutcome(t *testing.T) {
	now := time.Now()
	cache := redisLatest{CheckedAt: now, FoundAt: now.Add(-time.Hour), Latest: map[int]string{8: "8.2.2"}}
	if o := redisVersionOutcome("8.2.1", nil, cache, nil, now); o.Status != "warn" {
		t.Errorf("behind: %+v", o)
	}
	if o := redisVersionOutcome("8.2.1", errors.New("INFO failed"), cache, nil, now); o.Status != "unknown" {
		t.Errorf("broker unread: %+v", o)
	}
	if o := redisVersionOutcome("8.2.1", nil, cache, errors.New("no cache"), now); o.Status != "unknown" {
		t.Errorf("no cache: %+v", o)
	}
}
