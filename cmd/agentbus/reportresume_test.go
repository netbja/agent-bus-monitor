package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// resumeFixture: a Claude manifest with an own and a fallback profile, the
// environment the launcher gives Claude inside herdr, and recording deps.
type resumeFixture struct {
	t        *testing.T
	manifest string
	env      []string
	spawned  [][]string
}

func newResumeFixture(t *testing.T) *resumeFixture {
	t.Helper()
	dir := t.TempDir()
	manifest := filepath.Join(dir, "roles.toml")
	body := "schema_version = 2\n[defaults]\nprofile = \"main\"\n[profiles.main]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\n" +
		"[profiles.alt]\nclient = \"claude-code\"\nprovider = \"other\"\nmodel = \"m2\"\n" +
		"[roles.coder]\ntier = \"boot\"\nfallback_profiles = [\"alt\"]\n"
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return &resumeFixture{t: t, manifest: manifest, env: []string{
		"HERDR_ENV=1", "HERDR_PANE_ID=w1:p2", "HERDR_BIN_PATH=/opt/herdr", "AGENT_BUS_AGENT=coder",
		"AGENT_BUS_PROJECT=demo", "AGENT_BUS_PROFILE=main", "ROLES_TOML=" + manifest, "XDG_STATE_HOME=" + filepath.Join(dir, "state"),
	}}
}

const sessionStart = `{"session_id":"5f0c-11","transcript_path":"/x.jsonl","hook_event_name":"SessionStart","source":"startup"}`

func (f *resumeFixture) hook(stdin string, env ...string) string {
	var out, errw bytes.Buffer
	f.spawned = nil
	code := reportResume([]string{"/r/scripts/agent-launch"}, append(append([]string{}, f.env...), env...),
		strings.NewReader(stdin), &out, &errw, func(argv []string) error {
			f.spawned = append(f.spawned, argv)
			return nil
		})
	if code != 0 || out.Len() != 0 {
		f.t.Fatalf("hook exit %d, stdout %q (stderr %q): a SessionStart hook prints nothing and never fails", code, out.String(), errw.String())
	}
	return errw.String()
}

func TestReportResumeSpawnsTheSenderWithTheLaunchCommand(t *testing.T) {
	f := newResumeFixture(t)
	f.hook(sessionStart)
	if len(f.spawned) != 1 {
		t.Fatalf("spawned %q", f.spawned)
	}
	want := []string{"roles", "report-resume-send", filepath.Join(filepath.Dir(f.manifest), "state", "agentbus", "demo", "resume.log"),
		"/opt/herdr", "w1:p2", "--", "env", "ROLES_TOML=" + f.manifest, "/r/scripts/agent-launch", "coder", "demo", "--resume", "5f0c-11"}
	if got := f.spawned[0]; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("sender argv\n got %q\nwant %q", got, want)
	}
	// A swapped role resumes on the profile it runs on.
	f.hook(sessionStart, "AGENT_BUS_PROFILE=alt")
	if got := strings.Join(f.spawned[0], " "); !strings.HasSuffix(got, "coder demo --profile alt --resume 5f0c-11") {
		t.Fatalf("swapped: %s", got)
	}
}

func TestReportResumeGate(t *testing.T) {
	f := newResumeFixture(t)
	for _, v := range []string{"HERDR_ENV", "HERDR_PANE_ID", "HERDR_BIN_PATH", "AGENT_BUS_AGENT", "AGENT_BUS_PROJECT", "ROLES_TOML"} {
		f.hook(sessionStart, v+"=")
		if len(f.spawned) != 0 {
			t.Errorf("without %s: spawned %q", v, f.spawned)
		}
	}
	f.hook(sessionStart, "HERDR_ENV=0")
	for name, stdin := range map[string]string{
		"other event": strings.Replace(sessionStart, "SessionStart", "Stop", 1),
		"no session":  `{"hook_event_name":"SessionStart"}`,
		"subagent":    strings.Replace(sessionStart, `"source"`, `"agent_id":"a1","source"`, 1),
		"not json":    "nope",
		"apostrophe":  strings.Replace(sessionStart, "5f0c-11", "5f0c'11", 1),
	} {
		f.hook(stdin)
		if len(f.spawned) != 0 {
			t.Errorf("%s: spawned %q", name, f.spawned)
		}
	}
	f.hook(sessionStart, "AGENT_BUS_AGENT=nobody") // not a role of the manifest
	if len(f.spawned) != 0 {
		t.Errorf("unknown role: spawned %q", f.spawned)
	}
}

