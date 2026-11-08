// Package config is the person's settings (spec
// docs/superpowers/specs/2026-10-29-config-file-design.md): one registry of
// every tunable, the machine's config.toml, the legacy busmon.toml, and the
// resolution flag > environment > config.toml > busmon.toml > default, per key.
// Flags are the caller's: Resolver starts at the environment.
//
// keys.go is the only place a tunable's env name, file key, default and env
// rule are written. Each env rule is today's, byte for byte; only the file
// value is typed.
package config

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Kind is a setting's Go type in the file and in Resolved.Value.
type Kind int

const (
	Bool     Kind = iota // bool
	Int                  // int
	Float                // float64
	Duration             // time.Duration
	String               // string
)

func (k Kind) String() string {
	return [...]string{"bool", "int", "float", "duration", "string"}[k]
}

func (k Kind) holds(v any) bool {
	switch v.(type) {
	case bool:
		return k == Bool
	case int:
		return k == Int
	case float64:
		return k == Float
	case time.Duration:
		return k == Duration
	case string:
		return k == String
	}
	return false
}

// envResult is what an env rule makes of a set (non-empty) value.
type envResult int

const (
	envUse     envResult = iota // the value
	envSkip                     // ignored: the next source down applies
	envInvalid                  // refused: resolution stops (busmon exits, the runaway check reads unknown)
)

// Key is one row of the registry.
type Key struct {
	Name    string // file key, "<table>.<key>"
	Env     string // its environment variable; "" = file only
	Kind    Kind
	Default any // of Kind
	ReadBy  string
	Legacy  bool   // also read from busmon.toml
	Flag    string // the flag above the env, for the command that has it
	Invalid string // what an invalid env value does (only for rules that refuse one)
	// MachineOnly keys are the machine's alone: a project's services env file
	// is never read for them (forge-delivery answer 1).
	MachineOnly bool
	// PersonOnly keys are the person's alone (backup.after_*): read from
	// config.toml only, never written by agentbus (Set and Unset refuse
	// them: edit config.toml yourself). They have no env variable and are
	// MachineOnly too.
	PersonOnly bool
	// List keys are a TOML list in the file, kept as one comma-joined string
	// (backup.after_env, notify.targets...): a page edits them as text
	// separated by commas.
	List bool

	env  func(raw string) (any, envResult) // today's env rule, for a non-empty value
	file func(v any) (any, string)         // a decoded file value: the setting, or why not
}

