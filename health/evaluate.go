package health

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	RunawaySilentAfter  = 30 * time.Minute
	RunawayFastGrowth   = 100_000
	RunawayGrowthSpan   = 10 * time.Minute
	PendingWarnAfter    = 10 * time.Minute
	PendingFailAfter    = time.Hour
	LagWarnAt           = int64(100)
	LagFailAt           = int64(1000)
	QuotaStaleAfter     = 30 * time.Minute
	HerdrProbeFailAfter = 3
)

type Outcome struct {
	Status Status
	Detail string
	// Reason is a fixed code for the case, set by the evaluator, never read
	// from the detail: a warn or fail whose code is in the check's Reasons is
	// the person's (Definition.PlaceOf), with that code's command.
	Reason string
}

type RunawayThresholds struct {
	SilentAfter     time.Duration
	FastGrowth      int
	GrowthSpan      time.Duration
	RepeatedScreens int
}

func DefaultRunawayThresholds() RunawayThresholds {
	return RunawayThresholds{SilentAfter: RunawaySilentAfter, FastGrowth: RunawayFastGrowth,
		GrowthSpan: RunawayGrowthSpan, RepeatedScreens: 4}
}

type ServiceEvidence struct {
	Known, Installed, Active bool
	Failures                 map[string]int // per kind: its failed runs in its own budget's window
	RecentError              bool
	WaitingGit               bool // missions runs but waits for its directory to become a Git repository
}

// ServiceBudgetStarts is service.BudgetStarts (held equal by a test): this
// many failures in the window spend a kind's restart budget, and it gives up.
const ServiceBudgetStarts = 5

// The services check's person-only cases (foureyes, reviewer-codex): an
// inactive service or a recent error is sentinel's, these are not. A spent
// budget is per kind, so its code names the kind its command restarts.
const (
	ReasonBudgetSpent         = "budget-spent"          // every kind's: restart them all
	ReasonBudgetSpentDispatch = "budget-spent-dispatch" // sentinel's restarts draw on the same budget
	ReasonBudgetSpentMissions = "budget-spent-missions"
	ReasonWaitingGit          = "waiting-git" // only the person runs git init
)

func EvaluateServices(e ServiceEvidence) Outcome {
	if !e.Known {
		return Outcome{Status: Unknown, Detail: "service manager or state unavailable"}
	}
	if !e.Installed {
		return Outcome{Status: OK, Detail: "no managed services installed"}
	}
	// A spent budget first: its service gave up and exits, so it also reads
	// inactive (reviewer-codex). Each kind against its own budget, never a sum.
	var spent []string
	most := 0
	for _, kind := range []string{"dispatch", "missions"} {
		n := e.Failures[kind]
		most = max(most, n)
		if n >= ServiceBudgetStarts {
			spent = append(spent, kind)
		}
	}
	switch {
	case len(spent) == 2:
		return Outcome{Status: Fail, Detail: "restart budget spent: dispatch, missions", Reason: ReasonBudgetSpent}
	case len(spent) == 1 && spent[0] == "dispatch":
		return Outcome{Status: Fail, Detail: "restart budget spent: dispatch", Reason: ReasonBudgetSpentDispatch}
	case len(spent) == 1:
		return Outcome{Status: Fail, Detail: "restart budget spent: missions", Reason: ReasonBudgetSpentMissions}
	}
	if !e.Active {
		return Outcome{Status: Fail, Detail: "an installed service is inactive or failed"}
	}
	if most >= ServiceBudgetStarts-1 || e.RecentError {
		return Outcome{Status: Warn, Detail: "service had a recent error or restart budget is low"}
	}
	if e.WaitingGit {
		return Outcome{Status: Warn, Detail: "missions waits for a Git repository in the project directory", Reason: ReasonWaitingGit}
	}
	return Outcome{Status: OK, Detail: "installed services are active"}
}

type IdentityEvidence struct {
	Known, Active, Verified, Contradiction bool
}

func EvaluateIdentity(e IdentityEvidence) Outcome {
	if e.Contradiction {
		return Outcome{Status: Fail, Detail: "verified process identity conflicts with its pane"}
	}
	if !e.Known {
		return Outcome{Status: Unknown, Detail: "agent identity could not be checked"}
	}
	if !e.Active {
		return Outcome{Status: OK, Detail: "no active identity conflict"}
	}
	if !e.Verified {
		return Outcome{Status: Warn, Detail: "active agent identity is not fully verified"}
	}
	return Outcome{Status: OK, Detail: "active agent identity is verified"}
}

type LaunchEvidence struct {
	Known, Launcher, RequiredSettings, NamedIdentity bool
}

