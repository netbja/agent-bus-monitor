package health

import (
	"strings"
	"testing"
	"time"
)

// The repository a remote names on github.com, from every URL form Git
// accepts; any other host is no GitHub remote.
func TestGitHubRepo(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:netbja/agent-bus-monitor.git":          "netbja/agent-bus-monitor",
		"git@github.com:netbja/agent-bus-monitor":              "netbja/agent-bus-monitor",
		"ssh://git@github.com/netbja/agent-bus-monitor.git":    "netbja/agent-bus-monitor",
		"ssh://git@github.com:22/netbja/agent-bus-monitor":     "netbja/agent-bus-monitor",
		"https://github.com/netbja/agent-bus-monitor.git":      "netbja/agent-bus-monitor",
		"https://github.com/netbja/agent-bus-monitor/":         "netbja/agent-bus-monitor",
		"https://user:tok@github.com/netbja/agent-bus-monitor": "netbja/agent-bus-monitor",
		"HTTPS://GitHub.com/netbja/a.b_c-d":                    "netbja/a.b_c-d",
		"git@gitlab.com:netbja/agent-bus-monitor.git":          "",
		"https://github.com.evil.example/netbja/x":             "",
		"https://evil.example/github.com/netbja/x":             "",
		"https://github.com/netbja":                            "",
		"https://github.com/netbja/x/y":                        "",
		"/srv/git/agent-bus-monitor.git":                       "",
		"":                                                     "",
	} {
		got, ok := GitHubRepo(url)
		if got != want || ok != (want != "") {
			t.Errorf("GitHubRepo(%q) = %q, %v; want %q", url, got, ok, want)
		}
	}
}

// SSH is reached when GitHub answered, authenticated or not; failed only on a
// network error; not run when ssh could not run.
func TestClassifySSH(t *testing.T) {
	for _, c := range []struct {
		name          string
		ran, timedOut bool
		exit          int
		out           string
		want          ProbeResult
		note          string
	}{
		{"authenticated", true, false, 1, "Hi netbja! You've successfully authenticated, but GitHub does not provide shell access.", ProbeReached, "authenticated"},
		{"permission denied", true, false, 255, "git@github.com: Permission denied (publickey).", ProbeReached, "authentication unknown"},
		{"host key unknown", true, false, 255, "Host key verification failed.", ProbeReached, "host key"},
		{"no dns", true, false, 255, "ssh: Could not resolve hostname github.com: Temporary failure in name resolution", ProbeFailed, "name"},
		{"timeout", true, false, 255, "ssh: connect to host github.com port 22: Connection timed out", ProbeFailed, "timed out"},
		{"refused", true, false, 255, "ssh: connect to host github.com port 22: Connection refused", ProbeFailed, "refused"},
		{"unreachable", true, false, 255, "ssh: connect to host github.com port 22: Network is unreachable", ProbeFailed, "unreachable"},
		{"reset", true, false, 255, "kex_exchange_identification: read: Connection reset by peer", ProbeFailed, "reset"},
		{"deadline", true, true, -1, "", ProbeFailed, "timed out"},
		{"not run", false, false, -1, "", ProbeNotRun, "could not run"},
		{"unclassified", true, false, 255, "something else entirely", ProbeNotRun, "unrecognised"},
	} {
		p := ClassifySSH(c.ran, c.timedOut, c.exit, c.out)
		if p.Result != c.want || !strings.Contains(p.Note, c.note) {
			t.Errorf("%s: %v %q, want %v containing %q", c.name, p.Result, p.Note, c.want, c.note)
		}
		if strings.Contains(p.Note, "netbja") || strings.Contains(p.Note, "github.com:") {
			t.Errorf("%s: the note repeats the probe's output: %q", c.name, p.Note)
		}
	}
}