var Keys = []*Key{
	{Name: "waiting.popup_after", Kind: Duration, Default: 5 * time.Minute, ReadBy: "busmon", Legacy: true, file: fileDuration(true)},
	{Name: "waiting.snooze", Kind: Duration, Default: 15 * time.Minute, ReadBy: "busmon", Legacy: true, file: fileDuration(false)},
	{Name: "waiting.bell", Kind: Bool, Default: true, ReadBy: "busmon", Legacy: true, file: fileBool},
	{Name: "waiting.fade", Kind: Bool, Default: true, ReadBy: "busmon", Legacy: true, file: fileBool},
	{Name: "recap.auto_after", Kind: Duration, Default: 8 * time.Hour, ReadBy: "busmon", Legacy: true, file: fileDuration(true)},
	{Name: "display.theme", Env: "AGENT_BUS_BUSMON_THEME", Kind: String, Default: "auto", ReadBy: "busmon", Legacy: true,
		Flag: "busmon --theme", Invalid: "busmon refuses to start",
		env: func(raw string) (any, envResult) {
			if isTheme(raw) {
				return raw, envUse
			}
			return nil, envInvalid
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && isTheme(s) {
				return s, ""
			}
			return nil, "want auto, dark or light"
		}},
	// busmon-icons (spec 2026-11-02-busmon-icons-design.md): an icon per agent
	// and provider, off by default; a wrong value is skipped, never a broken screen.
	{Name: "display.icons", Env: "AGENT_BUS_BUSMON_ICONS", Kind: String, Default: "off", ReadBy: "busmon",
		env: func(raw string) (any, envResult) {
			if isIcons(raw) {
				return raw, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && isIcons(s) {
				return s, ""
			}
			return nil, "want off, nerd or agent-font"
		}},
	{Name: "display.limit", Env: "AGENT_BUS_BUSMON_LIMIT", Kind: Int, Default: 25, ReadBy: "busmon", Legacy: true, Flag: "busmon --limit",
		env: func(raw string) (any, envResult) {
			if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
				return n, envUse
			}
			return nil, envSkip
		},
		file: fileInt(func(int) bool { return true }, "")},
	// The automatic state (spec 2026-11-01-auto-state-design.md): busmon's idle?
	// hint after a working agent's pane is seen ready this long; 0 turns it off.
	{Name: "display.idle_hint_after", Env: "AGENT_BUS_IDLE_HINT_AFTER", Kind: Duration, Default: 10 * time.Minute, ReadBy: "busmon",
		env: envStallDuration, file: fileDuration(true)},
	// busmon's idle view (spec 2026-11-08-busmon-idle-view-design.md §2): a
	// calm screen after this long without input; 0 (the default) is off.
	{Name: "display.idle_view_after", Env: "AGENT_BUS_IDLE_VIEW_AFTER", Kind: Duration, Default: time.Duration(0), ReadBy: "busmon",
		env: envIdleView, file: fileIdleView},
	{Name: "identity.as", Env: "AGENT_BUS_HUMAN", Kind: String, Default: "", ReadBy: "busmon, agentbus identity, ask, plugin", Legacy: true,
		Flag: "busmon --as", Invalid: "busmon refuses to start",
		env: func(raw string) (any, envResult) {
			if validName(raw) {
				return raw, envUse
			}
			return nil, envInvalid
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && (s == "" || validName(s)) {
				return s, ""
			}
			return nil, "want a name: a lower-case letter, then letters, digits, - or _"
		}},
	{Name: "updates.check", Env: "AGENT_BUS_NO_UPDATE_CHECK", Kind: Bool, Default: true, ReadBy: "busmon",
		env:  func(raw string) (any, envResult) { return raw != "1", envUse }, // 1 turns it off
		file: fileBool},
	{Name: "dispatch.context_alert", Env: "AGENT_BUS_CONTEXT_ALERT", Kind: Float, Default: 80.0, ReadBy: "dispatch, busmon, health",
		env: func(raw string) (any, envResult) {
			if n, err := strconv.ParseFloat(raw, 64); err == nil && n >= 0 && n <= 100 {
				return n, envUse
			}
			return nil, envSkip
		},
		file: filePercent},
	{Name: "dispatch.prompt_grace", Env: "AGENT_BUS_PROMPT_GRACE", Kind: Duration, Default: 30 * time.Second, ReadBy: "dispatch",
		env: func(raw string) (any, envResult) {
			if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n <= 3600 {
				return time.Duration(n) * time.Second, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if n, ok := v.(int64); ok && n >= 0 && n <= 3600 {
				return time.Duration(n) * time.Second, ""
			}
			return nil, "want whole seconds from 0 to 3600"
		}},
	// focus-notice: a wake waiting this long on the pane that has the focus
	// earns the person one desktop notice (0 = off).
	{Name: "dispatch.focus_notice", Env: "AGENT_BUS_FOCUS_NOTICE", Kind: Duration, Default: 30 * time.Second, ReadBy: "dispatch",
		env: envStallDuration, file: fileDuration(true)},
	{Name: "dispatch.quota", Env: "AGENT_BUS_QUOTA", Kind: Bool, Default: true, ReadBy: "dispatch",
		env:  func(raw string) (any, envResult) { return raw != "off", envUse }, // off turns it off
		file: fileBool},
	{Name: "dispatch.quota_warn", Env: "AGENT_BUS_QUOTA_WARN", Kind: Float, Default: 85.0, ReadBy: "dispatch", env: envPositiveFloat, file: filePositivePercent},
	{Name: "dispatch.quota_hold", Env: "AGENT_BUS_QUOTA_HOLD", Kind: Float, Default: 95.0, ReadBy: "dispatch", env: envPositiveFloat, file: filePositivePercent},
	{Name: "dispatch.quota_max_age", Env: "AGENT_BUS_QUOTA_MAX_AGE", Kind: Duration, Default: 30 * time.Minute, ReadBy: "dispatch",
		env: func(raw string) (any, envResult) {
			if d, err := time.ParseDuration(raw); err == nil && d > 0 { // <= 0 was always the default
				return d, envUse
			}
			return nil, envSkip
		},
		file: filePositiveDuration},
	{Name: "dispatch.quota_long_hold", Env: "AGENT_BUS_QUOTA_LONG_HOLD", Kind: Duration, Default: 2 * time.Hour, ReadBy: "dispatch",
		env: func(raw string) (any, envResult) { // 0 = off
			if raw == "0" {
				return time.Duration(0), envUse
			}
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				return d, envUse
			}
			return nil, envSkip
		},
		file: fileDuration(true)},
	{Name: "dispatch.tokens", Env: "AGENT_BUS_TOKENS", Kind: Bool, Default: true, ReadBy: "dispatch",
		env:  func(raw string) (any, envResult) { return raw != "0", envUse }, // 0 turns it off
		file: fileBool},
	// On by default (autoclear-default, the person's decision); 0 is the off
	// switch, 1 on, and any other value is skipped (the next source applies),
	// so true or yes never turns it off.
	{Name: "dispatch.auto_clear", Env: "AGENT_BUS_AUTO_CLEAR", Kind: Bool, Default: true, ReadBy: "dispatch",
		env:  envZeroOne,
		file: fileBool},
	// The stall guard (spec 2026-11-01-stall-guard-design.md): how long before
	// each check judges a stall; 0 turns the check off.
	{Name: "dispatch.stall_after", Env: "AGENT_BUS_STALL_AFTER", Kind: Duration, Default: 15 * time.Minute, ReadBy: "dispatch",
		env: envStallDuration, file: fileDuration(true)},
	{Name: "dispatch.clear_stall_after", Env: "AGENT_BUS_CLEAR_STALL_AFTER", Kind: Duration, Default: 15 * time.Minute, ReadBy: "dispatch",
		env: envStallDuration, file: fileDuration(true)},
	{Name: "dispatch.quiet_after", Env: "AGENT_BUS_QUIET_AFTER", Kind: Duration, Default: 15 * time.Minute, ReadBy: "dispatch",
		env: envStallDuration, file: fileDuration(true)},
	// idle-implementer: an implementer idle this long while a request waits for
	// it wakes master (the person's answer, 2026-11-08: 15 minutes); 0 is off.
	{Name: "dispatch.idle_after", Env: "AGENT_BUS_IDLE_AFTER", Kind: Duration, Default: 15 * time.Minute, ReadBy: "dispatch",
		env: envStallDuration, file: fileDuration(true)},
	// The automatic state (spec 2026-11-01-auto-state-design.md): a turn's
	// working seen ready this long reads idle for team-stall; 0 turns it off.
	{Name: "dispatch.turn_stale_after", Env: "AGENT_BUS_TURN_STALE_AFTER", Kind: Duration, Default: 2 * time.Minute, ReadBy: "dispatch",
		env: envStallDuration, file: fileDuration(true)},
	// A pop's end (spec 2026-11-03-pop-auto-retire-design.md): the dispatcher
	// judges idle pops and asks master to end them (on by default, the
	// person's answer; 0 off, 1 on, any other value skipped), once a pop has
	// declared idle this long (0 turns it off); retire --forget re-judges it.
	{Name: "dispatch.pop_retire", Env: "AGENT_BUS_POP_RETIRE", Kind: Bool, Default: true, ReadBy: "dispatch, agentbus retire",
		env:  envZeroOne,
		file: fileBool},
	{Name: "dispatch.pop_retire_after", Env: "AGENT_BUS_POP_RETIRE_AFTER", Kind: Duration, Default: 15 * time.Minute, ReadBy: "dispatch, agentbus retire",
		env: envStallDuration, file: fileDuration(true)},
	// The history archive (spec 2026-11-01-history-design.md): days of day
	// files kept (0 = no archive) and the directory's cap in MiB.
	{Name: "history.keep_days", Env: "AGENT_BUS_HISTORY_DAYS", Kind: Int, Default: 90, ReadBy: "dispatch",
		env: envNonNegativeInt, file: fileInt(func(n int) bool { return n >= 0 }, "want a whole number, 0 for no archive")},
	{Name: "history.max_mb", Env: "AGENT_BUS_HISTORY_MAX_MB", Kind: Int, Default: 256, ReadBy: "dispatch", env: envPositiveInt, file: filePositiveInt},
	// The reload policy (lifecycle part C, auto-reload): on, propose or off.
	// AGENT_BUS_RELOAD's rule is today's: propose and off are themselves, any
	// other set value reads on. agentbus reload puts the project's env file
	// (up --reload) between the env and config.toml.
	// agentbus start's default team for new projects (agentbus-start spec):
	// the --roles syntax, written when the person confirms a reduced team.
	// No env: a person's choice kept in a file. roles.toml is checked by start
	// at use (config imports no rolecfg).
	{Name: "start.team", Kind: String, Default: "", ReadBy: "agentbus start", file: fileTeam},
	{Name: "sentinel.reload", Env: "AGENT_BUS_RELOAD", Kind: String, Default: "on", ReadBy: "agentbus reload, sentinel",
		env: func(raw string) (any, envResult) {
			if raw == "propose" || raw == "off" {
				return raw, envUse
			}
			return "on", envUse
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && (s == "on" || s == "propose" || s == "off") {
				return s, ""
			}
			return nil, "want on, propose or off"
		}},
	// Suspend (spec 2026-11-03-suspend-resume-design.md, §12 Q5): how long
	// agentbus suspend waits for running turns to end before it cancels (or,
	// with --force, goes on); 0 = no wait. --wait on the command line wins.
	{Name: "suspend.wait", Env: "AGENT_BUS_SUSPEND_WAIT", Kind: Duration, Default: 10 * time.Minute, ReadBy: "agentbus suspend",
		env: envStallDuration, file: fileDuration(true)},
	// Suspend (§12 Q6): the workspace hook resumes a suspended project by
	// itself, only after a reboot or a crash of this host; off by default.
	{Name: "suspend.auto_resume", Env: "AGENT_BUS_SUSPEND_AUTO_RESUME", Kind: Bool, Default: false, ReadBy: "the workspace hook",
		env: envZeroOne, file: fileBool},
	// Forge delivery (spec 2026-11-01-forge-delivery-design.md): the forge
	// agentbus deliver uses (auto reads origin) and the branch it targets;
	// one setting for the machine, never a project's.
	{Name: "delivery.forge", Env: "AGENT_BUS_DELIVERY_FORGE", Kind: String, Default: "auto", ReadBy: "agentbus deliver", MachineOnly: true,
		env: func(raw string) (any, envResult) {
			if isForge(raw) {
				return raw, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && isForge(s) {
				return s, ""
			}
			return nil, "want auto, github, gitlab, bitbucket or local"
		}},
	{Name: "delivery.base", Env: "AGENT_BUS_DELIVERY_BASE", Kind: String, Default: "main", ReadBy: "agentbus deliver", MachineOnly: true,
		env: func(raw string) (any, envResult) {
			if ValidBranchName(raw) {
				return raw, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && ValidBranchName(s) {
				return s, ""
			}
			return nil, "want a Git branch name"
		}},
	// Backups (spec 2026-11-01-backup-design.md): where archives go (an
	// absolute folder; "" = $XDG_DATA_HOME/agentbus/backups, BackupDir) and
	// how many of this host's are kept (0 keeps every one).
	{Name: "backup.schedule", Env: "AGENT_BUS_BACKUP_SCHEDULE", Kind: String, Default: "off", ReadBy: "agentbus backup, dispatch",
		env: func(raw string) (any, envResult) {
			if raw == "off" || raw == "daily" {
				return raw, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && (s == "off" || s == "daily") {
				return s, ""
			}
			return nil, "want off or daily"
		}},
	{Name: "backup.dir", Env: "AGENT_BUS_BACKUP_DIR", Kind: String, Default: "", ReadBy: "agentbus backup, dispatch",
		env: func(raw string) (any, envResult) {
			if filepath.IsAbs(raw) {
				return raw, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && (s == "" || filepath.IsAbs(s)) {
				return s, ""
			}
			return nil, "want an absolute folder"
		}},
	{Name: "backup.keep", Env: "AGENT_BUS_BACKUP_KEEP", Kind: Int, Default: 7, ReadBy: "agentbus backup, dispatch",
		env: envNonNegativeInt, file: fileInt(func(n int) bool { return n >= 0 }, "want a whole number, 0 keeps every archive")},
	// The age public key --secrets encrypts to (backup spec, answer 4): a
	// recipient, never a private key or a passphrase.
	// The health reminder: an archive covering each project at least this
	// recent (0 turns the reminder off; a failure still warns).
	{Name: "backup.warn_after", Env: "AGENT_BUS_BACKUP_WARN_AFTER", Kind: Duration, Default: 7 * 24 * time.Hour, ReadBy: "dispatch (health)",
		env: func(raw string) (any, envResult) {
			if d, err := time.ParseDuration(raw); err == nil && d >= 0 {
				return d, envUse
			}
			if raw == "0" {
				return time.Duration(0), envUse
			}
			return nil, envSkip
		},
		file: fileDuration(true)},
	{Name: "backup.age_recipient", Env: "AGENT_BUS_BACKUP_AGE_RECIPIENT", Kind: String, Default: "", ReadBy: "agentbus backup, dispatch",
		env: func(raw string) (any, envResult) {
			if ValidAgeRecipient(raw) {
				return raw, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && (s == "" || ValidAgeRecipient(s)) {
				return s, ""
			}
			return nil, "want an age public key (age1...)"
		}},
	// A command of the person's own run after each archive (backup spec,
	// "After the archive"), the variable names it may keep despite looking
	// secret, and its bound: config.toml only.
	{Name: "backup.after_command", Kind: String, Default: "", ReadBy: "agentbus backup, dispatch", MachineOnly: true, PersonOnly: true,
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && !strings.ContainsAny(s, "\n\r") {
				return s, ""
			}
			return nil, "want one command line"
		}},
	{Name: "backup.after_env", Kind: String, List: true, Default: "", ReadBy: "agentbus backup, dispatch", MachineOnly: true, PersonOnly: true,
		file: fileEnvNames},
	{Name: "backup.after_timeout", Kind: Duration, Default: 30 * time.Minute, ReadBy: "agentbus backup, dispatch", MachineOnly: true, PersonOnly: true,
		file: fileDuration(false)},
	// Notifications (spec 2026-11-03-notifications-design.md): the host's,
	// config.toml only, none a secret (the targets' URLs are in notify.toml).
	{Name: "notify.on", Kind: Bool, Default: false, ReadBy: "dispatch", MachineOnly: true, file: fileBool},
	{Name: "notify.targets", Kind: String, List: true, Default: "", ReadBy: "dispatch, agentbus notifications", MachineOnly: true,
		file: fileNameList("want a list of target names from notify.toml", func(s string) bool { return validName(s) })},
	{Name: "notify.events", Kind: String, List: true, Default: "decision,stall,fail,blocked,prompt", ReadBy: "dispatch", MachineOnly: true,
		file: fileNameList("want a list of: "+strings.Join(NotifyEvents, ", "), notifyEvent)},
	{Name: "notify.detail", Kind: String, Default: "summary", ReadBy: "dispatch", MachineOnly: true,
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && (s == "summary" || s == "full") {
				return s, ""
			}
			return nil, "want summary or full"
		}},
	{Name: "notify.delay", Kind: Duration, Default: 2 * time.Minute, ReadBy: "dispatch", MachineOnly: true, file: fileDuration(true)},
	{Name: "notify.max_per_hour", Kind: Int, Default: 6, ReadBy: "dispatch", MachineOnly: true, file: filePositiveInt},
	{Name: "notify.quiet", Kind: String, Default: "", ReadBy: "dispatch", MachineOnly: true,
		file: func(v any) (any, string) {
			s, ok := v.(string)
			if ok && s == "" {
				return s, ""
			}
			if _, valid := ParseQuiet(s); ok && valid {
				return s, ""
			}
			return nil, "want HH:MM-HH:MM (local time), or \"\" for none"
		}},
	{Name: "notify.quiet_exempt", Kind: String, List: true, Default: "fail,stall", ReadBy: "dispatch", MachineOnly: true,
		file: fileNameList("want a list of: "+strings.Join(NotifyEvents, ", "), notifyEvent)},
	{Name: "bus.report_max", Env: "AGENT_BUS_REPORT_MAX", Kind: Int, Default: 500, ReadBy: "each writer (per writer)", env: envPositiveInt, file: filePositiveInt},
	{Name: "bus.report_full_max", Env: "AGENT_BUS_REPORT_FULL_MAX", Kind: Int, Default: 8000, ReadBy: "each writer (per writer)", env: envPositiveInt, file: filePositiveInt},
	{Name: "bus.verdict_max", Env: "AGENT_BUS_VERDICT_MAX", Kind: Int, Default: 10000, ReadBy: "each writer (per writer)", env: envPositiveInt, file: filePositiveInt},
	// The disk check (disk-check): a device warns under either warn bound and
	// fails under either fail bound (its free percentage, its free GiB).
	{Name: "health.disk_warn_pct", Env: "AGENT_BUS_DISK_WARN_PCT", Kind: Float, Default: 10.0, ReadBy: "dispatch (health)", env: envPositiveFloat, file: filePositivePercent},
	{Name: "health.disk_warn_gib", Env: "AGENT_BUS_DISK_WARN_GIB", Kind: Int, Default: 5, ReadBy: "dispatch (health)", env: envPositiveInt, file: filePositiveInt},
	{Name: "health.disk_fail_pct", Env: "AGENT_BUS_DISK_FAIL_PCT", Kind: Float, Default: 2.0, ReadBy: "dispatch (health)", env: envPositiveFloat, file: filePositivePercent},
	{Name: "health.disk_fail_gib", Env: "AGENT_BUS_DISK_FAIL_GIB", Kind: Int, Default: 1, ReadBy: "dispatch (health)", env: envPositiveInt, file: filePositiveInt},
	{Name: "health.runaway.silent_after", Env: "AGENTBUS_HEALTH_RUNAWAY_SILENT_AFTER", Kind: Duration, Default: 30 * time.Minute, ReadBy: "dispatch (health)", Invalid: runawayInvalid, env: envRunawayDuration, file: filePositiveDuration},
	{Name: "health.runaway.context_growth", Env: "AGENTBUS_HEALTH_RUNAWAY_CONTEXT_GROWTH", Kind: Int, Default: 100000, ReadBy: "dispatch (health)", Invalid: runawayInvalid, env: envRunawayInt, file: filePositiveInt},
	{Name: "health.runaway.growth_span", Env: "AGENTBUS_HEALTH_RUNAWAY_GROWTH_SPAN", Kind: Duration, Default: 10 * time.Minute, ReadBy: "dispatch (health)", Invalid: runawayInvalid, env: envRunawayDuration, file: filePositiveDuration},
	{Name: "health.runaway.screen_repeats", Env: "AGENTBUS_HEALTH_RUNAWAY_SCREEN_REPEATS", Kind: Int, Default: 4, ReadBy: "dispatch (health)", Invalid: runawayInvalid, env: envRunawayInt, file: filePositiveInt},
	// model-detection §3.2: a catalogue whose source is older than this
	// withholds gone and newer, and the model-catalogue check names it.
	{Name: "models.source_max_age", Env: "AGENT_BUS_MODELS_SOURCE_MAX_AGE", Kind: Duration, Default: 7 * 24 * time.Hour, ReadBy: "agentbus models, roles doctor, dispatch (health)",
		env: func(raw string) (any, envResult) {
			if d, ok := parseAge(raw); ok {
				return d, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok {
				if d, ok := parseAge(s); ok {
					return d, ""
				}
			}
			return nil, "want days such as \"7d\", or a duration such as \"36h\", above 0 and at most 365d"
		}},
}

// fileTeam is start.team's rule: "" or role[:profile] items, comma-separated,
// each a name, no role twice.
func fileTeam(v any) (any, string) {
	const want = "want role or role:profile, comma-separated"
	s, ok := v.(string)
	if !ok {
		return nil, want
	}
	if strings.TrimSpace(s) == "" {
		return s, ""
	}
	seen := map[string]bool{}
	for _, item := range strings.Split(s, ",") {
		role, profile, hasProfile := strings.Cut(strings.TrimSpace(item), ":")
		if !validName(role) || (hasProfile && !validName(profile)) {
			return nil, want
		}
		if seen[role] {
			return nil, role + " twice"
		}
		seen[role] = true
	}
	return s, ""
}

// envNameRE is an environment variable's name.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// fileEnvNames is backup.after_env's rule: a list of variable names, kept
// as one comma-separated string.
func fileEnvNames(v any) (any, string) {
	const want = "want a list of variable names"
	list, ok := v.([]any)
	if !ok {
		return nil, want
	}
	names := make([]string, 0, len(list))
	for _, x := range list {
		s, ok := x.(string)
		if !ok || !envNameRE.MatchString(s) {
			return nil, want
		}
		names = append(names, s)
	}
	return strings.Join(names, ","), ""
}

// Lookup is the row of a file key.
func Lookup(name string) (*Key, bool) {
	for _, k := range Keys {
		if k.Name == name {
			return k, true
		}
	}
	return nil, false
}

// validName is bus.ValidName's rule, repeated because bus reads its caps from
// this package; TestValidNameIsTheBusRule keeps the two equal.
var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`).MatchString

func isTheme(s string) bool { return s == "auto" || s == "dark" || s == "light" }

func isIcons(s string) bool { return s == "off" || s == "nerd" || s == "agent-font" }

// envZeroOne takes 1 (on) and 0 (off) only; anything else is skipped.
func envZeroOne(raw string) (any, envResult) {
	switch raw {
	case "1":
		return true, envUse
	case "0":
		return false, envUse
	}
	return nil, envSkip
}

// envStallDuration: whole seconds (as prompt_grace) or a Go duration, 0 = off,
// at most a day; anything else falls back to the next source.
func envStallDuration(raw string) (any, envResult) {
	if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n <= 86400 {
		return time.Duration(n) * time.Second, envUse
	}
	if d, err := time.ParseDuration(raw); err == nil && d >= 0 && d <= 24*time.Hour {
		return d, envUse
	}
	return nil, envSkip
}

// idleViewOK: the idle view's delay is 0 (off) or from 1m to 24h; a few
// seconds would cover the dashboard while the person still reads it.
func idleViewOK(d time.Duration) bool { return d == 0 || (d >= time.Minute && d <= 24*time.Hour) }

// envIdleView: whole seconds or a Go duration, 0 or from 1m to 24h; anything
// else is skipped, so the file's value or the default (off) applies.
func envIdleView(raw string) (any, envResult) {
	if v, res := envStallDuration(raw); res == envUse && idleViewOK(v.(time.Duration)) {
		return v, envUse
	}
	return nil, envSkip
}

// fileIdleView: "0" or a Go duration from 1m to 24h; the settings page uses
// the same rule.
func fileIdleView(v any) (any, string) {
	if s, ok := v.(string); ok {
		if s == "0" {
			return time.Duration(0), ""
		}
		if d, err := time.ParseDuration(s); err == nil && idleViewOK(d) {
			return d, ""
		}
	}
	return nil, "want 0 (off) or a duration from 1m to 24h, such as \"10m\""
}

// envPositiveFloat: today a value <= 0 (or none that parses) is the
// dispatcher's default, never a setting.
func envPositiveFloat(raw string) (any, envResult) {
	if f, err := strconv.ParseFloat(raw, 64); err == nil && f > 0 {
		return f, envUse
	}
	return nil, envSkip
}

func envNonNegativeInt(raw string) (any, envResult) {
	if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
		return n, envUse
	}
	return nil, envSkip
}

func envPositiveInt(raw string) (any, envResult) {
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n, envUse
	}
	return nil, envSkip
}

const runawayInvalid = "the runaway check reads unknown"

// The runaway check reads unknown on an invalid value: it never falls through.
func envRunawayDuration(raw string) (any, envResult) {
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d, envUse
	}
	return nil, envInvalid
}

func envRunawayInt(raw string) (any, envResult) {
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n, envUse
	}
	return nil, envInvalid
}

func fileBool(v any) (any, string) {
	if b, ok := v.(bool); ok {
		return b, ""
	}
	return nil, "want true or false"
}

func filePercent(v any) (any, string) {
	var f float64
	switch n := v.(type) {
	case int64:
		f = float64(n)
	case float64:
		f = n
	default:
		return nil, "want a number from 0 to 100"
	}
	if !(f >= 0 && f <= 100) { // NaN fails both
		return nil, "want a number from 0 to 100"
	}
	return f, ""
}

func filePositivePercent(v any) (any, string) {
	if f, why := filePercent(v); why == "" && f.(float64) > 0 {
		return f, ""
	}
	return nil, "want a number above 0, up to 100"
}

func fileInt(ok func(int) bool, want string) func(any) (any, string) {
	return func(v any) (any, string) {
		if n, isInt := v.(int64); isInt && n == int64(int(n)) && ok(int(n)) {
			return int(n), ""
		}
		if want == "" {
			return nil, "want a whole number"
		}
		return nil, want
	}
}

var filePositiveInt = fileInt(func(n int) bool { return n > 0 }, "want a whole number above 0")

// parseAge is whole days ("7d") or a Go duration, above 0, at most 365 days.
func parseAge(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	var d time.Duration
	if n, ok := strings.CutSuffix(raw, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days <= 0 || days > 365 { // before multiplying: a huge count would wrap (reviewer-codex)
			return 0, false
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(raw); err != nil {
			return 0, false
		}
	}
	return d, d > 0 && d <= 365*24*time.Hour
}

// fileDuration is a Go duration string ("90s", "5m"); zeroOK lets "0" mean off
// (or immediately), as busmon.toml always has.
func fileDuration(zeroOK bool) func(any) (any, string) {
	return func(v any) (any, string) {
		want := "want a duration above 0, such as \"30m\""
		if zeroOK {
			want = "want a duration, such as \"30m\", or \"0\""
		}
		s, ok := v.(string)
		if !ok {
			return nil, want
		}
		if s == "0" {
			if zeroOK {
				return time.Duration(0), ""
			}
			return nil, want
		}
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 || (d == 0 && !zeroOK) {
			return nil, want
		}
		return d, ""
	}
}

var filePositiveDuration = fileDuration(false)
