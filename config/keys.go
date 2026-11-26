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
	"slices"
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
	// Project keys also have a value per project, in config.toml's
	// [project.<name>] table (quota settings §2.2), read through
	// ProjectValue: the quota thresholds and the single-approval delivery keys.
	Project bool
	// PersonOnly keys are the person's alone (backup.after_*): read from
	// config.toml only, never written by agentbus (Set and Unset refuse
	// them: edit config.toml yourself). They have no env variable and are
	// MachineOnly too.
	PersonOnly bool
	// ProjectOnly keys have no machine value: config.toml's
	// [project.<name>] table alone (delivery.same_provider_ok,
	// single-approval §4.8); a line in the machine's table is ignored with a
	// warning, so it never applies to every project at once.
	ProjectOnly bool
	// PersonEdit keys are the person's to set, from busmon's Shift+C or
	// config.toml (board.wip_limit, wip-limit r2 §3.3): Set and Unset refuse
	// them for everyone (agentbus config set and unset); SetAsPerson and
	// UnsetAsPerson, called by busmon alone, write them. A discipline, not a
	// boundary: a shell can still write the file.
	PersonEdit bool
	// List keys are a TOML list in the file, kept as one comma-joined string
	// (backup.after_env, notify.targets...): a page edits them as text
	// separated by commas.
	List bool
	// DevOnly are values the key takes only in development mode (dev): from
	// any source outside it, the key resolves Invalid, with Why naming it.
	DevOnly []string
	// FailClosed: a value the file's rule refuses resolves Invalid too, at
	// its line, never the next source down (a switch whose default is on:
	// self_repair = false must not leave it on, foureyes).
	FailClosed bool
	// Options, when set, is the key's fixed list of values (language-picker):
	// a page offers them as a list to choose from, never free text. The
	// key's own rules still judge every value (agentbus config set, a hand
	// edit of config.toml).
	Options func() []Option

	env  func(raw string) (any, envResult) // today's env rule, for a non-empty value
	file func(v any) (any, string)         // a decoded file value: the setting, or why not
}

