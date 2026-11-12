package rolecfg

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every client has its resume form recorded as data (self-repair step 5,
// herdr-exit §4.9), and only a client whose session id source was verified
// on this host resumes: Claude Code (its SessionStart hook) and Grok (the
// launcher chooses the id, verified with grok 1.0.77 on 2026-11-12). Codex,
// Kimi and agy start fresh until their probe runs.
func TestClientResumeForms(t *testing.T) {
	verified := map[string]bool{}
	for _, c := range Clients {
		if c.Resume == "" {
			t.Errorf("%s: no resume form", c.Client)
		}
		if c.ResumeSource == "" {
			t.Errorf("%s: no session id source", c.Client)
		}
		verified[c.Client] = ResumeVerified(c.Client)
	}
	want := map[string]bool{"claude-code": true, "codex": false, "kimi-cli": false, "grok": true, "agy": false}
	for client, v := range want {
		if verified[client] != v {
			t.Errorf("ResumeVerified(%s) = %v, want %v", client, verified[client], v)
		}
	}
	if ResumeVerified("") || ResumeVerified("extra") {
		t.Error("an unknown client resumes")
	}
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// fakeBindBin is an agentbus that records its arguments, for the launcher's
// bind inside herdr.
func fakeBindBin(t *testing.T, f *launchFixture) (bin, log string) {
	t.Helper()
	log = filepath.Join(f.root, "bind.log")
	bin = filepath.Join(f.bin, "fake-agentbus")
	must(t, os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" >> '"+log+"'\n"), 0o755))
	return bin, log
}

func herdrEnv(bin string) []string {
	return []string{"HERDR_ENV=1", "HERDR_PANE_ID=w1:p2", "AGENTBUS_BIN=" + bin}
}

// A fresh Grok launch chooses its session id (--session-id, a new UUID each
// launch) and the bind records it with the process it proves: the launcher
// is the source, no hook is needed.
func TestLaunchGrokChoosesItsSessionID(t *testing.T) {
	f := newLaunchFixture(t)
	bin, log := fakeBindBin(t, f)
	m := f.config("grok", "", "", "")
	var ids []string
	for range 2 {
		if _, e, err := f.launch(m, "coder", herdrEnv(bin)...); err != nil {
			t.Fatal(err, e)
		}
		i := indexOf(f.execed, "--session-id")
		if i < 0 || !uuidV4.MatchString(f.execed[i+1]) {
			t.Fatalf("argv = %q", f.execed)
		}
		if indexOf(f.execed, "--resume") >= 0 {
			t.Fatalf("a fresh launch resumes: %q", f.execed)
		}
		ids = append(ids, f.execed[i+1])
	}
	if ids[0] == ids[1] {
		t.Fatalf("two launches share session %s", ids[0])
	}
	binds := strings.Split(strings.TrimSpace(string(mustRead(t, log))), "\n")
	if len(binds) != 2 {
		t.Fatalf("binds = %q", binds)
	}
	for i, b := range binds {
		if !strings.Contains(b, "bind coder --pid ") || !strings.HasSuffix(b, " --session "+ids[i]) {
			t.Fatalf("bind %d = %q, want its session %s", i, b, ids[i])
		}
	}
}

// A resumed Grok launch passes --resume and binds the resumed session.
func TestLaunchGrokResumes(t *testing.T) {
	f := newLaunchFixture(t)
	bin, log := fakeBindBin(t, f)
	const id = "1a60c66b-d4e8-4a0e-8017-d50248645b38"
	if _, e, err := f.launchWithResume(f.config("grok", "", "", ""), "coder", id, herdrEnv(bin)...); err != nil {
		t.Fatal(err, e)
	}
	if i := indexOf(f.execed, "--resume"); i < 0 || f.execed[i+1] != id || indexOf(f.execed, "--session-id") >= 0 {
		t.Fatalf("argv = %q", f.execed)
	}
	if b := strings.TrimSpace(string(mustRead(t, log))); !strings.HasSuffix(b, " --session "+id) {
		t.Fatalf("bind = %q", b)
	}
}

// Outside herdr nothing is bound, and a dry run names the id it would choose.
func TestLaunchGrokSessionWithoutHerdr(t *testing.T) {
	f := newLaunchFixture(t)
	if _, e, err := f.launch(f.config("grok", "", "", ""), "coder"); err != nil || indexOf(f.execed, "--session-id") < 0 {
		t.Fatalf("argv = %q, %v %s", f.execed, err, e)
	}
	out, _, err := f.launch(f.config("grok", "", "", ""), "coder", "AGENT_LAUNCH_DRYRUN=1")
	if err != nil || !strings.Contains(out, "--session-id '<new session id>'") {
		t.Fatalf("dry run = %q, %v", out, err)
	}
}

// Only Grok is bound with a session: Claude Code proves its own through the
// SessionStart hook, and an unverified client has none to bind.
func TestOnlyGrokBindsASession(t *testing.T) {
	for _, client := range []string{"claude-code", "codex"} {
		f := newLaunchFixture(t)
		bin, log := fakeBindBin(t, f)
		if _, e, err := f.launch(f.config(client, "", "", ""), "coder", herdrEnv(bin)...); err != nil {
			t.Fatal(client, err, e)
		}
		if b := string(mustRead(t, log)); !strings.Contains(b, "bind coder") || strings.Contains(b, "--session") {
			t.Fatalf("%s bind = %q", client, b)
		}
	}
}

// An unverified client is refused a resume: it starts fresh, never with
// another flag that guesses (--continue picks the folder's latest session).
func TestUnverifiedClientsRefuseResume(t *testing.T) {
	for _, client := range []string{"codex", "kimi-cli"} {
		f := newLaunchFixture(t)
		_, _, err := f.launchWithResume(f.config(client, "", "", ""), "coder", "session-123")
		if err == nil || !strings.Contains(err.Error(), "session resume is not verified for "+client) {
			t.Fatalf("%s resume: %v", client, err)
		}
	}
}