func EvaluateLaunch(e LaunchEvidence) Outcome {
	if !e.Known {
		return Outcome{Status: Unknown, Detail: "client or role could not be established"}
	}
	if !e.Launcher || !e.RequiredSettings || !e.NamedIdentity {
		return Outcome{Status: Warn, Detail: "launcher, required settings, or named identity evidence is missing"}
	}
	return Outcome{Status: OK, Detail: "launcher and identity evidence is present"}
}

func EvaluateModelDrift(verdicts []string, known bool) Outcome {
	if !known {
		return Outcome{Status: Unknown, Detail: "model catalogue or doctor result unavailable"}
	}
	incomplete := false
	for _, v := range verdicts {
		switch v {
		case "drift", "unknown model", "gone", "newer":
			return Outcome{Status: Warn, Detail: "roles doctor reports model drift"}
		case "not reported", "unknown":
			incomplete = true
		}
	}
	if incomplete {
		return Outcome{Status: Unknown, Detail: "roles doctor could not verify every launched model"}
	}
	return Outcome{Status: OK, Detail: "no model drift reported"}
}

func EvaluateRunaway(silent time.Duration, contextGrowth int, growthSpan time.Duration, repeatedScreens int) Outcome {
	return EvaluateRunawayKnown(silent, contextGrowth, growthSpan, repeatedScreens, true)
}

func EvaluateRunawayKnown(silent time.Duration, contextGrowth int, growthSpan time.Duration, repeatedScreens int, known bool) Outcome {
	return EvaluateRunawayWithThresholds(silent, contextGrowth, growthSpan, repeatedScreens, known, DefaultRunawayThresholds())
}

func EvaluateRunawayWithThresholds(silent time.Duration, contextGrowth int, growthSpan time.Duration, repeatedScreens int, known bool, thresholds RunawayThresholds) Outcome {
	if !known {
		return Outcome{Status: Unknown, Detail: "working agent progress evidence is incomplete"}
	}
	if thresholds.SilentAfter <= 0 || thresholds.FastGrowth <= 0 || thresholds.GrowthSpan <= 0 || thresholds.RepeatedScreens <= 0 {
		return Outcome{Status: Unknown, Detail: "runaway thresholds are invalid"}
	}
	if silent >= thresholds.SilentAfter || (contextGrowth >= thresholds.FastGrowth && growthSpan <= thresholds.GrowthSpan && repeatedScreens >= thresholds.RepeatedScreens) {
		return Outcome{Status: Warn, Detail: "working agent has little visible progress"}
	}
	return Outcome{Status: OK, Detail: "working agents show recent progress"}
}

type DeliveryEvidence struct {
	Known      bool
	Pending    time.Duration
	Lag        int64
	HasPending bool
}

func EvaluateDelivery(e DeliveryEvidence) Outcome {
	if !e.Known {
		return Outcome{Status: Unknown, Detail: "consumer-group delivery state unavailable"}
	}
	if e.HasPending && e.Pending >= PendingFailAfter || e.Lag >= LagFailAt {
		return Outcome{Status: Fail, Detail: "cmd delivery has a long-pending entry or high lag"}
	}
	if e.HasPending && e.Pending >= PendingWarnAfter || e.Lag >= LagWarnAt {
		return Outcome{Status: Warn, Detail: "cmd delivery has an aged pending entry or elevated lag"}
	}
	return Outcome{Status: OK, Detail: "cmd delivery is within thresholds"}
}

func EvaluateQuotaFreshness(expected bool, known bool, observedAt int64, now time.Time) Outcome {
	if !expected {
		return Outcome{Status: OK, Detail: "no quota provider is configured"}
	}
	if !known || observedAt <= 0 {
		return Outcome{Status: Unknown, Detail: "quota reading is unavailable"}
	}
	if now.Sub(time.UnixMilli(observedAt)) > QuotaStaleAfter {
		return Outcome{Status: Warn, Detail: "quota reading is older than 30 minutes"}
	}
	return Outcome{Status: OK, Detail: "quota reading is fresh"}
}

type SecretPath struct{ Path, Kind string }

func IsSecretLikePath(path string) (string, bool) {
	base := filepath.Base(path)
	lower := strings.ToLower(base)
	if lower == ".env" || strings.HasPrefix(lower, ".env.") && lower != ".env.example" {
		return "environment file", true
	}
	if strings.HasSuffix(lower, ".pem") {
		return "private-key file", true
	}
	if strings.HasSuffix(lower, ".key") {
		return "key file", true
	}
	if strings.HasPrefix(lower, "id_rsa") || strings.HasPrefix(lower, "id_ed25519") {
		return "SSH private-key file", true
	}
	if strings.Contains(lower, "keypair") && strings.HasSuffix(lower, ".json") {
		return "keypair JSON", true
	}
	if strings.HasPrefix(lower, "wallet") && strings.HasSuffix(lower, ".json") {
		return "wallet JSON", true
	}
	return "", false
}

