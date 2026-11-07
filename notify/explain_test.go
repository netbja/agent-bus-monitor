package notify

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// notify-guided-setup §7: a test's failure in plain words, never a URL.
func TestExplainEveryRow(t *testing.T) {
	for _, c := range []struct {
		kind string
		err  error
		want string
	}{
		{"ntfy", &SendError{Target: "p", Reason: "no connection"}, "the server cannot be reached: check the address, or your network"},
		{"ntfy", &SendError{Target: "p", Reason: "certificate"}, "the server's certificate is not valid for this address"},
		{"ntfy", &SendError{Target: "p", Reason: "timeout"}, "the server did not answer in 10 s"},
		{"ntfy", &SendError{Target: "p", Status: 401}, "the server refused the token (or this topic needs one)"},
		{"ntfy", &SendError{Target: "p", Status: 403}, "the server refused the token (or this topic needs one)"},
		{"ntfy", &SendError{Target: "p", Status: 429}, "the server limits this machine for now: try again in a minute"},
		{"discord", &SendError{Target: "t", Status: 401}, "this webhook no longer exists, or was not copied whole"},
		{"discord", &SendError{Target: "t", Status: 404}, "this webhook no longer exists, or was not copied whole"},
		{"discord", &SendError{Target: "t", Status: 429}, "Discord limits this webhook for now: try again in a minute"},
		{"desktop", &SendError{Target: "h", Reason: "notify-send failed (exit 1)"}, "notify-send failed: is a desktop session running? (exit 1)"},
		{"desktop", &SendError{Target: "h", Reason: "no notify-send on PATH"}, "install libnotify (notify-send) first"},
		{"ntfy", &SendError{Target: "p", Status: 502}, "the send failed (502)"},
		{"discord", &SendError{Target: "t", Status: 500}, "the send failed (500)"},
	} {
		if got := Explain(c.kind, c.err); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.kind, c.err, got, c.want)
		}
	}
	if Explain("ntfy", nil) != "" {
		t.Error("no error explains nothing")
	}
}

// The transport reasons come from real failures: a refused connection, a
// certificate for another name, a server too slow.
func TestTransportReasonsFromRealFailures(t *testing.T) {
	send := func(url string, timeout time.Duration) error {
		c := newClient()
		c.http.Timeout = timeout
		c.sleep = func(context.Context, time.Duration) error { return nil }
		return (&ntfy{name: "p", url: url, c: c}).Send(context.Background(), Message{Title: "t"})
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	if got := Explain("ntfy", send(url+"/topic", time.Second)); !strings.HasPrefix(got, "the server cannot be reached") {
		t.Errorf("refused: %q", got)
	}
	tlsSrv := httptest.NewUnstartedServer(http.NotFoundHandler()) // a certificate this client does not trust
	tlsSrv.Config.ErrorLog = log.New(io.Discard, "", 0)
	tlsSrv.StartTLS()
	defer tlsSrv.Close()
	if got := Explain("ntfy", send(tlsSrv.URL+"/topic", time.Second)); got != "the server's certificate is not valid for this address" {
		t.Errorf("certificate: %q", got)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(300 * time.Millisecond) }))
	defer slow.Close()
	if got := Explain("ntfy", send(slow.URL+"/topic", 50*time.Millisecond)); got != "the server did not answer in 10 s" {
		t.Errorf("timeout: %q", got)
	}
}
