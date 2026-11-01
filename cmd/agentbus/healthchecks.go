package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
)

const (
	herdrMinimumVersion = "0.1.0"
	updateCacheMaxAge   = 24 * time.Hour
)

type usageBaseline struct {
	at     time.Time
	tokens int
}

// realHealthConfig builds read-only checks for one dispatcher. The scheduler
// runs these outside reconciliation and fences every resulting write.
func realHealthConfig(b *bus.Bus, rt dispatch.Runtime, paths service.Paths) dispatch.HealthConfig {
	manager, _ := service.Detect(runtime.GOOS, service.CommandRunner(service.ManagerBin(runtime.GOOS, os.Getenv)), os.Getenv)
	return dispatch.HealthConfig{Checks: realHealthChecks(b, rt, manager, paths), DailyWake: true,
		UsageRefresh: usageRefresh(b), ModelsRefresh: modelsRefresh(b), RedisRefresh: redisRefresh(b)}
}

func healthTask(id string, run func(context.Context) (health.Status, string, error)) dispatch.HealthCheck {
	return healthOutcomeTask(id, func(ctx context.Context) (health.Outcome, error) {
		status, detail, err := run(ctx)
		return health.Outcome{Status: status, Detail: detail}, err
	})
}

// healthOutcomeTask is a check whose evaluator's whole outcome is stored,
// its Reason included (services: a spent budget or a Git wait is the person's).
func healthOutcomeTask(id string, run func(context.Context) (health.Outcome, error)) dispatch.HealthCheck {
	d, ok := health.ByID(id)
	if !ok {
		panic("missing health definition: " + id)
	}
	return dispatch.HealthCheck{Definition: d, Run: run}
}

