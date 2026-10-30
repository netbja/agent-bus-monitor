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
	RecentFailures           int
	RecentError              bool
	WaitingGit               bool // missions runs but waits for its directory to become a Git repository
}

func EvaluateServices(e ServiceEvidence) Outcome {
	if !e.Known {
		return Outcome{Unknown, "service manager or state unavailable"}
	}
	if !e.Installed {
		return Outcome{OK, "no managed services installed"}
	}
	if !e.Active {
		return Outcome{Fail, "an installed service is inactive or failed"}
	}
	if e.RecentFailures >= 5 {
		return Outcome{Fail, "service restart budget is spent"}
	}
	if e.RecentFailures >= 4 || e.RecentError {
		return Outcome{Warn, "service had a recent error or restart budget is low"}
	}
	if e.WaitingGit {
		return Outcome{Warn, "missions waits for a Git repository in the project directory"}
	}
	return Outcome{OK, "installed services are active"}
}

type IdentityEvidence struct {
	Known, Active, Verified, Contradiction bool
}

func EvaluateIdentity(e IdentityEvidence) Outcome {
	if e.Contradiction {
		return Outcome{Fail, "verified process identity conflicts with its pane"}
	}
	if !e.Known {
		return Outcome{Unknown, "agent identity could not be checked"}
	}
	if !e.Active {
		return Outcome{OK, "no active identity conflict"}
	}
	if !e.Verified {
		return Outcome{Warn, "active agent identity is not fully verified"}
	}
	return Outcome{OK, "active agent identity is verified"}
}

type LaunchEvidence struct {
	Known, Launcher, RequiredSettings, NamedIdentity bool
}

func EvaluateLaunch(e LaunchEvidence) Outcome {
	if !e.Known {
		return Outcome{Unknown, "client or role could not be established"}
	}
	if !e.Launcher || !e.RequiredSettings || !e.NamedIdentity {
		return Outcome{Warn, "launcher, required settings, or named identity evidence is missing"}
	}
	return Outcome{OK, "launcher and identity evidence is present"}
}

func EvaluateModelDrift(verdicts []string, known bool) Outcome {
	if !known {
		return Outcome{Unknown, "model catalogue or doctor result unavailable"}
	}
	incomplete := false
	for _, v := range verdicts {
		switch v {
		case "drift", "unknown model", "gone", "newer":
			return Outcome{Warn, "roles doctor reports model drift"}
		case "not reported", "unknown":
			incomplete = true
		}
	}
	if incomplete {
		return Outcome{Unknown, "roles doctor could not verify every launched model"}
	}
	return Outcome{OK, "no model drift reported"}
}

func EvaluateRunaway(silent time.Duration, contextGrowth int, growthSpan time.Duration, repeatedScreens int) Outcome {
	return EvaluateRunawayKnown(silent, contextGrowth, growthSpan, repeatedScreens, true)
}

func EvaluateRunawayKnown(silent time.Duration, contextGrowth int, growthSpan time.Duration, repeatedScreens int, known bool) Outcome {
	return EvaluateRunawayWithThresholds(silent, contextGrowth, growthSpan, repeatedScreens, known, DefaultRunawayThresholds())
}

func EvaluateRunawayWithThresholds(silent time.Duration, contextGrowth int, growthSpan time.Duration, repeatedScreens int, known bool, thresholds RunawayThresholds) Outcome {
	if !known {
		return Outcome{Unknown, "working agent progress evidence is incomplete"}
	}
	if thresholds.SilentAfter <= 0 || thresholds.FastGrowth <= 0 || thresholds.GrowthSpan <= 0 || thresholds.RepeatedScreens <= 0 {
		return Outcome{Unknown, "runaway thresholds are invalid"}
	}
	if silent >= thresholds.SilentAfter || (contextGrowth >= thresholds.FastGrowth && growthSpan <= thresholds.GrowthSpan && repeatedScreens >= thresholds.RepeatedScreens) {
		return Outcome{Warn, "working agent has little visible progress"}
	}
	return Outcome{OK, "working agents show recent progress"}
}

type DeliveryEvidence struct {
	Known      bool
	Pending    time.Duration
	Lag        int64
	HasPending bool
}

func EvaluateDelivery(e DeliveryEvidence) Outcome {
	if !e.Known {
		return Outcome{Unknown, "consumer-group delivery state unavailable"}
	}
	if e.HasPending && e.Pending >= PendingFailAfter || e.Lag >= LagFailAt {
		return Outcome{Fail, "cmd delivery has a long-pending entry or high lag"}
	}
	if e.HasPending && e.Pending >= PendingWarnAfter || e.Lag >= LagWarnAt {
		return Outcome{Warn, "cmd delivery has an aged pending entry or elevated lag"}
	}
	return Outcome{OK, "cmd delivery is within thresholds"}
}

