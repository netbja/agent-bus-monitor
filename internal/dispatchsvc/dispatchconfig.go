package dispatchsvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/mission"
	"github.com/netbja/agent-bus-monitor/notify"
	"github.com/netbja/agent-bus-monitor/quota"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/tokens"
)

// The dispatch service's configuration (dispatchsvc spec §2.1, P8): the
// eleven functions that build it, gathered from servicecmd.go and quotacmd.go
// so the move to internal/dispatchsvc moves whole files only.

// Config is the dispatch service's dispatcher: every pass the
// service runs, and the relay (herdr exit §4.2). The relay was missing here
// until host-relay-timeout: only the foreground agentbus dispatch set it, so
// a host command from an agent's terminal was never read by the service.
//
// It fills cfg, which the caller owns and keeps (dispatchsvc spec §3): cfg
// starts as d.Base, whose EscalateTo the caller has set (service run
// --escalate-to, else AGENT_BUS_ESCALATE_TO; master otherwise). The stall
// checks, the self-repair watcher and the reboot judge read &cfg.EscalateTo,
// a pointer into the caller's config, never into a copy.
func Config(d Deps, cfg *dispatch.Config) (*dispatch.HostRuntime, error) {
	b, project, paths, log := d.Bus, d.Bus.Project(), d.Paths, d.Log
	*cfg = d.Base
	target := &cfg.EscalateTo
	cfg.Log = log
	cfg.HostOps = d.HostOps // host commands from inside an agent's terminal
	cfg.Quota = serviceQuota(os.Getenv, log)
	if cfg.Quota.Read != nil { // the thresholds re-read from the files each minute (quota settings §2.2)
		cfg.Quota.Thresholds = quotaThresholds(os.Getenv, project, paths.EnvFile(project))
	}
	cfg.Login = serviceLoginExpiry(os.Getenv)                               // an expired login told to the person (login-updates)
	cfg.Context = serviceContext(os.Getenv, exec.LookPath, notify.RunQuiet) // context fill and alert (context-alert)
	cfg.Tokens = serviceTokens(os.Getenv, os.UserHomeDir)                   // proved sessions and their tokens (tokens)
	cfg.History = serviceHistory(os.Getenv, paths.StateDir(project))        // the streams archived to day files (history)
	cfg.Backup = serviceBackup(log)                                         // the daily backup when backup.schedule = daily
	cfg.Observe = dispatch.ObserveEvery
	cfg.Prompt = servicePrompt(os.Getenv, exec.LookPath, notify.RunQuiet) // a pane blocked on its client's prompt (prompt-notify)
	cfg.Prompt.Skip = promptSkip(b, os.Getenv)                            // never rung twice with a desktop target (notifications)
	cfg.Notify = serviceNotify(b, os.Getenv)                              // the person's phone and desktop (notifications)
	cfg.MergeQueue = serviceMergeQueue(b, os.Getenv, target)              // what waits for the person's merge (merge-queue)
	cfg.Clear = serviceClear(os.Getenv, exec.LookPath, notify.RunQuiet)   // /clear after a declared hand-off (lifecycle part B)
	cfg.Clear.Decisions = dispatch.NewClearDecisions()                    // why a clear waits: logged, and read by clear-stall
	h, err := Runtime(project, os.Getenv)                                 // the dispatcher on the project's holder (herdr exit 10b)
	if err != nil {
		return nil, err
	}
	h.Hooks = bgHooks(b) // the agents' hook records: their background tasks (background-work-visible step 1)
	cfg.Health = realHealthConfig(b, h, d.Manager, paths, cfg.Clear, target)
	// The self-repair watcher: shadow passes only (step 6); its check in every mode.
	repair, repairCfg := serviceSelfRepair(b, os.Getenv, target, d.Boot)
	if skew := d.SelfRepairSkew; skew != nil { // tests only
		read := repair.read
		repair.read = func(ctx context.Context) (selfRepairReads, error) {
			r, err := read(ctx)
			if err == nil {
				r.Now = skew(r.Now)
			}
			return r, err
		}
	}
	cfg.SelfRepair, cfg.Health.Checks = repairCfg, append(cfg.Health.Checks, selfRepairCheck(repair))
	cfg.PopRetire = d.PopEnd(servicePopRetire(os.Getenv, func(ctx context.Context, a string) ([]string, error) { return mission.HeldBy(ctx, b, a) }), h)
	cfg.ReloadPolicy = serviceReloadPolicy(os.Getenv, paths.EnvFile(project))                         // read at each client-update send (auto-reload)
	cfg.Reboot = serviceRebootJudge(b, project, paths, os.Getenv, d.Workdir, d.Boot, target, log).run // judged once, before any loop (reboot-resume §3)
	return h, nil
}