// fakeResumeHerdr answers `resume_not_accepted` the first n calls, then accepts;
// it records each call's arguments, one line per call.
func fakeResumeHerdr(t *testing.T, refusals int, code string) (bin, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	bin = filepath.Join(dir, "herdr")
	script := "#!/bin/sh\necho \"$@\" >> " + calls + "\nn=$(wc -l < " + calls + ")\n" +
		"if [ \"$n\" -le " + strconv.Itoa(refusals) + " ]; then echo '{\"error\":{\"code\":\"" + code + "\",\"message\":\"no\"}}'; exit 1; fi\necho '{\"result\":{}}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func sendResume(t *testing.T, herdr string, budget time.Duration) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "state", "resume.log")
	clock := time.Unix(1_800_000_000, 0)
	d := senderDeps{
		now:   func() time.Time { return clock },
		sleep: func(d time.Duration) { clock = clock.Add(d) },
		run: func(ctx context.Context, bin string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, bin, args...).CombinedOutput()
		},
		budget: budget,
	}
	argv := []string{"env", "ROLES_TOML=/r/roles.toml", "/r/scripts/agent-launch", "coder", "demo", "--resume", "5f0c-11"}
	reportResumeSend(append([]string{log, herdr, "w1:p2", "--"}, argv...), d)
	if st, err := os.Stat(log); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("log %v %v", st, err)
	}
	b, _ := os.ReadFile(log)
	return string(b)
}

func TestReportResumeSendRetriesUntilAccepted(t *testing.T) {
	herdr, calls := fakeResumeHerdr(t, 2, "resume_not_accepted")
	logged := sendResume(t, herdr, 15*time.Second)
	b, _ := os.ReadFile(calls)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 {
		t.Fatalf("calls %q", lines)
	}
	var seqs []string
	for _, l := range lines {
		f := strings.Fields(l)
		i := indexOfStr(f, "--seq")
		if !strings.HasPrefix(l, "pane report-agent-session w1:p2 --source agentbus --agent claude --seq ") || i < 0 ||
			!strings.HasSuffix(l, " -- env ROLES_TOML=/r/roles.toml /r/scripts/agent-launch coder demo --resume 5f0c-11") {
			t.Fatalf("call %q", l)
		}
		seqs = append(seqs, f[i+1])
	}
	if !(seqs[0] < seqs[1] && seqs[1] < seqs[2]) || len(seqs[0]) != len(seqs[2]) {
		t.Fatalf("seq not increasing: %v", seqs)
	}
	if !strings.Contains(logged, "accepted") || !strings.Contains(logged, "--resume 5f0c-11") {
		t.Fatalf("log %q", logged)
	}
}

func TestReportResumeSendGivesUpAfterItsBudget(t *testing.T) {
	herdr, calls := fakeResumeHerdr(t, 9, "resume_not_accepted")
	logged := sendResume(t, herdr, 3*time.Second)
	b, _ := os.ReadFile(calls)
	if n := strings.Count(string(b), "\n"); n != 4 { // at 0, 1, 2 and 3 s
		t.Fatalf("%d calls within a 3 s budget", n)
	}
	if !strings.Contains(logged, "not accepted") {
		t.Fatalf("log %q", logged)
	}
}

func TestReportResumeSendStopsOnAnotherError(t *testing.T) {
	herdr, calls := fakeResumeHerdr(t, 9, "pane_not_found")
	logged := sendResume(t, herdr, 15*time.Second)
	b, _ := os.ReadFile(calls)
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Fatalf("%d calls after a final error", n)
	}
	if !strings.Contains(logged, "pane_not_found") {
		t.Fatalf("log %q", logged)
	}
}

func TestRolesLaunchProfileAndResume(t *testing.T) {
	f := newResumeFixture(t)
	repo := filepath.Dir(f.manifest)
	if err := os.MkdirAll(filepath.Join(repo, "roles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "roles", "coder.md"), []byte("coder prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{"ROLES_TOML=" + f.manifest, "AGENTBUS_REPO=" + repo, "AGENT_LAUNCH_DRYRUN=1"}
	code, out, errOut := roles(env, "launch", "coder", "demo", "--profile", "alt", "--resume", "5f0c-11")
	if code != 0 || !strings.Contains(out, "claude --model m2 --resume 5f0c-11") {
		t.Fatalf("launch --profile --resume = %d %q %q", code, out, errOut)
	}
}

func indexOfStr(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}