func EvaluateQuotaFreshness(expected bool, known bool, observedAt int64, now time.Time) Outcome {
	if !expected {
		return Outcome{OK, "no quota provider is configured"}
	}
	if !known || observedAt <= 0 {
		return Outcome{Unknown, "quota reading is unavailable"}
	}
	if now.Sub(time.UnixMilli(observedAt)) > QuotaStaleAfter {
		return Outcome{Warn, "quota reading is older than 30 minutes"}
	}
	return Outcome{OK, "quota reading is fresh"}
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
		return Outcome{Unknown, "tracked file paths could not be read"}
	}
	for _, path := range paths {
		if kind, ok := IsSecretLikePath(path); ok {
			return Outcome{Fail, fmt.Sprintf("tracked %s path: %s", kind, filepath.ToSlash(path))}
		}
	}
	return Outcome{OK, "no tracked secret-like path found"}
}

func EvaluateRedis(local, legacy bool) Outcome {
	if !local {
		return Outcome{Unknown, "Redis binding or credential source is not locally verifiable"}
	}
	if legacy { // a warning: the person rotates (./setup.sh rotate-redis-password), never sentinel
		return Outcome{Warn, "Redis still uses the historical default password"}
	}
	return Outcome{OK, "Redis is loopback-bound and uses an explicit credential source"}
}

func EvaluateFileMode(path string, statErr error, mode os.FileMode) Outcome {
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return Outcome{Fail, "installed service environment file is missing"}
		}
		return Outcome{Unknown, "service environment permissions could not be checked"}
	}
	if mode.Perm() != 0o600 {
		return Outcome{Fail, "service environment file mode is not 0600"}
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return Outcome{Unknown, "service environment directory permissions could not be checked"}
	}
	if parent.Mode().Perm() != 0o700 {
		return Outcome{Fail, "service environment directory mode is not 0700"}
	}
	return Outcome{OK, "service environment permissions are private"}
}

func EvaluateVersions(known bool, problems int) Outcome {
	if !known {
		return Outcome{Unknown, "version inventory is incomplete"}
	}
	if problems > 0 {
		return Outcome{Warn, "one or more installed components need review"}
	}
	return Outcome{OK, "installed versions and skill links are current"}
}

func EvaluateHerdrProbe(available, parseable bool, consecutiveFailures int) Outcome {
	if !available || !parseable {
		return Outcome{Unknown, "herdr probe is unavailable or unparseable"}
	}
	if consecutiveFailures >= HerdrProbeFailAfter {
		return Outcome{Fail, "herdr command probe repeatedly failed"}
	}
	if consecutiveFailures > 0 {
		return Outcome{Warn, "herdr command probe failed once; retrying"}
	}
	return Outcome{OK, "herdr command probe succeeded"}
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
		return Outcome{Unknown, "no fresh pane observation (the dispatch service observes)"}
	}
	if len(keys) > 0 {
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%d agent(s): %s", byVersion[k], k))
		}
		return Outcome{Warn, strings.Join(parts, "; ")}
	}
	if len(unjudged) > 0 {
		sort.Strings(unjudged)
		return Outcome{Unknown, "cannot tell the client version of: " + strings.Join(unjudged, ", ")}
	}
	return Outcome{OK, "every observed agent runs its installed client"}
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
		return Outcome{OK, "context alert off (AGENT_BUS_CONTEXT_ALERT=0)"}
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
		return Outcome{Unknown, "no context fill read (Claude Code agents, through the dispatch service)"}
	}
	if len(high) == 0 {
		return Outcome{OK, fmt.Sprintf("%d agent(s) below %.0f%%", known, threshold)}
	}
	sort.Slice(high, func(i, j int) bool { return high[i].Agent < high[j].Agent })
	parts := make([]string, 0, len(high))
	for _, e := range high {
		parts = append(parts, fmt.Sprintf("%s %.0f%%", e.Agent, e.Pct))
	}
	return Outcome{Warn, truncateDetail(fmt.Sprintf("at or above %.0f%%: %s", threshold, strings.Join(parts, ", ")))}
}

// truncateDetail keeps a detail within SafeText's bound.
func truncateDetail(s string) string {
	if r := []rune(s); len(r) > 300 {
		return string(r[:297]) + "..."
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
		return Outcome{Unknown, "broker version unavailable"}
	}
	if foundAt.IsZero() || now.Sub(foundAt) > RedisLatestMaxAge || foundAt.After(now.Add(5*time.Minute)) {
		return Outcome{Unknown, "latest Redis release not checked in the last 48 hours"}
	}
	top, newest := 0, ""
	for major, v := range latest {
		if _, ok := parseRedisVersion(v); !ok {
			return Outcome{Unknown, "latest Redis release unreadable"}
		}
		if major > top {
			top, newest = major, v
		}
	}
	same, ok := latest[have[0]]
	if !ok {
		return Outcome{Unknown, fmt.Sprintf("no recent release of Redis %d found", have[0])}
	}
	want, _ := parseRedisVersion(same)
	newer := ""
	if top > have[0] {
		newer = fmt.Sprintf("; Redis %s is out; this broker runs %d.x; changing major is your decision", newest, have[0])
	}
	if versionBelow(have, want) {
		return Outcome{Warn, fmt.Sprintf("Redis %s runs; %s is the latest %d.x%s", running, same, have[0], newer)}
	}
	if have == want {
		return Outcome{OK, fmt.Sprintf("Redis %s is the latest %d.x%s", running, have[0], newer)}
	}
	return Outcome{OK, fmt.Sprintf("Redis %s is not behind the latest %d.x (%s)%s", running, have[0], same, newer)}
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