// Deps is what the dispatch service's configuration takes from its
// caller (dispatchsvc spec §3).
type Deps struct {
	Bus     *bus.Bus
	Getenv  func(string) string
	Paths   service.Paths
	Workdir string // the unit's working directory: the project's repository
	Boot    string // this boot's id (lifecycle)
	Log     io.Writer

	Base    dispatch.Config        // dispatch.DefaultConfig with the person's name set
	HostOps dispatch.HostOpsConfig // the relay's runner: CLI commands
	PopEnd  func(dispatch.PopRetireConfig, dispatch.Runtime) dispatch.PopRetireConfig
	Manager service.Manager // the service manager the health checks read (the caller's detection)

	// SelfRepairSkew moves the time each self-repair read got from Redis
	// TIME. Tests only: nil in production (TestNoProductionSelfRepairSkew).
	SelfRepairSkew func(time.Time) time.Time
}

// ForegroundConfig adds to cfg, in place, what the foreground agentbus
// dispatch runs: the pop's end, the health checks, the reload policy
// (dispatchsvc spec §3). d.Bus may be nil: a bad flag exits
// before any bus is used.
func ForegroundConfig(d Deps, cfg *dispatch.Config, rt dispatch.Runtime) {
	b := d.Bus
	cfg.PopRetire = d.PopEnd(servicePopRetire(os.Getenv, func(ctx context.Context, a string) ([]string, error) { return mission.HeldBy(ctx, b, a) }), rt)
	if paths, err := service.PathsFrom(os.Getenv); err == nil {
		cfg.Health = realHealthConfig(b, rt, d.Manager, paths, cfg.Clear, &cfg.EscalateTo)
		if b != nil { // a bad flag exits before any bus is used
			cfg.ReloadPolicy = serviceReloadPolicy(os.Getenv, paths.EnvFile(b.Project()))
		}
	}
}

// serviceReloadPolicy reads the project's reload policy as agentbus reload
// does (service.ReloadPolicy: the environment, the project's env file, then
// config.toml), at each call, so a change on busmon's C page applies to the
// next client update without a restart (auto-reload).
func serviceReloadPolicy(getenv func(string) string, envFile string) func() string {
	return func() string {
		ef, _ := service.LoadEnvFile(envFile)
		mode, _ := service.ReloadPolicy(getenv, ef)
		return mode
	}
}

// serviceQuota is the dispatch service's quota policy: the configured readers
// and the thresholds from the environment (AGENT_BUS_QUOTA_WARN, _HOLD,
// _MAX_AGE; AGENT_BUS_QUOTA=off disables it).
func serviceQuota(getenv func(string) string, log io.Writer) dispatch.QuotaConfig {
	settings := config.Machine(getenv)
	if !settings.Bool("dispatch.quota") {
		return dispatch.QuotaConfig{}
	}
	cfg, err := quota.LoadConfig(quota.ConfigPath(), exec.LookPath)
	if err != nil {
		fmt.Fprintf(log, "%s quota config: %v (built-in readers only)\n", time.Now().Format("15:04:05"), err)
	}
	readers := quota.Readers(cfg, time.Now)
	q := dispatch.QuotaConfig{Read: func(ctx context.Context) []quota.Reading { return quota.ReadAll(ctx, readers) }}
	q.WarnPct, q.HoldPct = settings.Float("dispatch.quota_warn"), settings.Float("dispatch.quota_hold")
	q.MaxAge = settings.Duration("dispatch.quota_max_age")
	if q.LongHold = settings.Duration("dispatch.quota_long_hold"); q.LongHold == 0 {
		q.LongHold = -1 // 0 = off
	}
	// display.date_format, from config.toml at each message: a change in
	// busmon's Settings applies with no restart (quota settings §2.3).
	q.DateFormat = func() string { return config.Machine(getenv).String("display.date_format") }
	return q
}

