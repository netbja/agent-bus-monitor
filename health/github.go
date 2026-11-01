package health

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// GitHubNeitherWarnAfter is how long both transports may fail before the
// github check warns: a network outage is not the person's to fix here, so it
// reads unknown until then (the person's answer, busmon pages question 6).
const GitHubNeitherWarnAfter = time.Hour

// ProbeResult is a probe's network result: GitHub answered (whatever the
// answer), the network failed, or the probe could not judge.
type ProbeResult int

const (
	ProbeNotRun  ProbeResult = iota // ssh or git could not run, or an answer not recognised
	ProbeReached                    // it connected and GitHub answered
	ProbeFailed                     // no DNS, no route, timeout, refused, reset, TLS failure
)

// Probe is one transport's result and a fixed phrase saying why; the phrase
// never repeats the probe's output.
type Probe struct {
	Result ProbeResult
	Note   string
}

var gitHubRemote = regexp.MustCompile(`(?i)^(?:(?:https?|ssh|git)://(?:[^@/]*@)?github\.com(?::[0-9]+)?/|[^@/:]+@github\.com:)([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// GitHubRepo is owner/repo when url is a remote on github.com, in any of the
// forms Git accepts (scp-like, ssh://, https://); anything else is no GitHub
// remote. Credentials in the URL are never part of the result.
func GitHubRepo(url string) (string, bool) {
	m := gitHubRemote.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil || m[2] == "" {
		return "", false
	}
	return m[1] + "/" + m[2], true
}

type probePattern struct {
	text   string
	result ProbeResult
	note   string
}

// The answers each probe recognises, read in order; matched in lower case.
var sshPatterns = []probePattern{
	{"successfully authenticated", ProbeReached, "answered, authenticated"},
	{"permission denied", ProbeReached, "answered, authentication unknown (the service's own identities)"},
	{"host key verification failed", ProbeReached, "answered, its host key not known to the service"},
	{"could not resolve hostname", ProbeFailed, "name not resolved"},
	{"timed out", ProbeFailed, "connection timed out"},
	{"connection refused", ProbeFailed, "connection refused"},
	{"network is unreachable", ProbeFailed, "network unreachable"},
	{"no route to host", ProbeFailed, "network unreachable"},
	{"connection reset", ProbeFailed, "connection reset"},
	{"connection closed", ProbeFailed, "connection reset"},
}

var httpsPatterns = []probePattern{
	{"could not resolve host", ProbeFailed, "name not resolved"},
	{"timed out", ProbeFailed, "connection timed out"},
	{"failed to connect", ProbeFailed, "could not connect"},
	{"couldn't connect", ProbeFailed, "could not connect"},
	{"connection refused", ProbeFailed, "could not connect"},
	{"connection reset", ProbeFailed, "connection reset"},
	{"ssl", ProbeFailed, "TLS failure"},
	{"tls", ProbeFailed, "TLS failure"},
	{"could not read username", ProbeReached, "answered, authentication unknown (no credential helper, no prompt)"},
	{"authentication failed", ProbeReached, "answered, authentication unknown (no credential helper, no prompt)"},
	{"not found", ProbeReached, "answered: repository not found (a wrong or renamed path, or private)"},
	{"returned error", ProbeReached, "answered with an HTTP error"},
}

func classify(ran, timedOut bool, out string, patterns []probePattern) Probe {
	if !ran {
		return Probe{ProbeNotRun, "could not run"}
	}
	if timedOut {
		return Probe{ProbeFailed, "connection timed out (deadline)"}
	}
	low := strings.ToLower(out)
	for _, p := range patterns {
		if strings.Contains(low, p.text) {
			return Probe{p.result, p.note}
		}
	}
	return Probe{ProbeNotRun, "unrecognised answer"}
}

// ClassifySSH reads `ssh -o BatchMode=yes -o ConnectTimeout=5 -T
// git@github.com`: ran is false when ssh could not run, timedOut when the
// check's deadline killed it, out its combined output.
func ClassifySSH(ran, timedOut bool, exit int, out string) Probe {
	return classify(ran, timedOut, out, sshPatterns)
}

// ClassifyHTTPS reads `git -c credential.helper= ls-remote <https URL>` with
// no prompt: exit 0 is an answer whatever refs come back; any other answer
// from GitHub is reached, only a network error failed.
func ClassifyHTTPS(ran, timedOut bool, exit int, out string) Probe {
	if ran && !timedOut && exit == 0 {
		return Probe{ProbeReached, "answered"}
	}
	return classify(ran, timedOut, out, httpsPatterns)
}

// GitHubNeither is the case whose duration the runner tracks: both
// transports failed on the network.
func GitHubNeither(ssh, https Probe) bool {
	return ssh.Result == ProbeFailed && https.Result == ProbeFailed
}

const gitHubLimits = "Limits: HTTPS judges reachability only (no credential helper, no prompt); SSH uses the service's own identities; both down warns only after an hour."

// EvaluateGitHub (busmon pages, section 6) says only what was measured:
// reachable by both (ok), by one (warn), by neither (unknown, a warn once
// neitherFor reaches GitHubNeitherWarnAfter), not judged (unknown). Never a
// fail: neither probe can prove that GitHub refuses this host. repo is the
// remote's owner/repo, "" for no GitHub remote.
func EvaluateGitHub(repo string, ssh, https Probe, neitherFor time.Duration) Outcome {
	if repo == "" {
		return Outcome{Status: Unknown, Detail: "no GitHub remote: not run"}
	}
	status, summary := Unknown, ""
	switch {
	case ssh.Result == ProbeReached && https.Result == ProbeReached:
		status, summary = OK, "reachable by SSH and HTTPS"
	case ssh.Result == ProbeReached && https.Result == ProbeFailed:
		status, summary = Warn, "reachable by SSH only"
	case ssh.Result == ProbeFailed && https.Result == ProbeReached:
		status, summary = Warn, "reachable by HTTPS only"
	case GitHubNeither(ssh, https):
		summary = "reachable by neither: offline or blocked"
		if neitherFor >= GitHubNeitherWarnAfter {
			status, summary = Warn, summary+fmt.Sprintf(" for %s", neitherFor.Truncate(time.Minute))
		}
	case ssh.Result == ProbeNotRun && https.Result == ProbeNotRun:
		summary = "not judged: neither probe could judge"
	case ssh.Result == ProbeNotRun:
		summary = "SSH not judged"
	default:
		summary = "HTTPS not judged"
	}
	return Outcome{Status: status, Detail: truncateDetail(fmt.Sprintf("%s (%s); SSH: %s; HTTPS: %s. %s", summary, repo, ssh.Note, https.Note, gitHubLimits))}
}