func EvaluateSecretPaths(paths []string, known bool) Outcome {
	if !known {
		return Outcome{Status: Unknown, Detail: "tracked file paths could not be read"}
	}
	for _, path := range paths {
		if kind, ok := IsSecretLikePath(path); ok {
			return Outcome{Status: Fail, Detail: fmt.Sprintf("tracked %s path: %s", kind, filepath.ToSlash(path))}
		}
	}
	return Outcome{Status: OK, Detail: "no tracked secret-like path found"}
}

func EvaluateRedis(local, legacy bool) Outcome {
	if !local {
		return Outcome{Status: Unknown, Detail: "Redis binding or credential source is not locally verifiable"}
	}
	if legacy { // a warning: the person rotates (./setup.sh rotate-redis-password), never sentinel
		return Outcome{Status: Warn, Detail: "Redis still uses the historical default password"}
	}
	return Outcome{Status: OK, Detail: "Redis is loopback-bound and uses an explicit credential source"}
}

func EvaluateFileMode(path string, statErr error, mode os.FileMode) Outcome {
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return Outcome{Status: Fail, Detail: "installed service environment file is missing"}
		}
		return Outcome{Status: Unknown, Detail: "service environment permissions could not be checked"}
	}
	if mode.Perm() != 0o600 {
		return Outcome{Status: Fail, Detail: "service environment file mode is not 0600"}
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return Outcome{Status: Unknown, Detail: "service environment directory permissions could not be checked"}
	}
	if parent.Mode().Perm() != 0o700 {
		return Outcome{Status: Fail, Detail: "service environment directory mode is not 0700"}
	}
	return Outcome{Status: OK, Detail: "service environment permissions are private"}
}

func EvaluateVersions(known bool, problems int) Outcome {
	if !known {
		return Outcome{Status: Unknown, Detail: "version inventory is incomplete"}
	}
	if problems > 0 {
		return Outcome{Status: Warn, Detail: "one or more installed components need review"}
	}
	return Outcome{Status: OK, Detail: "installed versions and skill links are current"}
}

func EvaluateHerdrProbe(available, parseable bool, consecutiveFailures int) Outcome {
	if !available || !parseable {
		return Outcome{Status: Unknown, Detail: "herdr probe is unavailable or unparseable"}
	}
	if consecutiveFailures >= HerdrProbeFailAfter {
		return Outcome{Status: Fail, Detail: "herdr command probe repeatedly failed"}
	}
	if consecutiveFailures > 0 {
		return Outcome{Status: Warn, Detail: "herdr command probe failed once; retrying"}
	}
	return Outcome{Status: OK, Detail: "herdr command probe succeeded"}
}

// ClientEvidence is one verified agent's client, as the dispatcher last
// observed it (client reload spec): Fresh, the observation is recent;
// Judged, the dispatcher could tell (a launch fingerprint or the Claude Code
// layout); Outdated, a newer client is installed.
type ClientEvidence struct {
	Agent                      string
	Fresh, Judged, Outdated    bool
	Client, Running, Installed string
}

// EvaluateClientUpdates: warn when an agent runs an outdated client, ok when
// every freshly observed agent was judged and none is, unknown when nothing
// is fresh or an agent cannot be judged. Warn wakes nobody (v0.19).
func EvaluateClientUpdates(ev []ClientEvidence) Outcome {
	var fresh, unjudged []string
	byVersion := map[string]int{}
	var keys []string
	for _, e := range ev {
		if !e.Fresh {
			continue
		}
		fresh = append(fresh, e.Agent)
		switch {
		case !e.Judged:
			unjudged = append(unjudged, e.Agent)
		case e.Outdated:
			k := e.Client + " " + e.Running + "→" + e.Installed
			if byVersion[k] == 0 {
				keys = append(keys, k)
			}
			byVersion[k]++
		}
	}
	if len(fresh) == 0 {
		return Outcome{Status: Unknown, Detail: "no fresh pane observation (the dispatch service observes)"}
	}
	if len(keys) > 0 {
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%d agent(s): %s", byVersion[k], k))
		}
		return Outcome{Status: Warn, Detail: strings.Join(parts, "; ")}
	}
	if len(unjudged) > 0 {
		sort.Strings(unjudged)
		return Outcome{Status: Unknown, Detail: "cannot tell the client version of: " + strings.Join(unjudged, ", ")}
	}
	return Outcome{Status: OK, Detail: "every observed agent runs its installed client"}
}