// quotaThresholds reads project's quota thresholds from the files, for the
// dispatcher's refresh (quota settings §2.2): config.toml's [project.<p>]
// table, then the project's service env file (the shell at up), then the
// machine keys, then the defaults (config.ProjectValue). The process
// environment is never read: it is the service's start-time copy. A drain no
// file sets follows the warning (today's behaviour). A config.toml or env
// file that cannot be read is an error, and the dispatcher keeps the last
// values; a missing env file is none.
func quotaThresholds(getenv func(string) string, project, envFile string) func() (dispatch.QuotaThresholds, error) {
	seen := false // config.toml existed at a previous read
	return func() (dispatch.QuotaThresholds, error) {
		f := config.Load(config.Path(getenv), "config.toml")
		if !f.Exists && seen { // removed since: keep the last (spec §2.2)
			return dispatch.QuotaThresholds{}, errors.New("config.toml: missing since the last read")
		}
		seen = seen || f.Exists
		for _, w := range f.Warnings {
			if w.Key == "" { // the whole file: it cannot be read or does not parse
				return dispatch.QuotaThresholds{}, fmt.Errorf("config.toml: %s", w.Msg)
			}
		}
		ef, err := service.LoadEnvFile(envFile)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return dispatch.QuotaThresholds{}, err
		}
		get := func(name string) config.Resolved { return config.ProjectValue(f, ef, project, name) }
		th := dispatch.QuotaThresholds{Warn: get("dispatch.quota_warn").Value.(float64), Hold: get("dispatch.quota_hold").Value.(float64),
			MaxAge: get("dispatch.quota_max_age").Value.(time.Duration)}
		if d := get("dispatch.quota_drain"); d.Source == "default" {
			th.Drain = th.Warn
		} else {
			th.Drain = d.Value.(float64)
		}
		return th, nil
	}
}

// serviceContext is the dispatch service's context pass (context-alert): each
// verified agent's fill published every minute, and the alert threshold from
// AGENT_BUS_CONTEXT_ALERT (percent, default 80, 0 = off). The desktop
// notification goes through notify-send when it is on PATH, else nowhere.
func serviceContext(getenv func(string) string, lookPath func(string) (string, error),
	run func(ctx context.Context, bin string, args ...string) error) dispatch.ContextConfig {
	settings := config.Machine(getenv)
	return dispatch.ContextConfig{Every: time.Minute, Alert: bus.ContextAlert(getenv), MaxAge: bus.ContextMaxAge,
		Urgent: settings.Float("dispatch.context_urgent"), Deadline: settings.Duration("dispatch.context_handoff_deadline"),
		Read:      func(s string) (quota.ContextReading, bool) { return quota.ReadContext(quota.ContextDir(), s) },
		Compacted: serviceCompacted(getenv, os.UserHomeDir),
		Notify:    desktopNotify(lookPath, run)}
}

// serviceCompacted reads a session's last automatic compaction (§3.5) from
// its transcript under the agent's CLAUDE_CONFIG_DIR, else the service's,
// else ~/.claude; an unreadable transcript is none (the episode ends
// silently, as it would without the notice).
func serviceCompacted(getenv func(string) string, home func() (string, error)) func(string, string, int64) (int64, bool) {
	return func(configDir, session string, afterMs int64) (int64, bool) {
		base := configDir
		if base == "" {
			base = getenv("CLAUDE_CONFIG_DIR")
		}
		if base == "" {
			h, err := home()
			if err != nil {
				return 0, false
			}
			base = filepath.Join(h, ".claude")
		}
		pre, _, found, err := tokens.AutoCompactAfter(filepath.Join(base, "projects"), session, afterMs)
		return pre, err == nil && found
	}
}

