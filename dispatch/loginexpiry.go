package dispatch

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/loginexpiry"
)

// Claude Code's login expiry, read before it passes (login-updates spec r9
// §3.4-3.5): the dispatcher reads one field, claudeAiOauth.refreshTokenExpiresAt,
// from each Claude agent's config directory (loginexpiry), keeps the results
// in memory, warns the person once per agent and expiry, and the login-expiry
// health check reports them. What leaves the reader is a time and a reason
// from a closed set; nothing else of the credentials is seen here.

// DefaultLoginExpiryWarn is dispatch.login_expiry_warn's default.
const DefaultLoginExpiryWarn = 72 * time.Hour

const (
	loginCredentialsEvery = time.Hour   // an unexpired directory
	loginFailedEvery      = time.Minute // an expired one: its /login is seen within a minute
)

// LoginExpiry is one agent's result: its status (ok, warn, fail or unknown),
// the expiry read, or the reason none was.
type LoginExpiry struct {
	Agent  string
	Status health.Status
	Expiry time.Time
	Reason loginexpiry.Reason
}

// loginDir is a config directory's last read (readAt on the local clock: a
// throttle, never persisted).
type loginDir struct {
	readAt time.Time
	expiry time.Time
	reason loginexpiry.Reason
}

// loginResults are the last pass's results, read by the health goroutine.
type loginResults struct {
	mu   sync.Mutex
	read bool
	all  []LoginExpiry
}

func (l *loop) loginExpiryResults() []LoginExpiry {
	rs, _ := l.loginExpirySnapshot()
	return rs
}

// loginExpirySnapshot is a copy of the last results; false before the first pass.
func (l *loop) loginExpirySnapshot() ([]LoginExpiry, bool) {
	l.loginRes.mu.Lock()
	defer l.loginRes.mu.Unlock()
	return append([]LoginExpiry(nil), l.loginRes.all...), l.loginRes.read
}

// credentialsPass reads each Claude agent's directory when due (once an hour,
// once a minute after an expired read; agents sharing one are read once),
// judges every agent against now (Redis TIME) and warns the person.
func (l *loop) credentialsPass(ctx context.Context, now time.Time, zone *time.Location) error {
	c := l.cfg.Login
	if l.loginDirs == nil {
		l.loginDirs = map[string]loginDir{}
	}
	warn := DefaultLoginExpiryWarn
	if c.Warn != nil {
		warn = c.Warn()
	}
	names := make([]string, 0, len(l.snaps))
	for name, s := range l.snaps {
		if c.Checked != nil && c.Checked(s.Client) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	results := make([]LoginExpiry, 0, len(names))
	for _, name := range names {
		dir := l.snaps[name].ClaudeConfigDir
		d, seen := l.loginDirs[dir]
		every := loginCredentialsEvery
		if seen && d.reason == loginexpiry.OK && !d.expiry.After(now) {
			every = loginFailedEvery
		}
		if !seen || time.Since(d.readAt) >= every {
			d.expiry, d.reason = c.Credentials(dir, now)
			d.readAt = time.Now()
			l.loginDirs[dir] = d
		}
		results = append(results, judgeLogin(name, d, now, warn))
	}
	l.loginRes.mu.Lock()
	l.loginRes.read, l.loginRes.all = true, results
	l.loginRes.mu.Unlock()
	for _, r := range results {
		if r.Status != health.Warn {
			continue
		}
		sent, err := l.b.FencedNotifyOnceForPerson(ctx, l.token, loginExpiryMark(r.Agent), strconv.FormatInt(r.Expiry.UnixMilli(), 10),
			dispatchFrom, LoginExpiryWarnText(r.Agent, r.Expiry, now, zone))
		if err != nil {
			return err
		}
		if sent {
			l.log(map[string]any{"action": "login-expiry-warn", "agent": r.Agent, "expiry": r.Expiry.UnixMilli()})
		}
	}
	return nil
}

// judgeLogin: unknown with the read's reason; fail once passed; warn within
// warn (0: never); ok beyond.
func judgeLogin(agent string, d loginDir, now time.Time, warn time.Duration) LoginExpiry {
	r := LoginExpiry{Agent: agent, Expiry: d.expiry, Reason: d.reason}
	switch {
	case d.reason != loginexpiry.OK:
		r.Status, r.Expiry = health.Unknown, time.Time{}
	case !d.expiry.After(now):
		r.Status = health.Fail
	case d.expiry.Sub(now) <= warn: // 0: never, the expiry being ahead
		r.Status = health.Warn
	default:
		r.Status = health.OK
	}
	return r
}

// loginExpiryMark is the warning's once-mark; its episode is the expiry
// (ms), so a /login giving a later date warns again.
func loginExpiryMark(agent string) string { return "login-expiry:" + agent }

// LoginExpiryWarnText is the person's warning (§3.5).
func LoginExpiryWarnText(agent string, expiry, now time.Time, zone *time.Location) string {
	return fmt.Sprintf("%s's Claude login expires %s (%s): run /login in its pane.", agent, expiry.In(zone).Format("Mon 15:04"), timeLeft(expiry.Sub(now)))
}

func timeLeft(d time.Duration) string {
	if d < time.Minute { // a warn is always ahead: never "0 minutes"
		return "less than a minute"
	}
	n, unit := int(d/time.Minute), "minute"
	switch {
	case d >= 24*time.Hour:
		n, unit = int(d/(24*time.Hour)), "day"
	case d >= time.Hour:
		n, unit = int(d/time.Hour), "hour"
	}
	if n != 1 {
		unit += "s"
	}
	return strconv.Itoa(n) + " " + unit
}

// LoginExpiryCheck is the login-expiry health check: it reports the loop's
// last results (results, false before the first pass) and reads no file.
// Its status is its worst agent's.
func LoginExpiryCheck(results func() ([]LoginExpiry, bool), zone *time.Location) HealthCheck {
	d, _ := health.ByID("login-expiry")
	if zone == nil {
		zone = time.Local
	}
	return HealthCheck{Definition: d, Run: func(context.Context) (health.Outcome, error) {
		rs, read := results()
		if !read {
			return health.Outcome{Status: health.Unknown, Detail: "not read yet"}, nil
		}
		if len(rs) == 0 {
			return health.Outcome{Status: health.OK, Detail: "no Claude Code agent"}, nil
		}
		worst, parts := health.OK, make([]string, 0, len(rs))
		for _, r := range rs {
			if loginRank[r.Status] > loginRank[worst] {
				worst = r.Status
			}
			if r.Status == health.Unknown {
				parts = append(parts, r.Agent+": unknown ("+string(r.Reason)+")")
			} else {
				parts = append(parts, r.Agent+": "+r.Expiry.In(zone).Format("Mon 02 Jan 15:04")+" ("+string(r.Status)+")")
			}
		}
		return health.Outcome{Status: worst, Detail: strings.Join(parts, "; ")}, nil
	}}
}

var loginRank = map[health.Status]int{health.OK: 0, health.Unknown: 1, health.Warn: 2, health.Fail: 3}