var Keys = append([]*Key{
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
	// braille-sparklines §2, folded into display.rendering (tui-rendering §3,
	// the person's Q4): kept one release, mapped (block basic, braille fine,
	// ascii ascii) when display.rendering is not set.
	{Name: "display.graphs", Env: "AGENT_BUS_BUSMON_GRAPHS", Kind: String, Default: "auto", ReadBy: "busmon",
		env: func(raw string) (any, envResult) {
			if isGraphs(raw) {
				return raw, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && isGraphs(s) {
				return s, ""
			}
			return nil, "want auto, block, braille or ascii"
		}},
	// tui-rendering §3 (the person's Q1, Q4): how fine busmon draws its bars,
	// gauges and sparklines; auto is fine on a UTF-8 locale. It wins over
	// display.graphs when set; a locale without UTF-8 forces ascii.
	{Name: "display.rendering", Kind: String, Default: "auto", ReadBy: "busmon",
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && (s == "auto" || s == "fine" || s == "basic") {
				return s, ""
			}
			return nil, "want auto, fine or basic"
		}},
	// compose-at-file §5 (the person, Q1): the path busmon's @ inserts in the
	// compose window, absolute (the default) or relative to the project's
	// root. A home path (@~/) is always absolute.
	{Name: "compose.at_path", Kind: String, Default: "absolute", ReadBy: "busmon",
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && (s == "absolute" || s == "relative") {
				return s, ""
			}
			return nil, "want absolute or relative"
		}},
	// quota settings spec §2.3: how a reset (and any time to come) is written,
	// by busmon and by the dispatcher's messages. No environment variable: a
	// service reads config.toml as it is at each message, never a start-time
	// copy (foureyes on r3).
	{Name: "display.date_format", Kind: String, Default: "relative", ReadBy: "busmon, dispatch, agentbus quota",
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && (s == "relative" || s == "iso" || s == "clock12") {
				return s, ""
			}
			return nil, "want relative (today 13:59), iso (2026-11-18 13:59) or clock12 (today 1:59 PM)"
		}},
	// requests-plan-redesign (the person, Q1): the Work tab is the list (layout
	// A) below 130 columns of busmon's pane and the Kanban (layout B) from 130;
	// list or kanban keeps one at every width.
	{Name: "display.work_layout", Kind: String, Default: "auto", ReadBy: "busmon",
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && (s == "auto" || s == "list" || s == "kanban") {
				return s, ""
			}
			return nil, "want auto, list or kanban"
		}},
	// Activity's auto-scroll (palette spec §8.3): on, it scrolls with new
	// lines (a wheel up or a selection above the live window pauses it); off,
	// it never scrolls by itself and end/G jumps to the newest line once.
	{Name: "display.follow", Kind: Bool, Default: true, ReadBy: "busmon", file: fileBool},
	// q asks before busmon quits (busmon-quit-confirm, the person); off, q
	// quits at once. Ctrl-C always quits at once.
	{Name: "display.confirm_quit", Kind: Bool, Default: true, ReadBy: "busmon", file: fileBool},
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
	// The Board's alerts (views-redesign §3): a tracked request not accepted
	// this long after it was sent, or accepted with no reply this long; 0 off.
	{Name: "board.no_accept_after", Kind: Duration, Default: 30 * time.Minute, ReadBy: "busmon", file: fileDuration(true)},
	{Name: "board.no_reply_after", Kind: Duration, Default: 2 * time.Hour, ReadBy: "busmon", file: fileDuration(true)},
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
	// The person's own name (busmon-cli-flags item 3, the person's option A):
	// busmon's identity after --as and identity.as, before "pilot". An
	// invalid one is ignored, said, never a refusal.
	{Name: "person.name", Env: "AGENT_BUS_PERSON_NAME", Kind: String, Default: "", ReadBy: "busmon (its name after identity.as)",
		Invalid: "busmon writes as pilot",
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
	{Name: "person.language", Env: "AGENT_BUS_PERSON_LANGUAGE", Kind: String, Default: "en", ReadBy: "master's role skill (agentbus config)",
		Invalid: "master writes English", FailClosed: true, Options: languageOptions, // a refused value is en, said, never the next source
		env: func(raw string) (any, envResult) {
			if isLanguage(raw) {
				return raw, envUse
			}
			return nil, envInvalid
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && isLanguage(s) {
				return s, ""
			}
			return nil, "want an ISO 639-1 code in lower case: en, fr, de..."
		}},
	{Name: "updates.check", Env: "AGENT_BUS_NO_UPDATE_CHECK", Kind: Bool, Default: true, ReadBy: "busmon",
		env:  func(raw string) (any, envResult) { return raw != "1", envUse }, // 1 turns it off
		file: fileBool},
	// The automatic update (quiet-update-auto §6, §7; the person's answers):
	// install or only notify, when it may run, and when to tell master a
	// release is still waiting. The host's (the updater belongs to no
	// project), the person's: auto and max_wait in config.toml alone, the
	// window from busmon's Shift+C as well (Q2). No env variable.
	{Name: "updates.auto", Kind: String, Default: "notify", ReadBy: "agentbus update --auto (says whether it would install; installs from quiet-update step 4b), busmon (shown)", MachineOnly: true, PersonOnly: true,
		file: fileOneOf("notify", "install")},
	{Name: "updates.window", Kind: String, Default: "03:00-05:00", ReadBy: "busmon (shown; the updater reads it from quiet-update step 4d)", MachineOnly: true, PersonEdit: true,
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok {
				if _, _, ok := ParseWindow(s); ok {
					return s, ""
				}
			}
			return nil, "want a local time window HH:MM-HH:MM, such as \"03:00-05:00\" (it may cross midnight)"
		}},
	{Name: "updates.max_wait", Kind: Duration, Default: 7 * 24 * time.Hour, ReadBy: "busmon (shown; agentbus update --auto reads it from quiet-update step 4c)", MachineOnly: true, PersonOnly: true,
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok {
				if d, ok := parseAge(s); ok {
					return d, ""
				}
			}
			return nil, "want days such as \"7d\", or a duration such as \"36h\", above 0 and at most 365d"
		}},
	{Name: "dispatch.context_alert", Env: "AGENT_BUS_CONTEXT_ALERT", Kind: Float, Default: 80.0, ReadBy: "dispatch, busmon, health",
		env: func(raw string) (any, envResult) {
			if n, err := strconv.ParseFloat(raw, 64); err == nil && n >= 0 && n <= 100 {
				return n, envUse
			}
			return nil, envSkip
		},
		file: filePercent},
	// The context escalation (spec 2026-11-15-context-escalation-design.md
	// §3.2, §7): an alerted agent with no hand-off standing escalates (master
	// woken, the person told) at this fill, used only above context_alert
	// (0 = off), or this long after the alert (0 = off; at least 1m).
	{Name: "dispatch.context_urgent", Env: "AGENT_BUS_CONTEXT_URGENT", Kind: Float, Default: 90.0, ReadBy: "dispatch",
		env: func(raw string) (any, envResult) {
			if n, err := strconv.ParseFloat(raw, 64); err == nil && n >= 0 && n <= 100 {
				return n, envUse
			}
			return nil, envSkip
		},
		file: filePercent},
	// How long the dispatcher may skip an agent's auto-clear or pop-retire for
	// its background work before it tells the escalation target, once per
	// episode (background-work-visible §4a; 0 = off, else at least 1m).
	// The background judgement's never-counted process names
	// (background-ignore-helpers): the clients' own helpers, busy or not,
	// and a shell that only wraps them. A real command still counts: the
	// default names only the clients' own helpers, never a generic name such
	// as MainThread (every node or Python process), which would hide work
	// (master). The person may add names.
	{Name: "dispatch.background_ignore", Kind: String, List: true, Default: "codebase-memory,codex-code-mode", ReadBy: "dispatch", MachineOnly: true,
		file: fileNameList("want a list of process names", func(s string) bool {
			return s != "" && len(s) <= 64 && !strings.ContainsAny(s, ",\n\r\t ")
		})},
	{Name: "dispatch.background_notice_after", Env: "AGENT_BUS_BACKGROUND_NOTICE_AFTER", Kind: Duration, Default: 2 * time.Hour, ReadBy: "dispatch",
		env: func(raw string) (any, envResult) {
			if d, err := time.ParseDuration(raw); err == nil && (d == 0 || d >= time.Minute) {
				return d, envUse
			}
			if raw == "0" {
				return time.Duration(0), envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			d, why := fileDuration(true)(v)
			if why == "" && d.(time.Duration) > 0 && d.(time.Duration) < time.Minute {
				return nil, "want a duration of at least 1m, such as \"2h\", or \"0\""
			}
			return d, why
		}},
	{Name: "dispatch.context_handoff_deadline", Env: "AGENT_BUS_CONTEXT_HANDOFF_DEADLINE", Kind: Duration, Default: 20 * time.Minute, ReadBy: "dispatch",
		env: func(raw string) (any, envResult) {
			if d, err := time.ParseDuration(raw); err == nil && (d == 0 || d >= time.Minute) {
				return d, envUse
			}
			if raw == "0" {
				return time.Duration(0), envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			d, why := fileDuration(true)(v)
			if why == "" && d.(time.Duration) > 0 && d.(time.Duration) < time.Minute {
				return nil, "want a duration of at least 1m, such as \"20m\", or \"0\""
			}
			return d, why
		}},
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
	// agent-screen-blocked §3 (the person's Q3): an unrecognised screen
	// unchanged this long reads stuck in busmon and rings the desktop once per
	// episode (0 = off).
	{Name: "dispatch.stuck_after", Env: "AGENT_BUS_STUCK_AFTER", Kind: Duration, Default: 3 * time.Minute, ReadBy: "dispatch, busmon",
		env: func(raw string) (any, envResult) {
			if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n <= 86400 {
				return time.Duration(n) * time.Second, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if n, ok := v.(int64); ok && n >= 0 && n <= 86400 {
				return time.Duration(n) * time.Second, ""
			}
			return nil, "want whole seconds from 0 to 86400"
		}},
	// focus-notice: a wake waiting this long on the pane that has the focus
	// earns the person one desktop notice (0 = off).
	{Name: "dispatch.focus_notice", Env: "AGENT_BUS_FOCUS_NOTICE", Kind: Duration, Default: 30 * time.Second, ReadBy: "dispatch",
		env: envStallDuration, file: fileDuration(true)},
	{Name: "dispatch.quota", Env: "AGENT_BUS_QUOTA", Kind: Bool, Default: true, ReadBy: "dispatch",
		env:  func(raw string) (any, envResult) { return raw != "off", envUse }, // off turns it off
		file: fileBool},
	// The quota thresholds (quota settings spec §2.1): master warned and agents
	// with work told to finish their step at quota_warn; the run drained,
	// nothing new started, at quota_drain (unset: quota_warn's value, today's
	// behaviour); the provider held at quota_hold. warn <= drain <= hold, or
	// the dispatcher uses the defaults for all three. Per project too
	// ([project.<name>] in config.toml, ProjectValue), read each minute.
	{Name: "dispatch.quota_warn", Env: "AGENT_BUS_QUOTA_WARN", Kind: Float, Default: 85.0, ReadBy: "dispatch", Project: true, env: envPositiveFloat, file: filePositivePercent},
	{Name: "dispatch.quota_drain", Env: "AGENT_BUS_QUOTA_DRAIN", Kind: Float, Default: 85.0, ReadBy: "dispatch", Project: true, env: envPositiveFloat, file: filePositivePercent},
	{Name: "dispatch.quota_hold", Env: "AGENT_BUS_QUOTA_HOLD", Kind: Float, Default: 95.0, ReadBy: "dispatch", Project: true, env: envPositiveFloat, file: filePositivePercent},
	{Name: "dispatch.quota_max_age", Env: "AGENT_BUS_QUOTA_MAX_AGE", Kind: Duration, Default: 30 * time.Minute, ReadBy: "dispatch", Project: true,
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
	// claude-usage-probe: Claude's windows from the CLI's get_usage first,
	// the statusline as the fallback; 0 turns the probe off.
	{Name: "quota.claude.probe", Env: "AGENT_BUS_QUOTA_CLAUDE_PROBE", Kind: Bool, Default: true, ReadBy: "quota",
		env:  func(raw string) (any, envResult) { return raw != "0", envUse },
		file: fileBool},
	{Name: "dispatch.tokens", Env: "AGENT_BUS_TOKENS", Kind: Bool, Default: true, ReadBy: "dispatch",
		env:  func(raw string) (any, envResult) { return raw != "0", envUse }, // 0 turns it off
		file: fileBool},
	// On by default (autoclear-default, the person's decision); 0 is the off
	// switch, 1 on, and any other value is skipped (the next source applies),
	// so true or yes never turns it off.
	{Name: "dispatch.auto_clear", Env: "AGENT_BUS_AUTO_CLEAR", Kind: Bool, Default: true, ReadBy: "dispatch",
		env:  envZeroOne,
		file: fileBool},
	// auto-compact (the person's answer A): at dispatch.context_urgent for
	// 10 minutes with no hand-off declared, the dispatcher types /compact
	// into the idle pane. On by default; the person's alone (no env, busmon
	// or config.toml), read again at each minute's judgement.
	{Name: "dispatch.auto_compact", Kind: Bool, Default: true, ReadBy: "dispatch", MachineOnly: true, PersonEdit: true,
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
	// idle-master (spec 2026-11-15-master-idle-design.md §6, the person's
	// answers): master idle this long with work in hand is woken once (15
	// minutes; 0 is off); the same check-back set more than checkback_max
	// times tells the person once (3; 0 never).
	{Name: "dispatch.master_idle_after", Env: "AGENT_BUS_MASTER_IDLE_AFTER", Kind: Duration, Default: 15 * time.Minute, ReadBy: "dispatch",
		env: envStallDuration, file: fileDuration(true)},
	{Name: "dispatch.checkback_max", Env: "AGENT_BUS_CHECKBACK_MAX", Kind: Int, Default: 3, ReadBy: "dispatch",
		env: envNonNegativeInt, file: fileInt(func(n int) bool { return n >= 0 }, "want a whole number, 0 never tells the person")},
	// Claude Code's login expiry (login-updates spec r9 §3.4): a login that
	// expires within this warns the person once per agent and expiry; 0 turns
	// warnings off (an expired login still fails).
	{Name: "dispatch.login_expiry_warn", Env: "AGENT_BUS_LOGIN_EXPIRY_WARN", Kind: Duration, Default: 72 * time.Hour, ReadBy: "dispatch",
		env: envNonNegativeDuration, file: fileDuration(true)},
	// The run budget (spec 2026-11-15-run-budget-design.md §3): a run drains
	// after this much unpaused time, or this many deliveries; 0 is no limit
	// (the default). agentbus run shows them; the dispatcher acts on them.
	{Name: "run.max_duration", Env: "AGENT_BUS_RUN_MAX_DURATION", Kind: Duration, Default: time.Duration(0), ReadBy: "dispatch",
		env: envNonNegativeDuration, file: fileDuration(true)},
	{Name: "run.max_deliveries", Env: "AGENT_BUS_RUN_MAX_DELIVERIES", Kind: Int, Default: 0, ReadBy: "dispatch",
		env: envNonNegativeInt, file: fileInt(func(n int) bool { return n >= 0 }, "want a whole number, 0 for no limit")},
	// A draining run stops (the project pauses) when nothing is in flight,
	// or this long after the drain began; 0 drains until nothing is in flight.
	{Name: "run.drain_max", Env: "AGENT_BUS_RUN_DRAIN_MAX", Kind: Duration, Default: 2 * time.Hour, ReadBy: "dispatch",
		env: envNonNegativeDuration, file: fileDuration(true)},
	// The CI loop (spec 2026-11-19-ci-loop-design.md): a PR whose checks fail
	// goes back to its head's author at most ci.max_handoffs times, then to
	// the person; 0 hand-offs sends every red PR straight to the stop. A
	// repository with no CI stays silent, so on costs nothing.
	{Name: "ci.loop", Env: "AGENT_BUS_CI_LOOP", Kind: Bool, Default: true, ReadBy: "dispatch",
		env:  func(raw string) (any, envResult) { return raw != "off", envUse }, // off turns it off
		file: fileBool},
	{Name: "ci.max_handoffs", Env: "AGENT_BUS_CI_MAX_HANDOFFS", Kind: Int, Default: 3, ReadBy: "dispatch",
		env: envNonNegativeInt, file: fileInt(func(n int) bool { return n >= 0 }, "want a whole number, 0 to stop at once")},
	// A conflicting approved PR (conflict-owner §4): back to its owner once
	// per conflicting head, at most conflict.max_handoffs times, then to
	// master; master too once the conflict is conflict.escalate_after old
	// (from its first sight, the owner held or not). Both 0: master only.
	{Name: "conflict.max_handoffs", Env: "AGENT_BUS_CONFLICT_MAX_HANDOFFS", Kind: Int, Default: 3, ReadBy: "dispatch",
		env: envNonNegativeInt, file: fileInt(func(n int) bool { return n >= 0 }, "want a whole number, 0 to send every conflict to master")},
	{Name: "conflict.escalate_after", Env: "AGENT_BUS_CONFLICT_ESCALATE_AFTER", Kind: Duration, Default: 2 * time.Hour, ReadBy: "dispatch",
		env: envNonNegativeDuration, file: fileDuration(true)},
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
	// Self-repair (spec 2026-11-09-self-repair-watcher-design.md, 4.5).
	// Development mode: one boolean, read once at start, that only ever
	// unlocks extra values (shadow); it never weakens a check. 1 on, 0 off,
	// any other env value skipped. A top-level key: dev = true.
	{Name: "dev", Env: "AGENT_BUS_DEV", Kind: Bool, Default: false, ReadBy: "dispatch, busmon",
		env: envZeroOne, file: fileBool},
	// The watcher's switch, on by default (the person, Q1); shadow records
	// what it would do and acts on nothing, in development mode only. A value
	// its rule refuses, from the env or the file, or shadow outside dev, is
	// refused: the watcher stays off (SelfRepairMode).
	{Name: "dispatch.self_repair", Env: "AGENT_BUS_SELF_REPAIR", Kind: String, Default: "on", ReadBy: "dispatch",
		Invalid: "self-repair stays off", DevOnly: []string{"shadow"}, FailClosed: true,
		env: func(raw string) (any, envResult) {
			if isSelfRepairMode(raw) {
				return raw, envUse
			}
			return nil, envInvalid
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && isSelfRepairMode(s) {
				return s, ""
			}
			return nil, "want on, off or shadow (shadow in development mode only)"
		}},
	// Its budgets per agent (the person, Q5): one relaunch per this long, at
	// most this many a day; a fourth death in a day is a crash loop.
	{Name: "dispatch.self_repair_every", Env: "AGENT_BUS_SELF_REPAIR_EVERY", Kind: Duration, Default: 30 * time.Minute, ReadBy: "dispatch",
		env: envPositiveDuration, file: filePositiveDuration},
	{Name: "dispatch.self_repair_per_day", Env: "AGENT_BUS_SELF_REPAIR_PER_DAY", Kind: Int, Default: 3, ReadBy: "dispatch",
		env: envPositiveInt, file: filePositiveInt},
	// The holder (herdr exit, spec 2026-11-10-herdr-exit-design.md, 4.2, 4.4,
	// 4.5): each terminal's scrollback; how long a submit waits for the pasted
	// text to show at the input before it refuses the Enter; how long after the
	// person typed an agent is left alone. Read when the holder starts.
	{Name: "holder.scrollback", Env: "AGENT_BUS_HOLDER_SCROLLBACK", Kind: Int, Default: 5000, ReadBy: "agentbus holder",
		env: envPositiveInt, file: filePositiveInt},
	// Cap A, the holder unit's memory (spec
	// 2026-11-25-agent-resource-limits-design.md §3, §8; the person's Q1):
	// systemd's syntax, "off" for no limit. The person's alone and per host
	// (RAM differs), written into the unit by agentbus up, whose reload
	// applies it to the running holder at once: below its current use, the
	// kernel reclaims or kills then.
	{Name: "limits.holder_memory_max", Kind: String, Default: "70%", ReadBy: "agentbus up (the holder unit's MemoryMax; the running holder's at once)", MachineOnly: true, PersonOnly: true,
		file: fileMemoryLimit},
	{Name: "limits.holder_memory_high", Kind: String, Default: "60%", ReadBy: "agentbus up (the holder unit's MemoryHigh, a soft throttle; the running holder's at once)", MachineOnly: true, PersonOnly: true,
		file: fileMemoryLimit},
	{Name: "limits.holder_swap_max", Kind: String, Default: "4G", ReadBy: "agentbus up (the holder unit's MemorySwapMax; the running holder's at once)", MachineOnly: true, PersonOnly: true,
		file: fileMemoryLimit},
	{Name: "dispatch.submit_wait", Env: "AGENT_BUS_SUBMIT_WAIT", Kind: Duration, Default: 2 * time.Second, ReadBy: "agentbus holder",
		env: envBoundedDuration(time.Minute), file: fileBoundedDuration(time.Minute)},
	{Name: "dispatch.person_quiet", Env: "AGENT_BUS_PERSON_QUIET", Kind: Duration, Default: 3 * time.Second, ReadBy: "agentbus holder",
		env: envBoundedDuration(time.Hour), file: fileBoundedDuration(time.Hour)},
	// The machine's service manager (herdr exit, spec 4.4b): auto (systemd
	// when systemctl --user answers, launchd on macOS, else none), systemd,
	// launchd or none: detached supervisors (holder.Supervised, 6c3). The
	// variable is the one it has always been.
	{Name: "services.manager", Env: "AGENTBUS_SERVICE_MANAGER", Kind: String, Default: "auto", MachineOnly: true,
		ReadBy: "agentbus up, service, down, health, restore, suspend; busmon",
		env: func(raw string) (any, envResult) {
			if isServicesManager(raw) {
				return raw, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && isServicesManager(s) {
				return s, ""
			}
			return nil, "want auto, systemd, launchd or none"
		}},
	// agentbus attach's prefix key (herdr exit spec 4.6): <prefix> d detaches,
	// <prefix> <prefix> sends one. f1-f12 or ctrl+<letter>, ctrl+\, ctrl+];
	// never ctrl+c/d/h/i/j/m/q/s/z. Gated per client at each attach.
	{Name: "attach.prefix", Env: "AGENT_BUS_ATTACH_PREFIX", Kind: String, Default: "f12", ReadBy: "agentbus attach, busmon",
		Invalid: "agentbus attach refuses to start",
		env: func(raw string) (any, envResult) {
			if AttachPrefixAllowed(raw) {
				return raw, envUse
			}
			return nil, envInvalid
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && AttachPrefixAllowed(s) {
				return s, ""
			}
			return nil, "want f1-f12, ctrl+<letter>, ctrl+\\ or ctrl+] (not ctrl+c/d/h/i/j/m/q/s/z)"
		}},
	// Start at login (the person, Q8): the project's services start when the
	// person logs in: agentbus up and agentbus service login apply it through
	// service.LoginStart (step 9: systemd, launchd; refused elsewhere).
	{Name: "services.start_at_login", Kind: Bool, Default: false, ReadBy: "agentbus up, agentbus service login", MachineOnly: true, file: fileBool},
	// After an unplanned stop of the machine (reboot-resume §3.1, §6.3): the
	// dispatch service restarts the team by itself, at most unplanned_max
	// times in unplanned_window; one more stops the project. Machine-only:
	// they judge this host's reboots.
	{Name: "services.unplanned_restart", Env: "AGENT_BUS_UNPLANNED_RESTART", Kind: Bool, Default: true, ReadBy: "dispatch",
		MachineOnly: true, env: envZeroOne, file: fileBool},
	{Name: "services.unplanned_max", Env: "AGENT_BUS_UNPLANNED_MAX", Kind: Int, Default: 3, ReadBy: "dispatch",
		MachineOnly: true, env: envIntRange(1, 10), file: fileInt(func(n int) bool { return n >= 1 && n <= 10 }, "want a whole number from 1 to 10")},
	{Name: "services.unplanned_window", Env: "AGENT_BUS_UNPLANNED_WINDOW", Kind: Duration, Default: 24 * time.Hour, ReadBy: "dispatch",
		MachineOnly: true, env: envDurationRange(time.Hour, 7*24*time.Hour), file: fileDurationRange(time.Hour, 7*24*time.Hour)},
	// The history archive (spec 2026-11-01-history-design.md): days of day
	// files kept (0 = no archive) and the directory's cap in MiB.
	{Name: "history.keep_days", Env: "AGENT_BUS_HISTORY_DAYS", Kind: Int, Default: 90, ReadBy: "dispatch",
		env: envNonNegativeInt, file: fileInt(func(n int) bool { return n >= 0 }, "want a whole number, 0 for no archive")},
	{Name: "history.max_mb", Env: "AGENT_BUS_HISTORY_MAX_MB", Kind: Int, Default: 256, ReadBy: "dispatch", env: envPositiveInt, file: filePositiveInt},
	// The audit trail's age (audit spec §3, the person's Q1: 90 days): every
	// append trims entries older than this. The person's alone: an agent
	// must not shorten the trail it is in.
	{Name: "audit.retention", Kind: Duration, Default: 90 * 24 * time.Hour, ReadBy: "bus.Audit at each append, in the process that writes the line: agentbus's acts, dispatch's (CI hand-off, quota pause, merge), the person's answers and pauses", MachineOnly: true, PersonOnly: true,
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok {
				if d, ok := parseAge(s); ok {
					return d, ""
				}
			}
			return nil, "want days such as \"90d\", or a duration such as \"36h\", above 0 and at most 365d"
		}},
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
	// one setting for the machine, never read from a project's env file. A
	// project's template may name its own base ([delivery] base, rolecfg.
	// DeliveryBase, dev-branch-flow §4.1), above delivery.base.
	// The work-in-progress limit (wip-limit r2 §3.3): requests an agent may
	// hold accepted when it starts a new one, 0 = no limit; per project, the
	// person's alone (Q3), from Shift+C or config.toml, no env variable.
	{Name: "board.wip_limit", Kind: Int, Default: 2, ReadBy: "agentbus request accept, agentbus request send, busmon", MachineOnly: true, Project: true, PersonEdit: true,
		file: fileInt(func(n int) bool { return n >= 0 }, "want a whole number, 0 or more (0: no limit)")},
	{Name: "delivery.forge", Env: "AGENT_BUS_DELIVERY_FORGE", Kind: String, Default: "auto", ReadBy: "agentbus deliver, dispatch", MachineOnly: true,
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
			return nil, "want auto, github, gitlab, bitbucket, none or local"
		}},
	{Name: "delivery.base", Env: "AGENT_BUS_DELIVERY_BASE", Kind: String, Default: "main", ReadBy: "agentbus deliver, agentbus missions, dispatch", MachineOnly: true,
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
	// The broker's container runtime (broker-hardening step 3): the binary
	// every caller of the container runs (brokerrt), with "<runtime> compose".
	// One approval for a small change (spec 2026-11-21-single-approval-design
	// §3.1, §4.8), per project, read by agentbus deliver. The three that widen
	// the gate and same_provider_ok are the person's (§6 Q3); never_small
	// only narrows it (globs added to deliver's defaults, never removing one).
	{Name: "delivery.single_approval", Kind: String, Default: "off", ReadBy: "agentbus deliver", MachineOnly: true, Project: true, PersonOnly: true,
		file: fileOneOf("off", "small")},
	{Name: "delivery.small_max_lines", Kind: Int, Default: 150, ReadBy: "agentbus deliver", MachineOnly: true, Project: true, PersonOnly: true,
		file: filePositiveInt},
	{Name: "delivery.small_max_files", Kind: Int, Default: 5, ReadBy: "agentbus deliver", MachineOnly: true, Project: true, PersonOnly: true,
		file: filePositiveInt},
	{Name: "delivery.never_small", Kind: String, List: true, Default: "", ReadBy: "agentbus deliver", MachineOnly: true, Project: true,
		file: fileNameList("want a list of path globs, such as cmd/busmon/** or *.sql", func(s string) bool {
			return strings.TrimSpace(s) != "" && !strings.ContainsAny(s, "\n\r,")
		})},
	{Name: "delivery.same_provider_ok", Kind: String, Default: "off", ReadBy: "agentbus deliver", MachineOnly: true, Project: true, PersonOnly: true, ProjectOnly: true,
		file: fileOneOf("off", "docs", "small")},
	{Name: "broker.runtime", Env: "AGENT_BUS_BROKER_RUNTIME", Kind: String, Default: "docker", ReadBy: "./setup.sh, agentbus up, dispatch, busmon", MachineOnly: true,
		env: func(raw string) (any, envResult) {
			if IsBrokerRuntime(raw) {
				return raw, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && IsBrokerRuntime(s) {
				return s, ""
			}
			return nil, "want docker or podman"
		}},
	// The broker's users (broker-hardening step 4b): off, every process logs
	// in as the default user (requirepass), as before; on, each logs in as its
	// role's ACL user (bus.ACLUserFor) with its own password file. The person
	// turns it on with ./setup.sh acl on, which also writes the users.
	{Name: "broker.acl", Env: "AGENT_BUS_BROKER_ACL", Kind: String, Default: "off", ReadBy: "agentbus (the services included), busmon", MachineOnly: true,
		env: func(raw string) (any, envResult) {
			if IsBrokerACL(raw) {
				return raw, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && IsBrokerACL(s) {
				return s, ""
			}
			return nil, "want off or on"
		}},
	// The broker's image (broker-hardening step 3, the person's Q5): what
	// compose runs, redis:8-alpine by default, valkey/valkey:8 accepted. Every
	// compose call passes it (brokerrt.ComposeEnv), since compose reads only
	// its environment.
	{Name: "broker.image", Env: "AGENT_BUS_BROKER_IMAGE", Kind: String, Default: "redis:8-alpine", ReadBy: "./setup.sh, agentbus up", MachineOnly: true,
		env: func(raw string) (any, envResult) {
			if IsImageRef(raw) {
				return raw, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && IsImageRef(s) {
				return s, ""
			}
			return nil, "want an image reference, such as redis:8-alpine, valkey/valkey:8 or one pinned by @sha256:<digest>"
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
	// projects-admin §5.5 (the person, Q3, Q6, Q7): what a project's delete
	// (busmon's M page, busmon --delete) also removes from disk, both off.
	// delete_files: the template and the worktrees (never roles.toml, never
	// the checkout); delete_backups: the archives of the project alone,
	// never the one made just before the delete. No env: the machine's file.
	{Name: "projects.delete_files", Kind: Bool, Default: false, ReadBy: "busmon", MachineOnly: true, file: fileBool},
	{Name: "projects.delete_backups", Kind: Bool, Default: false, ReadBy: "busmon", MachineOnly: true, file: fileBool},
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
	// Cap C, one command run by agentbus run --cap (spec
	// 2026-11-25-agent-resource-limits-design.md §6, §8; the person's Q4):
	// the defaults a flag may only narrow. The person's alone and per host.
	{Name: "limits.command_memory", Kind: String, Default: "8G", ReadBy: "agentbus run --cap", MachineOnly: true, PersonOnly: true,
		file: fileSizeOrOff},
	{Name: "limits.command_time", Kind: Duration, Default: 30 * time.Minute, ReadBy: "agentbus run --cap (0: no time cap)", MachineOnly: true, PersonOnly: true,
		file: fileDuration(true)},
	{Name: "limits.command_cpu", Kind: Int, Default: 0, ReadBy: "agentbus run --cap (a CPU percentage, 0: no CPU cap; only where systemd-run runs the command)", MachineOnly: true, PersonOnly: true,
		file: fileInt(func(n int) bool { return n >= 0 }, "want a whole percentage, 0 for no CPU cap")},
}, quotaProviderKeys()...) // quota.<provider>.warn|drain|hold, quotaprovider.go

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
	return lookupQuotaProvider(name) // quota.<provider>.<level>, any provider id
}

// validName is bus.ValidName's rule, repeated because bus reads its caps from
// this package; TestValidNameIsTheBusRule keeps the two equal.
var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`).MatchString

func isServicesManager(s string) bool {
	return s == "auto" || s == "systemd" || s == "launchd" || s == "none"
}

func isTheme(s string) bool { return s == "auto" || s == "dark" || s == "light" }

func isSelfRepairMode(s string) bool { return s == "on" || s == "off" || s == "shadow" }

// envPositiveDuration: a Go duration above 0; anything else is skipped.
func envNonNegativeDuration(raw string) (any, envResult) {
	if d, err := time.ParseDuration(raw); err == nil && d >= 0 {
		return d, envUse
	}
	return nil, envSkip
}

func envPositiveDuration(raw string) (any, envResult) {
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d, envUse
	}
	return nil, envSkip
}

// envBoundedDuration: a Go duration above 0, at most max; anything else is skipped.
func envBoundedDuration(max time.Duration) func(string) (any, envResult) {
	return func(raw string) (any, envResult) {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 && d <= max {
			return d, envUse
		}
		return nil, envSkip
	}
}

// fileBoundedDuration is envBoundedDuration's rule for the file.
func fileBoundedDuration(max time.Duration) func(any) (any, string) {
	return func(v any) (any, string) {
		if s, ok := v.(string); ok {
			if d, err := time.ParseDuration(s); err == nil && d > 0 && d <= max {
				return d, ""
			}
		}
		return nil, "want a duration above 0, at most " + max.String() + ", such as \"2s\""
	}
}

func isIcons(s string) bool { return s == "off" || s == "nerd" || s == "agent-font" }
func isGraphs(s string) bool {
	return s == "auto" || s == "block" || s == "braille" || s == "ascii"
}

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

// fileMemoryLimit is a systemd memory limit (agent-resource-limits §8):
// "off", a whole percentage of RAM from 1 to 100 ("70%"), or a size above 0,
// in bytes or with a K, M, G or T suffix ("4G", "1.5G"). Nothing else, so the
// value is one unit-file word.
func fileMemoryLimit(v any) (any, string) {
	const want = "want a size such as \"4G\", a percentage of RAM such as \"70%\", or \"off\""
	s, ok := v.(string)
	if !ok {
		return nil, want
	}
	if s == "off" {
		return s, ""
	}
	if pct, isPct := strings.CutSuffix(s, "%"); isPct {
		if n, err := strconv.Atoi(pct); err == nil && n >= 1 && n <= 100 && strconv.Itoa(n) == pct {
			return s, ""
		}
		return nil, want
	}
	num := strings.TrimRight(s, "KMGT")
	if len(s)-len(num) > 1 || num == "" || strings.Trim(num, "0123456789.") != "" || strings.Count(num, ".") > 1 ||
		num[0] == '.' || num[len(num)-1] == '.' {
		return nil, want
	}
	if f, err := strconv.ParseFloat(num, 64); err != nil || f <= 0 {
		return nil, want
	}
	return s, ""
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

// Valid says whether v, as the file decodes it (a string for a duration, an
// int64, a float64, a bool), passes the key's own file rule: the settings
// page asks it which characters a field may take (a negative, a day count).
func (k *Key) Valid(v any) bool {
	if k.file == nil {
		return true
	}
	_, why := k.file(v)
	return why == ""
}

// envIntRange is a whole number from lo to hi.
func envIntRange(lo, hi int) func(string) (any, envResult) {
	return func(raw string) (any, envResult) {
		if n, err := strconv.Atoi(raw); err == nil && n >= lo && n <= hi {
			return n, envUse
		}
		return nil, envSkip
	}
}

// envDurationRange is a duration from lo to hi.
func envDurationRange(lo, hi time.Duration) func(string) (any, envResult) {
	return func(raw string) (any, envResult) {
		if d, err := time.ParseDuration(raw); err == nil && d >= lo && d <= hi {
			return d, envUse
		}
		return nil, envSkip
	}
}

// fileDurationRange is envDurationRange's rule for the file.
func fileDurationRange(lo, hi time.Duration) func(any) (any, string) {
	return func(v any) (any, string) {
		if s, ok := v.(string); ok {
			if d, err := time.ParseDuration(s); err == nil && d >= lo && d <= hi {
				return d, ""
			}
		}
		return nil, "want a duration from " + lo.String() + " to " + hi.String() + ", such as \"24h\""
	}
}

// fileOneOf takes a string among values.
func fileOneOf(values ...string) func(any) (any, string) {
	return func(v any) (any, string) {
		if s, ok := v.(string); ok && slices.Contains(values, s) {
			return s, ""
		}
		return nil, "want " + strings.Join(values[:len(values)-1], ", ") + " or " + values[len(values)-1]
	}
}