func realHealthChecks(b *bus.Bus, rt dispatch.Runtime, manager service.Manager, paths service.Paths) []dispatch.HealthCheck {
	var usageMu sync.Mutex
	baselines := map[string]usageBaseline{}
	runawayThresholds, runawayThresholdsOK := runawayThresholdsFromEnv(os.Getenv)
	var probeMu sync.Mutex
	probeFailures := 0
	return []dispatch.HealthCheck{
		healthOutcomeTask("services", func(ctx context.Context) (health.Outcome, error) {
			installed, err := b.ServicesInstalled(ctx)
			if err != nil {
				return health.Outcome{Status: health.Unknown, Detail: "service installation state unavailable"}, err
			}
			inst, records, err := b.ServiceStates(ctx)
			if err != nil {
				return health.Outcome{Status: health.Unknown, Detail: "service state unavailable"}, err
			}
			redisNow, err := b.ServerTime(ctx)
			if err != nil {
				return health.Outcome{Status: health.Unknown, Detail: "server time unavailable"}, err
			}
			if !installed || inst == nil || len(inst.Kinds) == 0 {
				out := health.EvaluateServices(health.ServiceEvidence{Known: true})
				return out, nil
			}
			if manager == nil {
				return health.Outcome{Status: health.Unknown, Detail: "service manager unavailable"}, errors.New("service manager unavailable")
			}
			active, failures, recentError, waitingGit := true, 0, false, false
			for _, kind := range inst.Kinds {
				if !service.ValidKind(kind) {
					return health.Outcome{Status: health.Unknown, Detail: "service registry contains an unsupported kind"}, errors.New("unsupported service kind")
				}
				state, err := manager.State(b.Project(), kind)
				if err != nil {
					return health.Outcome{Status: health.Unknown, Detail: "service manager state unavailable"}, err
				}
				active = active && state.Installed && state.Active
				count, err := (service.Budget{Path: paths.BudgetFile(b.Project(), kind)}).FailureCountChecked(time.Now())
				if err != nil {
					return health.Outcome{Status: health.Unknown, Detail: "service restart budget unavailable"}, err
				}
				failures += count
				if rec := records[kind]; rec.Error != "" && rec.At > 0 && redisNow.Sub(time.UnixMilli(rec.At)) <= time.Hour {
					recentError = true // never retain or display the error text
				}
				if rec := records[kind]; rec.State == gitWaitState && rec.At > 0 && redisNow.Sub(time.UnixMilli(rec.At)) <= time.Minute {
					waitingGit = true
				}
			}
			out := health.EvaluateServices(health.ServiceEvidence{Known: true, Installed: true, Active: active,
				RecentFailures: failures, RecentError: recentError, WaitingGit: waitingGit})
			return out, nil
		}),
		healthTask("agent-identity", func(ctx context.Context) (health.Status, string, error) {
			snaps, err := b.Agents(ctx)
			if err != nil {
				return health.Unknown, "agent snapshots unavailable", err
			}
			if len(snaps) == 0 {
				out := health.EvaluateIdentity(health.IdentityEvidence{Known: true})
				return out.Status, "no registered agents", nil
			}
			unknown, unverified, conflict, active := false, false, false, false
			for _, snap := range snaps {
				if !healthActive(snap.State) {
					continue
				}
				active = true
				if snap.PID == 0 || snap.PIDStart == 0 {
					unverified = true
					continue
				}
				start, err := dispatch.ProcStart(snap.PID)
				if err != nil {
					unknown = true // an exited process is not positive evidence of PID reuse
					continue
				}
				if start != snap.PIDStart {
					conflict = true
					continue
				}
				if snap.Pane == "" || rt == nil {
					unverified = true
					continue
				}
				in := rt.Inspect(ctx, snap.Pane)
				if ctx.Err() != nil || in.Err != nil || in.Gone {
					unknown = true
					continue
				}
				foundPID := false
				for _, pid := range in.PIDs {
					if pid != snap.PID {
						continue
					}
					foundPID = true
					start, hasStart := in.Starts[pid]
					if !hasStart {
						unknown = true
					} else if start != snap.PIDStart {
						conflict = true
					}
				}
				if !foundPID {
					conflict = true
				}
			}
			out := health.EvaluateIdentity(health.IdentityEvidence{Known: !unknown, Active: active,
				Verified: !unverified && !unknown, Contradiction: conflict})
			return out.Status, out.Detail, nil
		}),
		healthTask("agent-launch", func(ctx context.Context) (health.Status, string, error) {
			snaps, err := b.Agents(ctx)
			if err != nil {
				return health.Unknown, "agent snapshots unavailable", err
			}
			manifestPath, err := manifestPath(os.Environ())
			if err != nil {
				return health.Unknown, "role manifest unavailable", err
			}
			manifest, err := rolecfg.Load(manifestPath)
			if err != nil {
				return health.Unknown, "role manifest could not be validated", err
			}
			known, launcher, settings, named := false, true, true, true
			for agent, snap := range snaps {
				if !healthActive(snap.State) {
					continue
				}
				known = true
				if snap.PID <= 0 {
					launcher, named = false, false
					continue
				}
				role, resolveErr := manifest.Resolve(agent)
				if resolveErr != nil {
					return health.Unknown, "active agent role could not be established", resolveErr
				}
				hasSettings, argErr := procHasSettingsArg(snap.PID)
				if argErr != nil {
					return health.Unknown, "active process launch evidence unavailable", errors.New("process evidence unavailable")
				}
				rolePresent, roleEvidence, nameEvidence, evidenceErr := procEnvIdentity(snap.PID, agent, agent)
				if evidenceErr != nil {
					return health.Unknown, "active process identity evidence unavailable", evidenceErr
				}
				// AGENT_BUS_AGENT is the launcher's required role identity. Newer
				// launchers may also expose AGENT_BUS_ROLE; if present it must agree.
				if !nameEvidence || rolePresent && !roleEvidence {
					launcher = false
				}
				if !nameEvidence {
					named = false
				}
				if role.Client == "claude-code" && !role.ToolRules.Empty() && !hasSettings {
					settings = false
				}
			}
			if !known {
				return health.Unknown, "no active role process to inspect", errors.New("no active process")
			}
			out := health.EvaluateLaunch(health.LaunchEvidence{Known: true, Launcher: launcher,
				RequiredSettings: settings, NamedIdentity: named})
			return out.Status, out.Detail, nil
		}),
		healthTask("model-drift", func(ctx context.Context) (health.Status, string, error) {
			rows, err := doctorRows(ctx, b, realDoctorDeps(os.Environ()))
			if err != nil {
				return health.Unknown, "roles doctor could not complete", err
			}
			verdicts := make([]string, 0, len(rows))
			for _, row := range rows {
				verdicts = append(verdicts, row.Verdict)
			}
			out := health.EvaluateModelDrift(verdicts, true)
			return out.Status, out.Detail, nil
		}),
		healthTask("client-updates", func(ctx context.Context) (health.Status, string, error) {
			now, err := b.ServerTime(ctx)
			if err != nil {
				return health.Unknown, "server time unavailable", err
			}
			snaps, err := b.Agents(ctx)
			if err != nil {
				return health.Unknown, "agent snapshots unavailable", err
			}
			observed, err := b.Observed(ctx)
			if err != nil {
				return health.Unknown, "pane observations unavailable", err
			}
			out := health.EvaluateClientUpdates(clientEvidence(snaps, observed, now.UnixMilli()))
			return out.Status, out.Detail, nil
		}),
		healthTask("context", func(ctx context.Context) (health.Status, string, error) {
			fills, err := b.Context(ctx)
			if err != nil {
				return health.Unknown, "context fills unavailable", err
			}
			out := health.EvaluateContext(contextEvidence(fills), bus.ContextAlert(os.Getenv))
			return out.Status, out.Detail, nil
		}),
		healthTask("runaway", func(ctx context.Context) (health.Status, string, error) {
			now, err := b.ServerTime(ctx)
			if err != nil {
				return health.Unknown, "server time unavailable", err
			}
			snaps, err := b.Agents(ctx)
			if err != nil {
				return health.Unknown, "agent snapshots unavailable", err
			}
			usageRows, err := b.Usage(ctx)
			if err != nil {
				return health.Unknown, "agent usage unavailable", err
			}
			observed, err := b.Observed(ctx)
			if err != nil {
				return health.Unknown, "pane observations unavailable", err
			}
			events, _, err := b.Recent(ctx, []string{"status", "report", "cmd"}, 1000)
			if err != nil {
				return health.Unknown, "recent activity unavailable", err
			}
			latest := make(map[string]int64)
			for _, event := range events {
				id := event.Agent
				if !bus.ValidName(id) {
					continue
				}
				ms := streamIDMillis(event.ID)
				if ms > latest[id] {
					latest[id] = ms
				}
			}
			var warned, unknown, working int
			usageMu.Lock()
			defer usageMu.Unlock()
			for agent, snap := range snaps {
				if snap.State != "working" {
					continue
				}
				working++
				last := snap.TS
				if latest[agent] > last {
					last = latest[agent]
				}
				row := usageRows[agent]
				tokens, tokensKnown := contextTokenCount(row.Ctx)
				known := runawayThresholdsOK && last > 0 && row.TS > 0 && tokensKnown
				age := time.Duration(0)
				if last > 0 {
					age = now.Sub(time.UnixMilli(last))
				}
				base := baselines[agent]
				growth, span := 0, time.Duration(0)
				if base.at.IsZero() || now.Sub(base.at) > runawayThresholds.GrowthSpan {
					baselines[agent] = usageBaseline{at: now, tokens: tokens}
				} else {
					growth, span = tokens-base.tokens, now.Sub(base.at)
					if growth < 0 {
						growth = 0
					}
					baselines[agent] = usageBaseline{at: base.at, tokens: base.tokens}
				}
				repeats := 0
				if o, ok := observed[agent]; ok && o.PID == snap.PID && o.Fresh(now.UnixMilli()) {
					repeats = o.ScreenRepeats
				} else {
					known = false
				}
				out := health.EvaluateRunawayWithThresholds(age, growth, span, repeats, known, runawayThresholds)
				if out.Status == health.Warn {
					warned++
				} else if out.Status == health.Unknown {
					unknown++
				}
			}
			if warned > 0 {
				return health.Warn, fmt.Sprintf("%d working agent(s) show little visible progress", warned), nil
			}
			if unknown > 0 {
				return health.Unknown, "working agent progress evidence is incomplete", errors.New("incomplete progress evidence")
			}
			if working == 0 {
				return health.OK, "no declared working agents", nil
			}
			return health.OK, "working agents show recent progress", nil
		}),
		healthTask("cmd-delivery", func(ctx context.Context) (health.Status, string, error) {
			now, err := b.ServerTime(ctx)
			if err != nil {
				return health.Unknown, "server time unavailable", err
			}
			groups, err := b.CmdDeliveryGroups(ctx)
			if err != nil {
				return health.Unknown, "consumer-group state unavailable", err
			}
			snaps, err := b.Agents(ctx)
			if err != nil {
				return health.Unknown, "agent registry unavailable", err
			}
			ev, ignored := deliveryEvidence(groups, snaps, now)
			out := health.EvaluateDelivery(ev)
			if ignored > 0 {
				out.Detail += fmt.Sprintf(" (%d group(s) of unregistered agents ignored: agentbus agents forget <name>)", ignored)
			}
			return out.Status, out.Detail, nil
		}),
		healthTask("cmd-targets", func(ctx context.Context) (health.Status, string, error) {
			targets, err := b.CmdTargets(ctx)
			if err != nil {
				return health.Unknown, "cmd targets unavailable", err
			}
			snaps, err := b.Agents(ctx)
			if err != nil {
				return health.Unknown, "agent registry unavailable", err
			}
			reserved := map[string]bool{bus.PeopleTarget: true, "dispatch": true, "conductor": true,
				"missions": true, "master": true, "sentinel": true}
			unknown := 0
			for _, target := range targets {
				if !reserved[target] {
					if _, ok := snaps[target]; !ok {
						unknown++
					}
				}
			}
			if unknown > 0 {
				return health.Warn, fmt.Sprintf("%d retained cmd target(s) are not registered", unknown), nil
			}
			return health.OK, "all retained cmd targets are registered or reserved", nil
		}),
		healthTask("quota-freshness", func(ctx context.Context) (health.Status, string, error) {
			snaps, err := b.Agents(ctx)
			if err != nil {
				return health.Unknown, "agent registry unavailable", err
			}
			providers := map[string]bool{}
			unsupported := false
			for _, snap := range snaps {
				if provider := bus.ProviderFor(snap.Client, snap.Provider); provider != "" {
					providers[provider] = true
				} else if healthActive(snap.State) && snap.Client != "" {
					unsupported = true
				}
			}
			if len(providers) == 0 {
				if unsupported {
					return health.Unknown, "an active agent uses an unsupported quota provider", errors.New("unsupported quota provider")
				}
				out := health.EvaluateQuotaFreshness(false, true, 0, time.Now())
				return out.Status, out.Detail, nil
			}
			budgets, err := b.Budgets(ctx)
			if err != nil {
				return health.Unknown, "quota snapshots unavailable", err
			}
			var stale, unknown int
			now, err := b.ServerTime(ctx)
			if err != nil {
				return health.Unknown, "server time unavailable", err
			}
			for provider := range providers {
				row, ok := budgets[provider]
				at := row.ObservedAt
				if at == 0 {
					at = row.CollectedAt
				}
				if at == 0 {
					at = row.TS
				}
				if !ok || row.Unknown || at == 0 {
					unknown++
				} else if now.Sub(time.UnixMilli(at)) > health.QuotaStaleAfter {
					stale++
				}
			}
			if stale > 0 {
				return health.Warn, fmt.Sprintf("%d configured quota reading(s) are stale", stale), nil
			}
			if unknown > 0 {
				return health.Unknown, "one or more configured quota readings are unavailable", errors.New("quota source incomplete")
			}
			if unsupported {
				return health.Unknown, "an active agent uses an unsupported quota provider", errors.New("unsupported quota provider")
			}
			return health.OK, "configured quota readings are fresh", nil
		}),
		healthTask("tracked-secrets", func(ctx context.Context) (health.Status, string, error) {
			root, err := os.Getwd()
			if err != nil {
				return health.Unknown, "repository path unavailable", err
			}
			paths, err := trackedPaths(ctx, root)
			if err != nil {
				return health.Unknown, "tracked file paths unavailable", err
			}
			out := health.EvaluateSecretPaths(paths, true)
			return out.Status, out.Detail, nil
		}),
		healthTask("redis-password", func(ctx context.Context) (health.Status, string, error) {
			host := os.Getenv("REDIS_HOST")
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			host = strings.Trim(host, "[]")
			if host != "" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
				return health.Unknown, "Redis host is not locally verifiable", errors.New("external Redis host")
			}
			if os.Getenv("REDIS_PASSWORD_FILE") != "" {
				return health.Unknown, "Redis password file cannot be classified without reading its credential", errors.New("file credential source")
			}
			cmd := exec.CommandContext(ctx, "docker", "ps", "--filter", "publish=6380", "--format", "{{.Ports}}")
			cmd.WaitDelay = time.Second
			out, err := cmd.Output()
			if err != nil {
				return health.Unknown, "Docker port mapping unavailable", err
			}
			published := strings.TrimSpace(string(out))
			local := published == ""
			for _, line := range strings.Split(published, "\n") {
				for _, port := range strings.Split(line, ",") {
					port = strings.TrimSpace(port)
					if !strings.Contains(port, "->") {
						continue
					}
					if strings.HasPrefix(port, "127.0.0.1:") || strings.HasPrefix(port, "[::1]:") {
						local = true
					} else {
						return health.Fail, "Redis port is published beyond loopback", nil
					}
				}
			}
			outcome := health.EvaluateRedis(local, bus.LegacyPasswordInUse(os.Getenv))
			return outcome.Status, outcome.Detail, nil
		}),
		healthTask("redis-version", func(ctx context.Context) (health.Status, string, error) {
			info, runErr := b.RedisInfo(ctx)
			path, err := redisLatestPath()
			cache, cacheErr := redisLatest{}, err
			if err == nil {
				cache, cacheErr = readRedisLatest(path)
			}
			out := redisVersionOutcome(info.Version, runErr, cache, cacheErr, time.Now())
			return out.Status, out.Detail, nil
		}),
		healthTask("service-env", func(ctx context.Context) (health.Status, string, error) {
			installed, err := b.ServicesInstalled(ctx)
			if err != nil {
				return health.Unknown, "service installation state unavailable", err
			}
			if !installed {
				return health.Unknown, "services are not installed for this project", errors.New("services not installed")
			}
			info, err := os.Stat(paths.EnvFile(b.Project()))
			out := health.EvaluateFileMode(paths.EnvFile(b.Project()), err, infoMode(info))
			return out.Status, out.Detail, nil
		}),
		healthTask("installed-versions", func(ctx context.Context) (health.Status, string, error) {
			problems, known := 0, true
			cache, err := latestReleaseCache()
			if err != nil {
				known = false
			} else if time.Since(cache.CheckedAt) > updateCacheMaxAge || cache.CheckedAt.After(time.Now().Add(5*time.Minute)) {
				known = false
			} else if cache.Tag != "" && !versionAtLeast(buildinfo.Version, cache.Tag) {
				problems++
			}
			manifestPath, err := manifestPath(os.Environ())
			if err != nil {
				known = false
			} else if m, err := rolecfg.Load(manifestPath); err != nil {
				known = false
			} else if !skillsMatch(m, os.Environ()) {
				problems++
			}
			version, err := readVersion(ctx, "herdr")
			if err != nil {
				known = false
			} else if !versionAtLeast(version, herdrMinimumVersion) {
				problems++
			}
			plugin, err := exec.CommandContext(ctx, herdrBin(os.Getenv), "plugin", "list", "--json").Output()
			if err != nil {
				known = false
			} else {
				root, rootErr := repoDir(os.Getenv)
				problem, readable := pluginProblem(plugin, buildinfo.Version, root)
				clear(plugin)
				if rootErr != nil || !readable {
					known = false
				} else if problem {
					problems++
				}
			}
			out := health.EvaluateVersions(known, problems)
			return out.Status, out.Detail, nil
		}),
		healthTask("herdr-probe", func(ctx context.Context) (health.Status, string, error) {
			pinger, ok := rt.(interface{ Ping(context.Context) error })
			if !ok {
				out := health.EvaluateHerdrProbe(false, false, 0)
				return out.Status, out.Detail, errors.New("runtime has no health probe")
			}
			if err := pinger.Ping(ctx); err != nil {
				if ctx.Err() != nil {
					return health.Unknown, "herdr probe cancelled", ctx.Err()
				}
				probeMu.Lock()
				probeFailures++
				count := probeFailures
				probeMu.Unlock()
				out := health.EvaluateHerdrProbe(true, true, count)
				return out.Status, out.Detail, nil
			}
			probeMu.Lock()
			probeFailures = 0
			probeMu.Unlock()
			out := health.EvaluateHerdrProbe(true, true, 0)
			return out.Status, out.Detail, nil
		}),
	}
}

