package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// recorder is an httptest server that answers each request with the next
// status of its script (the last one repeats) and keeps what it received.
type recorder struct {
	mu       sync.Mutex
	statuses []int
	headers  []http.Header
	bodies   []string
	paths    []string
	extra    func(w http.ResponseWriter, n int)
	srv      *httptest.Server
}

func newRecorder(t *testing.T, statuses ...int) *recorder {
	t.Helper()
	r := &recorder{statuses: statuses}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		n := len(r.bodies)
		r.headers = append(r.headers, req.Header.Clone())
		r.bodies = append(r.bodies, string(data))
		r.paths = append(r.paths, req.URL.Path)
		status := r.statuses[len(r.statuses)-1]
		if n < len(r.statuses) {
			status = r.statuses[n]
		}
		r.mu.Unlock()
		if r.extra != nil {
			r.extra(w, n)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "secret-body-from-server")
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

// noSleep records the waits a client asked for instead of sleeping.
type noSleep struct{ waits []time.Duration }

func (s *noSleep) sleep(ctx context.Context, d time.Duration) error {
	s.waits = append(s.waits, d)
	return ctx.Err()
}

func testClient(s *noSleep) *client {
	return &client{http: plainHTTP(nil), sleep: s.sleep, now: time.Now}
}

var sample = Message{Project: "demo", Title: "demo · 2 waiting for you",
	Lines: []string{"master asks a question (2)", "team-stall: every agent idle"}, Priority: High}

func TestNtfySendsTitleBodyAndPriority(t *testing.T) {
	r := newRecorder(t, 200)
	s := &ntfy{name: "phone", url: r.srv.URL + "/agentbus-topic-123", c: testClient(&noSleep{})}
	if err := s.Send(context.Background(), sample); err != nil {
		t.Fatal(err)
	}
	h := r.headers[0]
	if h.Get("Title") != sample.Title || h.Get("Priority") != "high" || h.Get("Tags") != "agentbus" || h.Get("Markdown") != "no" {
		t.Fatalf("headers %v", h)
	}
	if h.Get("Authorization") != "" {
		t.Fatalf("no token, yet Authorization %q", h.Get("Authorization"))
	}
	if r.bodies[0] != "master asks a question (2)\nteam-stall: every agent idle" {
		t.Fatalf("body %q", r.bodies[0])
	}
	if r.paths[0] != "/agentbus-topic-123" {
		t.Fatalf("path %q", r.paths[0])
	}
	if s.Name() != "phone" || s.Kind() != "ntfy" {
		t.Fatalf("name %q kind %q", s.Name(), s.Kind())
	}
}

func TestNtfyBearerOnlyWithAToken(t *testing.T) {
	r := newRecorder(t, 200)
	s := &ntfy{name: "phone", url: r.srv.URL + "/t", token: "tk_abc", c: testClient(&noSleep{})}
	if err := s.Send(context.Background(), Message{Title: "x", Lines: []string{"y"}, Priority: Low}); err != nil {
		t.Fatal(err)
	}
	if got := r.headers[0].Get("Authorization"); got != "Bearer tk_abc" {
		t.Fatalf("Authorization %q", got)
	}
	if got := r.headers[0].Get("Priority"); got != "low" {
		t.Fatalf("Priority %q", got)
	}
}

func TestDiscordSendsJSONWithNoMentions(t *testing.T) {
	r := newRecorder(t, 204)
	s := &discord{name: "team", url: r.srv.URL + "/api/webhooks/1/x", c: testClient(&noSleep{})}
	m := sample
	m.Lines = append(m.Lines, "@everyone <@&123> look")
	if err := s.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Username        string `json:"username"`
		Content         string `json:"content"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	if err := json.Unmarshal([]byte(r.bodies[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got.Username != "agentbus" || got.AllowedMentions.Parse == nil || len(got.AllowedMentions.Parse) != 0 {
		t.Fatalf("payload %s", r.bodies[0])
	}
	if !strings.Contains(r.bodies[0], `"parse":[]`) {
		t.Fatalf("allowed_mentions.parse must be an empty list: %s", r.bodies[0])
	}
	want := "**demo · 2 waiting for you**\nmaster asks a question (2)\nteam-stall: every agent idle\n@everyone <@&123> look"
	if got.Content != want {
		t.Fatalf("content %q", got.Content)
	}
	if r.headers[0].Get("Content-Type") != "application/json" {
		t.Fatalf("content type %q", r.headers[0].Get("Content-Type"))
	}
}

func TestMessageCappedAt2000Bytes(t *testing.T) {
	r := newRecorder(t, 204)
	s := &discord{name: "team", url: r.srv.URL + "/w", c: testClient(&noSleep{})}
	m := Message{Title: "t", Lines: []string{strings.Repeat("é", 1500)}}
	if err := s.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	var got struct{ Content string }
	_ = json.Unmarshal([]byte(r.bodies[0]), &got)
	if len(got.Content) > MaxBytes || !strings.HasSuffix(got.Content, "…") {
		t.Fatalf("content %d bytes", len(got.Content))
	}
	if !utf8.ValidString(got.Content) {
		t.Fatal("cut inside a rune")
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	target := newRecorder(t, 200)
	r := newRecorder(t, 302)
	r.extra = func(w http.ResponseWriter, _ int) { w.Header().Set("Location", target.srv.URL+"/stolen") }
	s := &ntfy{name: "phone", url: r.srv.URL + "/t", c: testClient(&noSleep{})}
	err := s.Send(context.Background(), sample)
	if err == nil || target.count() != 0 {
		t.Fatalf("err %v, redirect target hit %d times", err, target.count())
	}
	if r.count() != 1 {
		t.Fatalf("a 3xx is not retried: %d requests", r.count())
	}
}

func TestRetryAfterOnceOn429(t *testing.T) {
	r := newRecorder(t, 429, 429, 200)
	r.extra = func(w http.ResponseWriter, n int) {
		if n < 2 {
			w.Header().Set("Retry-After", "7")
		}
	}
	sl := &noSleep{}
	s := &ntfy{name: "phone", url: r.srv.URL + "/t", c: testClient(sl)}
	err := s.Send(context.Background(), sample)
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("a second 429 fails the send: %v", err)
	}
	if r.count() != 2 || len(sl.waits) != 1 || sl.waits[0] != 7*time.Second {
		t.Fatalf("requests %d waits %v", r.count(), sl.waits)
	}
}

func TestRetryAfterCappedAt60s(t *testing.T) {
	r := newRecorder(t, 429, 200)
	r.extra = func(w http.ResponseWriter, n int) { w.Header().Set("Retry-After", "3600") }
	sl := &noSleep{}
	s := &ntfy{name: "phone", url: r.srv.URL + "/t", c: testClient(sl)}
	if err := s.Send(context.Background(), sample); err != nil {
		t.Fatal(err)
	}
	if len(sl.waits) != 1 || sl.waits[0] != 60*time.Second {
		t.Fatalf("waits %v", sl.waits)
	}
}

func TestServerErrorRetriedTwice(t *testing.T) {
	r := newRecorder(t, 500)
	sl := &noSleep{}
	s := &ntfy{name: "phone", url: r.srv.URL + "/t", c: testClient(sl)}
	err := s.Send(context.Background(), sample)
	if err == nil || r.count() != 3 {
		t.Fatalf("err %v requests %d", err, r.count())
	}
	if len(sl.waits) != 2 || sl.waits[0] != 2*time.Second || sl.waits[1] != 8*time.Second {
		t.Fatalf("waits %v", sl.waits)
	}
}

func TestServerErrorThenSuccess(t *testing.T) {
	r := newRecorder(t, 503, 200)
	s := &ntfy{name: "phone", url: r.srv.URL + "/t", c: testClient(&noSleep{})}
	if err := s.Send(context.Background(), sample); err != nil || r.count() != 2 {
		t.Fatalf("err %v requests %d", err, r.count())
	}
}

func TestUnauthorizedNotRetried(t *testing.T) {
	r := newRecorder(t, 401)
	s := &ntfy{name: "phone", url: r.srv.URL + "/secret-topic-xyz", c: testClient(&noSleep{})}
	err := s.Send(context.Background(), sample)
	if err == nil || r.count() != 1 {
		t.Fatalf("err %v requests %d", err, r.count())
	}
	msg := err.Error()
	if !strings.Contains(msg, "phone") || !strings.Contains(msg, "401") {
		t.Fatalf("error names the target and status: %q", msg)
	}
	for _, leak := range []string{"secret-topic-xyz", "secret-body-from-server", "127.0.0.1", "http"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("error %q leaks %q", msg, leak)
		}
	}
}

func TestTransportErrorNamesNoURL(t *testing.T) {
	r := newRecorder(t, 200)
	url := r.srv.URL + "/secret-topic-xyz"
	r.srv.Close()
	sl := &noSleep{}
	s := &ntfy{name: "phone", url: url, c: testClient(sl)}
	err := s.Send(context.Background(), sample)
	if err == nil || len(sl.waits) != 2 {
		t.Fatalf("err %v waits %v", err, sl.waits)
	}
	for _, leak := range []string{"secret-topic-xyz", "127.0.0.1", "http"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error %q leaks %q", err, leak)
		}
	}
}

func TestDefaultClientIgnoresEnvironmentProxy(t *testing.T) {
	proxied := newRecorder(t, 200)
	t.Setenv("HTTP_PROXY", proxied.srv.URL)
	t.Setenv("HTTPS_PROXY", proxied.srv.URL)
	t.Setenv("NO_PROXY", "")
	r := newRecorder(t, 200)
	s := &ntfy{name: "phone", url: r.srv.URL + "/t", c: newClient()}
	if err := s.Send(context.Background(), sample); err != nil {
		t.Fatal(err)
	}
	if proxied.count() != 0 || r.count() != 1 {
		t.Fatalf("proxy %d direct %d", proxied.count(), r.count())
	}
}

func TestDesktopRunsNotifySend(t *testing.T) {
	var got []string
	d := NewDesktop("here", func(_ context.Context, title, body, urgency string) error {
		got = []string{title, body, urgency}
		return nil
	})
	if err := d.Send(context.Background(), sample); err != nil {
		t.Fatal(err)
	}
	if got[0] != sample.Title || got[1] != "master asks a question (2)\nteam-stall: every agent idle" || got[2] != "critical" {
		t.Fatalf("got %q", got)
	}
	for p, want := range map[Priority]string{Default: "normal", Low: "low"} {
		_ = d.Send(context.Background(), Message{Title: "t", Priority: p})
		if got[2] != want {
			t.Fatalf("priority %v: urgency %q", p, got[2])
		}
	}
	if d.Name() != "here" || d.Kind() != "desktop" {
		t.Fatalf("name %q kind %q", d.Name(), d.Kind())
	}
}

func TestDesktopWithoutNotifySend(t *testing.T) {
	err := NewDesktop("here", nil).Send(context.Background(), sample)
	if err == nil || !strings.Contains(err.Error(), "no notify-send on PATH") || !strings.Contains(err.Error(), "here") {
		t.Fatalf("err %v", err)
	}
}

func TestRetryAfterAsAnHTTPDate(t *testing.T) {
	now := time.Date(2026, 11, 3, 14, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		header string
		want   time.Duration
	}{
		{now.Add(30 * time.Second).Format(http.TimeFormat), 30 * time.Second},
		{now.Add(2 * time.Hour).Format(http.TimeFormat), 60 * time.Second},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{"nonsense", 2 * time.Second},
		{"999999999999999999999999999999", 60 * time.Second}, // past any integer: the cap, not 2 s (reviewer-codex)
		{"9300000000", 60 * time.Second},                     // past time.Duration: never a negative wait (foureyes)
		{"61", 60 * time.Second},
		{"", 2 * time.Second},
	} {
		r := newRecorder(t, 429, 200)
		h := c.header
		r.extra = func(w http.ResponseWriter, n int) {
			if h != "" {
				w.Header().Set("Retry-After", h)
			}
		}
		sl := &noSleep{}
		cl := testClient(sl)
		cl.now = func() time.Time { return now }
		s := &ntfy{name: "phone", url: r.srv.URL + "/t", c: cl}
		if err := s.Send(context.Background(), sample); err != nil {
			t.Fatal(err)
		}
		if len(sl.waits) != 1 || sl.waits[0] != c.want {
			t.Errorf("Retry-After %q: waits %v, want %v", c.header, sl.waits, c.want)
		}
	}
}
