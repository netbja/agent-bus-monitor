package dispatch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/loginexpiry"
)

// credWorld is a dispatcher holding the lease whose Claude agents' expiry is
// read by a fake reader: expiry[dir] (relative to the broker's now) or
// reason[dir], with each read counted.
type credWorld struct {
	l      *loop
	b      *bus.Bus
	r      *redis.Client
	now    time.Time // the broker's TIME at setup
	expiry map[string]time.Duration
	reason map[string]loginexpiry.Reason
	reads  map[string]int
	warn   time.Duration
}

func newCredWorld(t *testing.T, snaps map[string]bus.AgentSnapshot) *credWorld {
	t.Helper()
	b, rc := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "holder", time.Minute); err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	now, err := b.ServerTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w := &credWorld{b: b, r: rc, now: now, expiry: map[string]time.Duration{}, reason: map[string]loginexpiry.Reason{}, reads: map[string]int{}, warn: 72 * time.Hour}
	w.l = &loop{b: b, token: "holder", last: map[string]string{}, snaps: snaps, cfg: Config{EscalateTo: "master", Log: io.Discard,
		Login: LoginConfig{Zone: time.UTC,
			Checked: func(client string) bool { return client == "claude-code" },
			Warn:    func() time.Duration { return w.warn },
			Credentials: func(dir string, _ time.Time) (time.Time, loginexpiry.Reason) {
				w.reads[dir]++
				if r, ok := w.reason[dir]; ok {
					return time.Time{}, r
				}
				return w.now.Add(w.expiry[dir]).Truncate(time.Millisecond), loginexpiry.OK
			}}}}
	return w
}