func healthActive(state string) bool {
	return state == "working" || state == "idle" || state == "blocked" || state == "waiting"
}

func procHasSettingsArg(pid int) (bool, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return false, err
	}
	found := false
	for _, arg := range bytes.Split(raw, []byte{0}) {
		if bytes.Equal(arg, []byte("--settings")) || bytes.HasPrefix(arg, []byte("--settings=")) {
			found = true
			break
		}
	}
	clear(raw)
	return found, nil
}

func procEnvIdentity(pid int, agent, role string) (bool, bool, bool, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return false, false, false, err
	}
	defer clear(raw)
	rolePresent, roleOK, agentOK := false, false, false
	remaining := raw
	for len(remaining) > 0 {
		end := bytes.IndexByte(remaining, 0)
		item := remaining
		if end >= 0 {
			item, remaining = remaining[:end], remaining[end+1:]
		} else {
			remaining = nil
		}
		sep := bytes.IndexByte(item, '=')
		if sep < 0 {
			continue
		}
		key, value := item[:sep], item[sep+1:]
		if bytes.Equal(key, []byte("AGENT_BUS_ROLE")) {
			rolePresent = true
			roleOK = bytes.Equal(value, []byte(role))
		}
		if bytes.Equal(key, []byte("AGENT_BUS_AGENT")) {
			agentOK = bytes.Equal(value, []byte(agent))
		}
	}
	return rolePresent, roleOK, agentOK, nil
}

