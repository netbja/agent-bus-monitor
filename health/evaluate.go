package health

import (
	"fmt"
	"os"
	"path/filepath"
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
	if legacy { // warn until v0.19.1 ships the rotation command: there is nothing to run yet
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
