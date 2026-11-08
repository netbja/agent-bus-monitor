package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// The one HTTP client every sender uses (spec "The sender interface"): 10 s
// a request, no redirect followed, no proxy from the environment, TLS
// verified; a 429 waits for its Retry-After (at most 60 s) once, a 5xx or a
// transport error is retried twice, after 2 s then 8 s.
const (
	requestTimeout = 10 * time.Second
	maxRetryAfter  = 60 * time.Second
)

var serverRetries = []time.Duration{2 * time.Second, 8 * time.Second}

type client struct {
	http  *http.Client
	sleep func(ctx context.Context, d time.Duration) error
	now   func() time.Time // reads a Retry-After date
}

// directTransport is the default transport without a proxy: a message goes
// straight to its target or nowhere.
var directTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return t
}()

// plainHTTP is the http.Client a send uses: rt (a test's) or the direct
// transport, never a redirect followed (a 3xx could carry the message, and
// a token, to another host).
func plainHTTP(rt http.RoundTripper) *http.Client {
	if rt == nil {
		rt = directTransport
	}
	return &http.Client{Transport: rt, Timeout: requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func newClient() *client { return &client{http: plainHTTP(nil), sleep: sleepCtx, now: time.Now} }

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// post sends body to url with the headers, under the retry rule. Any status
// in 200-299 is success; anything else fails as a SendError naming target.
func (c *client) post(ctx context.Context, target, url string, header http.Header, body []byte) error {
	waited429 := false
	serverTry := 0
	for {
		status, retryAfter, err := c.once(ctx, url, header, body)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return &SendError{Target: target, Reason: "cancelled"}
			}
			if serverTry < len(serverRetries) {
				if c.sleep(ctx, serverRetries[serverTry]) != nil {
					return &SendError{Target: target, Reason: "cancelled"}
				}
				serverTry++
				continue
			}
			return &SendError{Target: target, Reason: transportReason(err)}
		case status >= 200 && status < 300:
			return nil
		case status == http.StatusTooManyRequests && !waited429:
			waited429 = true
			if c.sleep(ctx, retryAfter) != nil {
				return &SendError{Target: target, Reason: "cancelled"}
			}
			continue
		case status >= 500 && serverTry < len(serverRetries):
			if c.sleep(ctx, serverRetries[serverTry]) != nil {
				return &SendError{Target: target, Reason: "cancelled"}
			}
			serverTry++
			continue
		default:
			return &SendError{Target: target, Status: status}
		}
	}
}

// once makes one request. Its error is the transport's; the caller turns it
// into a reason that names no URL.
func (c *client) once(ctx context.Context, url string, header http.Header, body []byte) (int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // never kept: it may echo the URL
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	return resp.StatusCode, retryAfter(resp.Header.Get("Retry-After"), now()), nil
}

// retryAfter reads a Retry-After in seconds or as an HTTP date
// (reviewer-codex), capped at 60 s, a date already past = no wait; anything
// else (absent, nonsense) waits 2 s.
func retryAfter(v string, now time.Time) time.Duration {
	var d time.Duration
	if allDigits(v) {
		// Any number of seconds past the cap is the cap, however many digits
		// (reviewer-codex): never an overflow, never a negative wait.
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil || n > uint64(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		d = time.Duration(n) * time.Second
	} else if at, err := http.ParseTime(v); err == nil {
		d = max(at.Sub(now), 0)
	} else {
		return serverRetries[0]
	}
	return min(d, maxRetryAfter)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// transportReason is a transport error in words that carry no URL nor host:
// net/http's own message quotes the request URL.
func transportReason(err error) string {
	var te interface{ Timeout() bool }
	if errors.As(err, &te) && te.Timeout() {
		return "timeout"
	}
	var cv *tls.CertificateVerificationError
	var ua x509.UnknownAuthorityError
	var he x509.HostnameError
	var ci x509.CertificateInvalidError
	if errors.As(err, &cv) || errors.As(err, &ua) || errors.As(err, &he) || errors.As(err, &ci) {
		return "certificate" // the setup says so in words (Explain)
	}
	return "no connection"
}

func httpStatusText(code int) string {
	if s := http.StatusText(code); s != "" {
		return s
	}
	return "status"
}