// HTTPS is reached on any answer from GitHub (refs, an empty list, an
// authentication refusal, a 404); failed only on a network error.
func TestClassifyHTTPS(t *testing.T) {
	for _, c := range []struct {
		name          string
		ran, timedOut bool
		exit          int
		out           string
		want          ProbeResult
		note          string
	}{
		{"refs", true, false, 0, "abc\tHEAD\n", ProbeReached, "answered"},
		{"empty repository", true, false, 0, "", ProbeReached, "answered"},
		{"auth required", true, false, 128, "fatal: could not read Username for 'https://github.com': terminal prompts disabled", ProbeReached, "authentication unknown"},
		{"auth failed", true, false, 128, "remote: Invalid username or password.\nfatal: Authentication failed for 'https://github.com/o/r/'", ProbeReached, "authentication unknown"},
		{"not found", true, false, 128, "remote: Repository not found.\nfatal: repository 'https://github.com/o/r/' not found", ProbeReached, "not found"},
		{"http error", true, false, 128, "fatal: unable to access 'https://github.com/o/r/': The requested URL returned error: 503", ProbeReached, "HTTP error"},
		{"no dns", true, false, 128, "fatal: unable to access 'https://github.com/o/r/': Could not resolve host: github.com", ProbeFailed, "name"},
		{"connect", true, false, 128, "fatal: unable to access 'https://github.com/o/r/': Failed to connect to github.com port 443 after 3 ms: Couldn't connect to server", ProbeFailed, "connect"},
		{"timeout", true, false, 128, "fatal: unable to access 'https://github.com/o/r/': Connection timed out after 10001 milliseconds", ProbeFailed, "timed out"},
		{"reset", true, false, 128, "fatal: unable to access 'https://github.com/o/r/': Recv failure: Connection reset by peer", ProbeFailed, "reset"},
		{"tls", true, false, 128, "fatal: unable to access 'https://github.com/o/r/': SSL certificate problem: unable to get local issuer certificate", ProbeFailed, "TLS"},
		{"deadline", true, true, -1, "", ProbeFailed, "timed out"},
		{"not run", false, false, -1, "", ProbeNotRun, "could not run"},
		{"unclassified", true, false, 128, "fatal: something new", ProbeNotRun, "unrecognised"},
	} {
		p := ClassifyHTTPS(c.ran, c.timedOut, c.exit, c.out)
		if p.Result != c.want || !strings.Contains(p.Note, c.note) {
			t.Errorf("%s: %v %q, want %v containing %q", c.name, p.Result, p.Note, c.want, c.note)
		}
	}
}

// The table of the spec (busmon pages, section 6): never a fail, a warn when
// only one transport reaches GitHub, unknown when neither does until an hour
// has passed, and a probe that could not run never lets ok be claimed.
func TestEvaluateGitHub(t *testing.T) {
	reached := Probe{Result: ProbeReached, Note: "authenticated"}
	failed := Probe{Result: ProbeFailed, Note: "connection timed out"}
	notRun := Probe{Result: ProbeNotRun, Note: "could not run"}
	for _, c := range []struct {
		name       string
		repo       string
		ssh, https Probe
		neither    time.Duration
		want       Status
		detail     string
	}{
		{"both", "o/r", reached, reached, 0, OK, "reachable by SSH and HTTPS"},
		{"ssh only", "o/r", reached, failed, 0, Warn, "reachable by SSH only"},
		{"https only", "o/r", failed, reached, 0, Warn, "reachable by HTTPS only"},
		{"neither, fresh", "o/r", failed, failed, 59 * time.Minute, Unknown, "reachable by neither: offline or blocked"},
		{"neither, an hour", "o/r", failed, failed, time.Hour, Warn, "reachable by neither: offline or blocked"},
		{"ssh not run, https reached", "o/r", notRun, reached, 0, Unknown, "SSH not judged"},
		{"https not run, ssh reached", "o/r", reached, notRun, 0, Unknown, "HTTPS not judged"},
		{"ssh not run, https failed", "o/r", notRun, failed, 2 * time.Hour, Unknown, "SSH not judged"},
		{"neither ran", "o/r", notRun, notRun, 0, Unknown, "not judged"},
		{"no github remote", "", reached, reached, 0, Unknown, "no GitHub remote"},
	} {
		out := EvaluateGitHub(c.repo, c.ssh, c.https, c.neither)
		if out.Status != c.want || !strings.Contains(out.Detail, c.detail) {
			t.Errorf("%s: %s %q, want %s containing %q", c.name, out.Status, out.Detail, c.want, c.detail)
		}
		if !SafeText(out.Detail) {
			t.Errorf("%s: detail not safe text: %q", c.name, out.Detail)
		}
		if c.repo != "" && !strings.Contains(out.Detail, "HTTPS judges reachability only") {
			t.Errorf("%s: the detail does not state its limits: %q", c.name, out.Detail)
		}
	}
}

// GitHubNeither is what starts the hour: both probes failed on the network.
func TestGitHubNeither(t *testing.T) {
	r, f, n := Probe{Result: ProbeReached}, Probe{Result: ProbeFailed}, Probe{Result: ProbeNotRun}
	if !GitHubNeither(f, f) || GitHubNeither(r, f) || GitHubNeither(f, r) || GitHubNeither(n, f) || GitHubNeither(f, n) {
		t.Error("GitHubNeither must hold only when both probes failed")
	}
}

// The check is for the person when it warns, never fails, and runs every
// 15 minutes within 10 seconds.
func TestGitHubDefinition(t *testing.T) {
	d, ok := ByID("github")
	if !ok {
		t.Fatal("github is not in the catalogue")
	}
	if d.Interval != 15*time.Minute || d.Deadline != 10*time.Second {
		t.Errorf("interval %v deadline %v", d.Interval, d.Deadline)
	}
	if d.Place(Warn) != PlaceForYou || d.Command != "git ls-remote <your remote URL>" {
		t.Errorf("warn place %q command %q", d.Place(Warn), d.Command)
	}
	if !strings.Contains(d.Remediation, "Check your git remote and credentials; try: git ls-remote <your remote URL>") {
		t.Errorf("remediation %q", d.Remediation)
	}
}