func streamIDMillis(id string) int64 {
	ms, _, _ := strings.Cut(id, "-")
	n, _ := strconv.ParseInt(ms, 10, 64)
	return n
}

func parseContextTokens(value string) int {
	n, ok := contextTokenCount(value)
	if !ok {
		return 0
	}
	return n
}

func contextTokenCount(value string) (int, bool) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return 0, false
	}
	value = fields[0]
	multiplier := 1
	if strings.HasSuffix(strings.ToLower(value), "k") {
		multiplier, value = 1000, value[:len(value)-1]
	} else if strings.HasSuffix(strings.ToLower(value), "m") {
		multiplier, value = 1_000_000, value[:len(value)-1]
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > float64(int(^uint(0)>>1))/float64(multiplier) {
		return 0, false
	}
	return int(n * float64(multiplier)), true
}

func runawayThresholdsFromEnv(getenv func(string) string) (health.RunawayThresholds, bool) {
	// The env first (an invalid value makes the check unknown, as always), then
	// config.toml: up never copies these into the services' env file.
	settings := config.Machine(getenv)
	t, valid := health.DefaultRunawayThresholds(), true
	for _, x := range []struct {
		key string
		set func(any)
	}{
		{"health.runaway.silent_after", func(v any) { t.SilentAfter = v.(time.Duration) }},
		{"health.runaway.context_growth", func(v any) { t.FastGrowth = v.(int) }},
		{"health.runaway.growth_span", func(v any) { t.GrowthSpan = v.(time.Duration) }},
		{"health.runaway.screen_repeats", func(v any) { t.RepeatedScreens = v.(int) }},
	} {
		if r := settings.Get(x.key); r.Invalid {
			valid = false
		} else {
			x.set(r.Value)
		}
	}
	return t, valid
}