// ContextEvidence is one verified agent's context fill as the dispatcher last
// published it (context-alert): Known, a reading that is neither missing nor
// aged.
type ContextEvidence struct {
	Agent string
	Pct   float64
	Known bool
}

// EvaluateContext: warn when a known fill is at or above threshold (the
// dispatcher has told those agents to hand off), ok when every known fill is
// below it, unknown when none is known. threshold 0 (the alert off) is ok.
func EvaluateContext(ev []ContextEvidence, threshold float64) Outcome {
	if threshold <= 0 {
		return Outcome{Status: OK, Detail: "context alert off (AGENT_BUS_CONTEXT_ALERT=0)"}
	}
	known := 0
	var high []ContextEvidence
	for _, e := range ev {
		if !e.Known {
			continue
		}
		known++
		if e.Pct >= threshold {
			high = append(high, e)
		}
	}
	if known == 0 {
		return Outcome{Status: Unknown, Detail: "no context fill read (Claude Code agents, through the dispatch service)"}
	}
	if len(high) == 0 {
		return Outcome{Status: OK, Detail: fmt.Sprintf("%d agent(s) below %.0f%%", known, threshold)}
	}
	sort.Slice(high, func(i, j int) bool { return high[i].Agent < high[j].Agent })
	parts := make([]string, 0, len(high))
	for _, e := range high {
		parts = append(parts, fmt.Sprintf("%s %.0f%%", e.Agent, e.Pct))
	}
	return Outcome{Status: Warn, Detail: truncateDetail(fmt.Sprintf("at or above %.0f%%: %s", threshold, strings.Join(parts, ", ")))}
}

// detailMax is SafeText's bound on a detail, in runes.
const detailMax = 300

// truncateDetail keeps a detail within SafeText's bound.
func truncateDetail(s string) string {
	if r := []rune(s); len(r) > detailMax {
		return string(r[:detailMax-3]) + "..."
	}
	return s
}

// RedisLatestMaxAge is how old the last successful check of the latest Redis
// release may be before the comparison is unknown: two daily checks missed.
const RedisLatestMaxAge = 48 * time.Hour

var redisVersionPattern = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)$`)

// EvaluateRedisVersion (redis-watch) compares the broker's version with the
// latest release of its own major (latest: major -> version, from a recent
// check). Warn when behind
// in that major; a newer major is never a warning, only named in the detail
// (changing major is the person's decision); unknown when anything is not
// known.
func EvaluateRedisVersion(running string, latest map[int]string, foundAt, now time.Time) Outcome {
	have, ok := parseRedisVersion(running)
	if !ok {
		return Outcome{Status: Unknown, Detail: "broker version unavailable"}
	}
	if foundAt.IsZero() || now.Sub(foundAt) > RedisLatestMaxAge || foundAt.After(now.Add(5*time.Minute)) {
		return Outcome{Status: Unknown, Detail: "latest Redis release not checked in the last 48 hours"}
	}
	top, newest := 0, ""
	for major, v := range latest {
		if _, ok := parseRedisVersion(v); !ok {
			return Outcome{Status: Unknown, Detail: "latest Redis release unreadable"}
		}
		if major > top {
			top, newest = major, v
		}
	}
	same, ok := latest[have[0]]
	if !ok {
		return Outcome{Status: Unknown, Detail: fmt.Sprintf("no recent release of Redis %d found", have[0])}
	}
	want, _ := parseRedisVersion(same)
	newer := ""
	if top > have[0] {
		newer = fmt.Sprintf("; Redis %s is out; this broker runs %d.x; changing major is your decision", newest, have[0])
	}
	if versionBelow(have, want) {
		return Outcome{Status: Warn, Detail: fmt.Sprintf("Redis %s runs; %s is the latest %d.x%s", running, same, have[0], newer)}
	}
	if have == want {
		return Outcome{Status: OK, Detail: fmt.Sprintf("Redis %s is the latest %d.x%s", running, have[0], newer)}
	}
	return Outcome{Status: OK, Detail: fmt.Sprintf("Redis %s is not behind the latest %d.x (%s)%s", running, have[0], same, newer)}
}

func versionBelow(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func parseRedisVersion(s string) ([3]int, bool) {
	var v [3]int
	m := redisVersionPattern.FindStringSubmatch(s)
	if m == nil {
		return v, false
	}
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}