// pass runs one login pass past the minute throttle.
func (w *credWorld) pass(t *testing.T) {
	t.Helper()
	w.l.loginAt = time.Time{}
	if err := w.l.loginPass(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (w *credWorld) result(t *testing.T, agent string) LoginExpiry {
	t.Helper()
	for _, r := range w.l.loginExpiryResults() {
		if r.Agent == agent {
			return r
		}
	}
	t.Fatalf("no result for %s in %+v", agent, w.l.loginExpiryResults())
	return LoginExpiry{}
}

func claudeSnap(dir string) bus.AgentSnapshot {
	return bus.AgentSnapshot{Client: "claude-code", ClaudeConfigDir: dir}
}

// Far future → ok, no notice.
func TestLoginExpiryFarFutureIsOK(t *testing.T) {
	w := newCredWorld(t, map[string]bus.AgentSnapshot{"coder": claudeSnap("/c")})
	w.expiry["/c"] = 30 * 24 * time.Hour
	w.pass(t)
	if r := w.result(t, "coder"); r.Status != health.OK || r.Reason != loginexpiry.OK || !r.Expiry.Equal(w.now.Add(30*24*time.Hour).Truncate(time.Millisecond)) {
		t.Fatalf("%+v", r)
	}
	if ns := personNotices(t, w.b); len(ns) != 0 {
		t.Fatalf("notices %+v", ns)
	}
}

// Two days → warn and one notice naming the agent; the next pass sends none.
func TestLoginExpiryWarnsOncePerEpisode(t *testing.T) {
	w := newCredWorld(t, map[string]bus.AgentSnapshot{"coder": claudeSnap("/c")})
	w.expiry["/c"] = 49 * time.Hour
	w.pass(t)
	w.l.loginDirs["/c"] = loginDir{} // past the hour: read again
	w.pass(t)
	if r := w.result(t, "coder"); r.Status != health.Warn {
		t.Fatalf("%+v", r)
	}
	ns := personNotices(t, w.b)
	exp := w.now.Add(49 * time.Hour).Truncate(time.Millisecond)
	want := "coder's Claude login expires " + exp.UTC().Format("Mon 15:04") + " (2 days): run /login in its pane."
	if len(ns) != 1 || ns[0].Message != want || ns[0].From != "dispatch" {
		t.Fatalf("notices %+v, want one %q", ns, want)
	}
	marks, err := w.b.QuotaMarks(context.Background())
	if err != nil || marks["login-expiry:coder"] != strconv.FormatInt(exp.UnixMilli(), 10) {
		t.Fatalf("the episode is not the expiry: %v %v", marks, err)
	}
	// A new /login gives a later date, still within the threshold: a new episode.
	w.expiry["/c"] = 50 * time.Hour
	w.l.loginDirs["/c"] = loginDir{}
	w.pass(t)
	if n := len(personNotices(t, w.b)); n != 2 {
		t.Fatalf("%d notices after a later expiry, want 2", n)
	}
}

// dispatch.login_expiry_warn = 0: no warn and no notice.
func TestLoginExpiryWarnOff(t *testing.T) {
	w := newCredWorld(t, map[string]bus.AgentSnapshot{"coder": claudeSnap("/c")})
	w.expiry["/c"] = time.Hour
	w.warn = 0
	w.pass(t)
	if r := w.result(t, "coder"); r.Status != health.OK {
		t.Fatalf("%+v", r)
	}
	if ns := personNotices(t, w.b); len(ns) != 0 {
		t.Fatalf("notices %+v", ns)
	}
}

// Expired → fail (its notice and hold are the next step's).
func TestLoginExpiryPassedIsFail(t *testing.T) {
	w := newCredWorld(t, map[string]bus.AgentSnapshot{"coder": claudeSnap("/c")})
	w.expiry["/c"] = -time.Minute
	w.warn = 0 // fail whatever the setting
	w.pass(t)
	if r := w.result(t, "coder"); r.Status != health.Fail {
		t.Fatalf("%+v", r)
	}
}

// Each reason of the closed set is unknown with exactly that reason, and
// never a notice.
func TestLoginExpiryReasonIsUnknown(t *testing.T) {
	for _, r := range []loginexpiry.Reason{loginexpiry.InvalidPath, loginexpiry.NoFile, loginexpiry.NotRegular, loginexpiry.NotOwned,
		loginexpiry.Unreadable, loginexpiry.TooLarge, loginexpiry.NoField, loginexpiry.NotANumber, loginexpiry.NotATime,
		loginexpiry.KeychainUnverified, loginexpiry.KeychainExit(36)} {
		w := newCredWorld(t, map[string]bus.AgentSnapshot{"coder": claudeSnap("/c")})
		w.reason["/c"] = r
		w.pass(t)
		if got := w.result(t, "coder"); got.Status != health.Unknown || got.Reason != r {
			t.Fatalf("%q: %+v", r, got)
		}
		if ns := personNotices(t, w.b); len(ns) != 0 {
			t.Fatalf("%q: notices %+v", r, ns)
		}
	}
}

// Two agents sharing a directory → one read; a non-Claude agent → not
// checked; the default directory is "".
func TestLoginExpiryReadsEachDirectoryOnce(t *testing.T) {
	w := newCredWorld(t, map[string]bus.AgentSnapshot{
		"coder": claudeSnap(""), "foureyes": claudeSnap(""), "coder2": claudeSnap("/b"),
		"reviewer-codex": {Client: "codex"},
	})
	w.expiry[""], w.expiry["/b"] = 30*24*time.Hour, 30*24*time.Hour
	w.pass(t)
	if w.reads[""] != 1 || w.reads["/b"] != 1 || len(w.reads) != 2 {
		t.Fatalf("reads %v", w.reads)
	}
	var names []string
	for _, r := range w.l.loginExpiryResults() {
		names = append(names, r.Agent)
	}
	if strings.Join(names, ",") != "coder,coder2,foureyes" {
		t.Fatalf("results for %v", names)
	}
}

// Cadence: an ok directory is read once an hour, a failed one once a minute.
func TestLoginExpiryCadence(t *testing.T) {
	w := newCredWorld(t, map[string]bus.AgentSnapshot{"coder": claudeSnap("/ok"), "master": claudeSnap("/gone")})
	w.expiry["/ok"], w.expiry["/gone"] = 30*24*time.Hour, -time.Hour
	w.pass(t)
	age := func(dir string, d time.Duration) {
		e := w.l.loginDirs[dir]
		e.readAt = e.readAt.Add(-d)
		w.l.loginDirs[dir] = e
	}
	age("/ok", 59*time.Minute)
	age("/gone", 61*time.Second)
	w.pass(t)
	if w.reads["/ok"] != 1 || w.reads["/gone"] != 2 {
		t.Fatalf("reads %v", w.reads)
	}
	// /login: the failed directory's later expiry is read within the minute.
	w.expiry["/gone"] = 30 * 24 * time.Hour
	age("/ok", 2*time.Minute)
	age("/gone", 61*time.Second)
	w.pass(t)
	if w.reads["/ok"] != 2 || w.reads["/gone"] != 3 || w.result(t, "master").Status != health.OK {
		t.Fatalf("reads %v, master %+v", w.reads, w.result(t, "master"))
	}
}

// The health check reports the loop's results and reads nothing itself.
func TestLoginExpiryCheckReportsResults(t *testing.T) {
	exp := time.Date(2026, 11, 19, 15, 40, 0, 0, time.UTC)
	calls := 0
	check := LoginExpiryCheck(func() ([]LoginExpiry, bool) {
		calls++
		return []LoginExpiry{
			{Agent: "coder", Status: health.Warn, Expiry: exp},
			{Agent: "coder2", Status: health.Unknown, Reason: loginexpiry.NoFile},
			{Agent: "master", Status: health.OK, Expiry: exp.AddDate(0, 0, 20)},
		}, true
	}, time.UTC)
	if check.Definition.ID != "login-expiry" || check.Definition.Interval != 5*time.Minute || len(check.Definition.Person) != 0 {
		t.Fatalf("definition %+v", check.Definition)
	}
	out, err := check.Run(context.Background())
	want := "coder: Thu 19 Nov 15:40 (warn); coder2: unknown (no file); master: Wed 09 Dec 15:40 (ok)"
	if err != nil || out.Status != health.Warn || out.Detail != want || calls != 1 {
		t.Fatalf("%+v %v, want warn %q", out, err, want)
	}
	// The worst status wins: fail over warn over unknown over ok.
	for _, c := range []struct {
		in   []health.Status
		want health.Status
	}{
		{[]health.Status{health.OK, health.Unknown}, health.Unknown},
		{[]health.Status{health.Unknown, health.Warn}, health.Warn},
		{[]health.Status{health.Warn, health.Fail, health.OK}, health.Fail},
		{[]health.Status{health.OK}, health.OK},
	} {
		var rs []LoginExpiry
		for i, s := range c.in {
			rs = append(rs, LoginExpiry{Agent: "a" + strconv.Itoa(i), Status: s, Expiry: exp, Reason: loginexpiry.NoFile})
		}
		out, _ := LoginExpiryCheck(func() ([]LoginExpiry, bool) { return rs, true }, time.UTC).Run(context.Background())
		if out.Status != c.want {
			t.Fatalf("%v → %s, want %s", c.in, out.Status, c.want)
		}
	}
	// Not read yet: unknown. Read, no Claude agent: ok.
	if out, _ := LoginExpiryCheck(func() ([]LoginExpiry, bool) { return nil, false }, time.UTC).Run(context.Background()); out.Status != health.Unknown {
		t.Fatalf("before the first pass: %+v", out)
	}
	if out, _ := LoginExpiryCheck(func() ([]LoginExpiry, bool) { return nil, true }, time.UTC).Run(context.Background()); out.Status != health.OK || out.Detail != "no Claude Code agent" {
		t.Fatalf("no agent: %+v", out)
	}
}

// The warning's wording for the remaining time.
func TestLoginExpiryWarnText(t *testing.T) {
	exp := time.Date(2026, 11, 19, 15, 40, 0, 0, time.UTC)
	for _, c := range []struct {
		left time.Duration
		want string
	}{
		{49 * time.Hour, "(2 days)"},
		{25 * time.Hour, "(1 day)"},
		{5*time.Hour + 30*time.Minute, "(5 hours)"},
		{90 * time.Minute, "(1 hour)"},
		{20 * time.Minute, "(20 minutes)"},
	} {
		got := LoginExpiryWarnText("coder", exp, exp.Add(-c.left), time.UTC)
		if got != "coder's Claude login expires Thu 15:40 "+c.want+": run /login in its pane." {
			t.Fatalf("%v: %q", c.left, got)
		}
	}
}

// The health loop runs the login-expiry check on the login pass's results
// when the loop reads credentials, and stores its record.
func TestHealthLoopReportsLoginExpiry(t *testing.T) {
	w := newCredWorld(t, map[string]bus.AgentSnapshot{"coder": claudeSnap("/c")})
	ctx := context.Background()
	if err := w.b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		t.Fatal(err)
	}
	w.expiry["/c"] = 49 * time.Hour
	w.pass(t)
	reads := w.reads["/c"]
	w.l.cfg.Health = HealthConfig{Tick: time.Millisecond, PassBudget: time.Second}
	runCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	_ = w.l.healthLoop(runCtx)
	cancel()
	recs, err := w.b.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rec := recs["login-expiry"]; rec.Status != health.Warn || !strings.HasPrefix(rec.Detail, "coder: ") {
		t.Fatalf("record %+v", rec)
	}
	if w.reads["/c"] != reads {
		t.Fatalf("the check read the credentials: %d reads, want %d", w.reads["/c"], reads)
	}
}

// No token anywhere (§6): credential files whose tokens are sentinels, read
// by the real reader through ok, warn, fail, a later /login and each unknown
// case; the sentinels appear in none of the log, any key of the project, the
// health check's detail or the results.
func TestLoginExpiryLeaksNoToken(t *testing.T) {
	const access, refresh = "SENTINEL-ACCESS-7f3a", "SENTINEL-REFRESH-91c2"
	root := t.TempDir()
	w := newCredWorld(t, map[string]bus.AgentSnapshot{})
	var logged bytes.Buffer
	w.l.cfg.Log = &logged
	w.l.cfg.Login.Credentials = func(dir string, now time.Time) (time.Time, loginexpiry.Reason) {
		return loginexpiry.ReadFile(loginexpiry.CredentialsFile(dir, ""), now)
	}
	write := func(name, field string) string {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := `{"claudeAiOauth":{"accessToken":"` + access + `","refreshToken":"` + refresh + `","expiresAt":` +
			strconv.FormatInt(w.now.UnixMilli(), 10) + `,"refreshTokenExpiresAt":` + field + `},"other":"` + access + `"}`
		if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	ms := func(d time.Duration) string { return strconv.FormatInt(w.now.Add(d).UnixMilli(), 10) }
	ok, warn, fail := write("ok", ms(30*24*time.Hour)), write("warn", ms(30*time.Hour)), write("fail", ms(-time.Hour))
	str, sec := write("string", `"`+refresh+`"`), write("seconds", strconv.FormatInt(w.now.Unix(), 10))
	none := filepath.Join(root, "none")
	big := write("big", ms(time.Hour))
	if err := os.WriteFile(filepath.Join(big, ".credentials.json"), bytes.Repeat([]byte(access), 65<<10/len(access)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.MkdirAll(link, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ok, ".credentials.json"), filepath.Join(link, ".credentials.json")); err != nil {
		t.Fatal(err)
	}
	w.l.snaps = map[string]bus.AgentSnapshot{"a-ok": claudeSnap(ok), "a-warn": claudeSnap(warn), "a-fail": claudeSnap(fail),
		"a-string": claudeSnap(str), "a-seconds": claudeSnap(sec), "a-none": claudeSnap(none), "a-big": claudeSnap(big),
		"a-link": claudeSnap(link), "a-relative": claudeSnap("relative")}
	w.pass(t)
	write("fail", ms(20*24*time.Hour)) // /login: released on the next minute's read
	w.l.loginDirs[fail] = loginDir{}
	w.pass(t)
	want := map[string]health.Status{"a-ok": health.OK, "a-warn": health.Warn, "a-fail": health.OK, "a-string": health.Unknown,
		"a-seconds": health.Unknown, "a-none": health.Unknown, "a-big": health.Unknown, "a-link": health.Unknown, "a-relative": health.Unknown}
	var seen []string
	for _, r := range w.l.loginExpiryResults() {
		if want[r.Agent] != r.Status {
			t.Errorf("%s: %+v, want %s", r.Agent, r, want[r.Agent])
		}
		seen = append(seen, r.Agent+" "+string(r.Status)+" "+string(r.Reason)+" "+r.Expiry.String())
	}
	out, err := LoginExpiryCheck(w.l.loginExpirySnapshot, time.UTC).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dump := dumpProject(t, w.r, w.b.Project())
	everything := logged.String() + out.Detail + strings.Join(seen, "\n") + dump
	if !strings.Contains(dump, "a-warn's Claude login expires") || !strings.Contains(logged.String(), "login-expiry-warn") {
		t.Fatal("the dump holds nothing: the scan proves nothing")
	}
	for _, s := range []string{access, refresh, "SENTINEL"} {
		if strings.Contains(everything, s) {
			t.Fatalf("%s leaked:\n%s", s, everything)
		}
	}
}

// dumpProject is every value of every {p}:* key, as text.
func dumpProject(t *testing.T, r *redis.Client, project string) string {
	t.Helper()
	ctx := context.Background()
	keys, err := r.Keys(ctx, project+":*").Result()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, k := range keys {
		typ, err := r.Type(ctx, k).Result()
		if err != nil {
			t.Fatal(err)
		}
		var v any
		switch typ {
		case "string":
			v, err = r.Get(ctx, k).Result()
		case "hash":
			v, err = r.HGetAll(ctx, k).Result()
		case "list":
			v, err = r.LRange(ctx, k, 0, -1).Result()
		case "set":
			v, err = r.SMembers(ctx, k).Result()
		case "zset":
			v, err = r.ZRangeWithScores(ctx, k, 0, -1).Result()
		case "stream":
			v, err = r.XRange(ctx, k, "-", "+").Result()
		default:
			t.Fatalf("%s: type %s not dumped", k, typ)
		}
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, "%s %v\n", k, v)
	}
	return out.String()
}