func trackedPaths(ctx context.Context, root string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z")
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		clear(output)
		return nil, err
	}
	paths := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	clear(output)
	if len(paths) == 1 && paths[0] == "" {
		return nil, nil
	}
	return paths, nil
}

type releaseCache struct {
	CheckedAt time.Time `json:"checked_at"`
	Tag       string    `json:"tag"`
}

func latestReleaseCache() (releaseCache, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return releaseCache{}, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "agentbus", "latest.json"))
	if err != nil {
		return releaseCache{}, err
	}
	var cache releaseCache
	err = json.Unmarshal(b, &cache)
	return cache, err
}

var versionPattern = regexp.MustCompile(`v?([0-9]+)\.([0-9]+)\.([0-9]+)`)

func versionAtLeast(got, minimum string) bool {
	a, b := versionPattern.FindStringSubmatch(got), versionPattern.FindStringSubmatch(minimum)
	if len(a) != 4 || len(b) != 4 {
		return false
	}
	for i := 1; i <= 3; i++ {
		av, _ := strconv.Atoi(a[i])
		bv, _ := strconv.Atoi(b[i])
		if av != bv {
			return av > bv
		}
	}
	return true
}

// pluginState reads `herdr plugin list --json` and reports whether the plugin
// with id want is registered and at which version. ok is false when the JSON
// cannot be read.
func pluginState(listJSON []byte, want string) (registered bool, version, root string, ok bool) {
	var list struct {
		Result struct {
			Plugins []struct {
				PluginID   string `json:"plugin_id"`
				Version    string `json:"version"`
				PluginRoot string `json:"plugin_root"`
			} `json:"plugins"`
		} `json:"result"`
	}
	if err := json.Unmarshal(listJSON, &list); err != nil {
		return false, "", "", false
	}
	for _, p := range list.Result.Plugins {
		if p.PluginID == want {
			return true, p.Version, p.PluginRoot, true
		}
	}
	return false, "", "", true
}

var releaseVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

// pluginProblem judges the registered plugin against the binary's version and
// the expected checkout. A dev build or an untagged describe does not compare versions; the root is
// always compared.
func pluginProblem(listJSON []byte, binaryVersion, wantRoot string) (problem, readable bool) {
	registered, version, root, ok := pluginState(listJSON, pluginID)
	if !ok {
		return false, false
	}
	if !registered || !sameDir(root, wantRoot) {
		return true, true
	}
	// setup stamps the full `git describe`: only an exact vX.Y.Z is comparable;
	// v0.20.0-3-gabc or v0.20.0-dirty (an untagged commit) is treated like dev.
	if binaryVersion == "dev" || !releaseVersion.MatchString(binaryVersion) {
		return false, true
	}
	return version == "" || strings.TrimPrefix(version, "v") != strings.TrimPrefix(binaryVersion, "v"), true
}

// sameDir compares two directories after Clean, and after EvalSymlinks when
// both resolve.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func readVersion(ctx context.Context, command string) (string, error) {
	cmd := exec.CommandContext(ctx, command, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	m := versionPattern.Find(out)
	clear(out)
	if len(m) == 0 {
		return "", fmt.Errorf("version unavailable")
	}
	return string(m), nil
}

func skillsMatch(m *rolecfg.Manifest, env []string) bool {
	if m == nil {
		return false
	}
	root := os.Getenv("AGENTBUS_REPO")
	if root == "" {
		manifest, err := manifestPath(env)
		if err != nil {
			return false
		}
		root = filepath.Dir(manifest)
	}
	roles := make([]string, 0, len(m.Roles))
	for role := range m.Roles {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		profile, err := m.Resolve(role)
		if err != nil {
			return false
		}
		dest := skillsDestination(profile.Client, env)
		for _, name := range profile.Skills {
			target := filepath.Join(dest, name)
			resolved, err := filepath.EvalSymlinks(target)
			if err != nil || filepath.Clean(resolved) != filepath.Clean(filepath.Join(root, "skills", name)) {
				return false
			}
		}
	}
	return true
}

