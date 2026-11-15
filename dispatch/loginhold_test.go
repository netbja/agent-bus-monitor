package dispatch

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/loginexpiry"
)

// dispatchCmds are the cmd entries the dispatcher wrote to target, by text.
func (w *credWorld) dispatchCmds(t *testing.T, target string) []string {
	t.Helper()
	var out []string
	for _, m := range entries(t, w.r, w.b, "cmd") {
		if m.Values["from"] == "dispatch" && m.Values["target"] == target {
			out = append(out, m.Values["command"].(string))
		}
	}
	return out
}

func (w *credWorld) marks(t *testing.T) map[string]string {
	t.Helper()
	m, err := w.b.QuotaMarks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// An expired login (r9 §3.6): the hold field (its value the expiry), what
// coder had in {p}:held, one notice for the person, one directive to master,
// no wake; a second pass repeats nothing. A later expiry after /login
// releases it: the resume directive, then both fields deleted.
func TestLoginExpiryHoldsAndReleases(t *testing.T) {
	w := newCredWorld(t, map[string]bus.AgentSnapshot{"coder": claudeSnap("/c"), "master": claudeSnap("/m")})
	ctx := context.Background()
	w.r.HSet(ctx, bus.BoardKey(w.b.Project()), "wiki-3", `{"owner":"coder","state":"accepted","branch":"feat/wiki-3"}`,
		"wiki-4", `{"owner":"coder","state":"done"}`, "other", `{"owner":"coder2","state":"accepted"}`)
	report, err := w.b.Report(ctx, "coder", "", "step 2 done")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.Report(ctx, "coder2", "", "not coder's"); err != nil {
		t.Fatal(err)
	}
	w.expiry["/c"], w.expiry["/m"] = -time.Hour, 30*24*time.Hour
	exp := w.now.Add(-time.Hour).Truncate(time.Millisecond)
	ms := strconv.FormatInt(exp.UnixMilli(), 10)
	w.pass(t)
	w.l.loginDirs["/c"] = loginDir{}
	w.pass(t)

	if got := w.marks(t)[bus.LoginHoldField("coder")]; got != ms {
		t.Fatalf("hold field %q, want %s", got, ms)
	}
	held, err := w.b.Held(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h := held["coder"]; h.Provider != "login" || h.Since == 0 || strings.Join(h.Tasks, ",") != "wiki-3" ||
		strings.Join(h.Branches, ",") != "feat/wiki-3" || h.Report != report || len(held) != 1 {
		t.Fatalf("held %+v", held)
	}
	at := exp.UTC().Format("Mon 15:04")
	ns := personNotices(t, w.b)
	if len(ns) != 1 || ns[0].Message != "coder's Claude login expired "+at+": run /login in its pane. Its work is held (master told)." {
		t.Fatalf("notices %+v", ns)
	}
	if got := w.dispatchCmds(t, "master"); len(got) != 1 ||
		got[0] != "coder is held (Claude login expired "+at+", released after /login): accepted wiki-3 (feat/wiki-3). Reassign it, or wait." {
		t.Fatalf("master's directives %q", got)
	}
	snap := w.l.snaps["coder"]
	if !w.l.heldBy("coder", &snap) || w.l.heldBy("master", &snap) {
		t.Fatal("coder not held, or master held")
	}
	if w.l.recipientCannotAct("coder") != "" {
		t.Fatal("master cannot act although only coder is held")
	}

	// /login: a later expiry, read within the minute.
	w.expiry["/c"] = 30 * 24 * time.Hour
	w.l.loginDirs["/c"] = loginDir{readAt: time.Now().Add(-61 * time.Second), expiry: exp}
	w.pass(t)
	if m := w.marks(t); m[bus.LoginHoldField("coder")] != "" {
		t.Fatalf("still held: %v", m)
	}
	if held, _ := w.b.Held(ctx); len(held) != 0 {
		t.Fatalf("held after release %+v", held)
	}
	if got := w.dispatchCmds(t, "coder"); len(got) != 1 ||
		got[0] != "Your Claude login is renewed. Work interrupted by it: wiki-3. Resume it where you stopped, then report as usual." {
		t.Fatalf("coder's directives %q", got)
	}
	if w.l.heldBy("coder", &snap) {
		t.Fatal("still held after release")
	}
	w.pass(t) // nothing more
	if len(w.dispatchCmds(t, "coder")) != 1 || len(w.dispatchCmds(t, "master")) != 1 || len(personNotices(t, w.b)) != 1 {
		t.Fatal("a later pass repeated something")
	}
}

// Held, not released: the same expiry again, an older one, or a read that
// fails keep the hold.
func TestLoginHoldStaysWithoutALaterExpiry(t *testing.T) {
	w := newCredWorld(t, map[string]bus.AgentSnapshot{"coder": claudeSnap("/c")})
	w.expiry["/c"] = -time.Hour
	w.pass(t)
	for _, step := range []func(){
		func() { w.expiry["/c"] = -time.Hour },
		func() { w.expiry["/c"] = -2 * time.Hour },
		func() { w.reason["/c"] = "no file" },
	} {
		step()
		w.l.loginDirs["/c"] = loginDir{}
		w.pass(t)
		if w.marks(t)[bus.LoginHoldField("coder")] == "" {
			t.Fatal("released without a later expiry")
		}
	}
	if got := w.dispatchCmds(t, "coder"); len(got) != 0 {
		t.Fatalf("resumed: %q", got)
	}
}

// An agent with no work is held, with no {p}:held record; master is told it
// had nothing. Master's own expiry: no directive to itself, and the
// person's notice names master without "(master told)".
func TestLoginHoldWithoutWorkAndOfMaster(t *testing.T) {
	w := newCredWorld(t, map[string]bus.AgentSnapshot{"coder": claudeSnap("/c"), "master": claudeSnap("/m")})
	w.expiry["/c"], w.expiry["/m"] = -time.Hour, -time.Hour
	at := w.now.Add(-time.Hour).Truncate(time.Millisecond).UTC().Format("Mon 15:04")
	w.pass(t)
	if held, _ := w.b.Held(context.Background()); len(held) != 0 {
		t.Fatalf("held records without work: %+v", held)
	}
	m := w.marks(t)
	if m[bus.LoginHoldField("coder")] == "" || m[bus.LoginHoldField("master")] == "" {
		t.Fatalf("marks %v", m)
	}
	if got := w.dispatchCmds(t, "master"); len(got) != 1 || got[0] != "coder is held (Claude login expired "+at+", released after /login): nothing accepted." {
		t.Fatalf("master's directives %q", got)
	}
	want := map[string]bool{
		"coder's Claude login expired " + at + ": run /login in its pane. Its work is held (master told).": true,
		"master's Claude login expired " + at + ": run /login in its pane. Its work is held.":              true,
	}
	ns := personNotices(t, w.b)
	for _, n := range ns {
		delete(want, n.Message)
	}
	if len(ns) != 2 || len(want) != 0 {
		t.Fatalf("notices %+v, missing %v", ns, want)
	}
	snap := w.l.snaps["master"]
	if w.l.recipientCannotAct("coder") == "" || !w.l.heldBy("master", &snap) {
		t.Fatal("a login-held master can act")
	}
}

// A hold left by an earlier dispatcher (or an agent no longer read) still
// holds its agent: the field, not memory, is the hold.
func TestLoginHoldReadFromMarks(t *testing.T) {
	w := newCredWorld(t, map[string]bus.AgentSnapshot{"coder": claudeSnap("/c")})
	w.reason["/c"] = "no file" // its read fails: nothing releases it
	if err := w.b.FencedQuotaSet(context.Background(), "holder", bus.LoginHoldField("coder"), "1794777000000"); err != nil {
		t.Fatal(err)
	}
	w.pass(t)
	snap := w.l.snaps["coder"]
	if !w.l.heldBy("coder", &snap) {
		t.Fatal("a hold field holds nobody")
	}
}

// The dispatcher's wake skip learns the login hold: with only
// hold-login:coder set, coder is never prompted while kimi, on the same
// provider, is.
func TestRunLoginHeldAgentIsNotWoken(t *testing.T) {
	b, r := dialRun(t)
	cleanupQuota(t, b)
	registerAs(t, b, "coder", "p-coder", "anthropic")
	registerAs(t, b, "kimi", "p-kimi", "anthropic")
	r.HSet(context.Background(), bus.QuotaKey(b.Project()), bus.LoginHoldField("coder"), "1794777000000")
	send(t, b, "coder", "build it")
	send(t, b, "kimi", "check it")
	rt := newFakeRT(alwaysReady)
	cfg := fastConfig()
	cfg.Login = LoginConfig{Checked: func(string) bool { return true }, // read, unknown: the hold stands
		Credentials: func(string, time.Time) (time.Time, loginexpiry.Reason) { return time.Time{}, loginexpiry.NoFile }}
	start(t, b, rt, cfg)
	waitFor(t, 3*time.Second, "kimi woken", func() bool {
		for _, p := range rt.Prompts() {
			if p.Pane == "p-kimi" {
				return true
			}
		}
		return false
	})
	time.Sleep(200 * time.Millisecond)
	for _, p := range rt.Prompts() {
		if p.Pane == "p-coder" {
			t.Fatal("a login-held agent was prompted")
		}
	}
}

// A login-held agent swapped to a client with no login expiry (foureyes on
// 3a): its credentials are no longer read, so nothing would ever release it.
// Once the swap is over, the hold and its record go, with no resume (the
// swap resumes it); an agent with no snapshot keeps its hold (forget clears it).
func TestLoginHoldReleasedWhenTheClientChanges(t *testing.T) {
	w := newCredWorld(t, map[string]bus.AgentSnapshot{"coder": claudeSnap("/c")})
	ctx := context.Background()
	w.r.HSet(ctx, bus.BoardKey(w.b.Project()), "wiki-3", `{"owner":"coder","state":"accepted"}`)
	w.expiry["/c"] = -time.Hour
	w.pass(t)
	if held, _ := w.b.Held(ctx); len(held) != 1 {
		t.Fatalf("not held: %+v", held)
	}
	if err := w.b.FencedQuotaSet(ctx, "holder", bus.LoginHoldField("ghost"), "1"); err != nil {
		t.Fatal(err)
	}
	w.l.snaps = map[string]bus.AgentSnapshot{"coder": {Client: "codex", Provider: "openai"}}
	// While the swap runs, the hold waits for it.
	if err := w.r.Set(ctx, bus.SwapKey(w.b.Project(), "coder"), "intent", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	w.pass(t)
	if w.marks(t)[bus.LoginHoldField("coder")] == "" {
		t.Fatal("released during the swap")
	}
	w.r.Del(ctx, bus.SwapKey(w.b.Project(), "coder"))
	w.pass(t)
	m := w.marks(t)
	if m[bus.LoginHoldField("coder")] != "" || m[bus.LoginHoldField("ghost")] == "" {
		t.Fatalf("marks %v", m)
	}
	if held, _ := w.b.Held(ctx); len(held) != 0 {
		t.Fatalf("held record kept: %+v", held)
	}
	if got := w.dispatchCmds(t, "coder"); len(got) != 0 {
		t.Fatalf("a resume after a client swap: %q", got)
	}
	snap := w.l.snaps["coder"]
	if w.l.heldBy("coder", &snap) {
		t.Fatal("still held in the loop")
	}
}