// serviceTokens is the dispatch service's tokens pass: the transcripts of
// Claude Code's projects directory ($CLAUDE_CONFIG_DIR/projects, else
// ~/.claude/projects) and the rollouts of Codex's sessions directory
// ($CODEX_HOME/sessions, else ~/.codex/sessions), {p}:tokens every 5 minutes,
// 64 files and 128 MiB per pass for both. AGENT_BUS_TOKENS=0 turns it off.
func serviceTokens(getenv func(string) string, home func() (string, error)) dispatch.TokensConfig {
	if !config.Machine(getenv).Bool("dispatch.tokens") {
		return dispatch.TokensConfig{}
	}
	h, err := home()
	if err != nil {
		h = ""
	}
	dir := func(env, under string) string { // "" when neither the env nor a home names it
		if v := getenv(env); v != "" {
			return v
		}
		if h == "" {
			return ""
		}
		return filepath.Join(h, under)
	}
	base, codex := dir("CLAUDE_CONFIG_DIR", ".claude"), dir("CODEX_HOME", ".codex")
	if base == "" && codex == "" {
		return dispatch.TokensConfig{}
	}
	c := dispatch.TokensConfig{Every: 5 * time.Minute, Budget: tokens.Budget{Files: 64, Bytes: 128 << 20}}
	if base != "" {
		c.Root = filepath.Join(base, "projects")
	}
	if codex != "" { // either one counts alone (reviewer-codex)
		c.CodexRoot = filepath.Join(codex, "sessions")
	}
	return c
}

// servicePrompt is the dispatch service's prompt notification (prompt-notify):
// AGENT_BUS_PROMPT_GRACE seconds (default 30, 0 = off), through the same
// notify-send runner as the context alert.
func servicePrompt(getenv func(string) string, lookPath func(string) (string, error),
	run func(ctx context.Context, bin string, args ...string) error) dispatch.PromptConfig {
	return dispatch.PromptConfig{Grace: dispatch.PromptGrace(getenv), Notify: desktopNotify(lookPath, run),
		FocusGrace: dispatch.FocusNotice(getenv), StuckAfter: dispatch.StuckAfter(getenv)}
}

// serviceClear is the dispatch service's auto-clear (lifecycle part B): on by
// default; AGENT_BUS_AUTO_CLEAR=0 or auto_clear = false in config.toml turns
// it off (another env value is skipped, with a warning naming the variable). An escalated clear rings the desktop through
// the context alert's runner.
func serviceClear(getenv func(string) string, lookPath func(string) (string, error),
	run func(ctx context.Context, bin string, args ...string) error) dispatch.ClearConfig {
	return dispatch.ClearConfig{On: config.Machine(getenv).Bool("dispatch.auto_clear"), Proof: 30 * time.Second, Notify: desktopNotify(lookPath, run)}
}

// servicePopRetire is the pop's end (pop-auto-retire §8): on unless
// dispatch.pop_retire is off or its delay is 0; missions lists the mission
// attempts in progress on an agent.
func servicePopRetire(getenv func(string) string, missions func(context.Context, string) ([]string, error)) dispatch.PopRetireConfig {
	r := config.Machine(getenv)
	after := r.Duration("dispatch.pop_retire_after")
	return dispatch.PopRetireConfig{On: r.Bool("dispatch.pop_retire") && after > 0, After: after, Missions: missions,
		ProcStart: dispatch.ProcStart}
}

// desktopNotify runs notify-send, bounded to 5 s; nil when it is not on PATH.
func desktopNotify(lookPath func(string) (string, error),
	run func(ctx context.Context, bin string, args ...string) error) func(ctx context.Context, title, body string) error {
	bin, err := lookPath("notify-send")
	if err != nil {
		return nil
	}
	return func(ctx context.Context, title, body string) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return run(ctx, bin, "-a", "agentbus", title, body)
	}
}