func skillsDestination(client string, env []string) string {
	for _, item := range env {
		if key, value, ok := strings.Cut(item, "="); ok && key == "SKILLS_DEST" && value != "" {
			return value
		}
	}
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	dir := ".agents/skills"
	if client == "claude-code" {
		dir = ".claude/skills"
	}
	return filepath.Join(home, dir)
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}

// clientEvidence is every verified agent's client as last observed (client
// reload spec): a reading for another process than the agent's is not fresh.
func clientEvidence(snaps map[string]bus.AgentSnapshot, obs map[string]bus.Observation, nowMs int64) []health.ClientEvidence {
	var ev []health.ClientEvidence
	for a, s := range snaps {
		if s.Pane == "" || s.PID == 0 {
			continue
		}
		o, ok := obs[a]
		e := health.ClientEvidence{Agent: a, Fresh: ok && o.Fresh(nowMs) && o.PID == s.PID, Judged: o.ClientJudged}
		if o.Update != nil {
			e.Outdated, e.Client, e.Running, e.Installed = true, o.Update.Client, o.Update.Running, o.Update.Installed
		}
		ev = append(ev, e)
	}
	return ev
}

// contextEvidence is the context check's evidence: one entry per published
// fill, known only when it is not aged.
func contextEvidence(fills map[string]bus.ContextFill) []health.ContextEvidence {
	ev := make([]health.ContextEvidence, 0, len(fills))
	for a, f := range fills {
		ev = append(ev, health.ContextEvidence{Agent: a, Pct: f.Pct, Known: f.Known()})
	}
	return ev
}

// deliveryEvidence is cmd-delivery's evidence from the consumer groups of the
// agents registered in {p}:agents only (A9): a dead group (a name never
// forgotten) keeps a lag no one will read, and once tripped "fail" for nobody.
// It returns how many groups it ignored.
func deliveryEvidence(groups []bus.CmdDeliveryGroup, registered map[string]bus.AgentSnapshot, now time.Time) (health.DeliveryEvidence, int) {
	ev, ignored := health.DeliveryEvidence{Known: true}, 0
	for _, group := range groups {
		if _, ok := registered[group.Group]; !ok {
			ignored++
			continue
		}
		if group.Pending > 0 && group.OldestAt > 0 {
			if age := now.Sub(time.UnixMilli(group.OldestAt)); age > ev.Pending {
				ev.Pending, ev.HasPending = age, true
			}
		}
		ev.Lag = max(ev.Lag, group.Lag)
	}
	return ev, ignored
}
