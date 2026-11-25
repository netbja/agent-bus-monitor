package config

// Applies is when a change of a key takes effect (settings-read-when): a
// change made in config.toml or busmon's Shift+C. It is the slowest of the
// key's readers: a key the dispatcher reads once at its start applies on
// service restart even when a command also reads it at each run. A service
// reads its env file when it starts, except where a reader reloads it at each
// call: the quota thresholds (serviceQuota) and the reload policy
// (serviceReloadPolicy), both live.
type Applies int

const (
	AppliesUnknown Applies = iota
	AppliesLive            // re-read while running: busmon at once, a service at its next pass
	AppliesRun             // read by a command each time it runs
	AppliesBusmon          // read when busmon starts
	AppliesService         // read when a service starts (dispatch, missions, holder)
	AppliesAgent           // read by an agent when it starts
)

func (a Applies) String() string {
	switch a {
	case AppliesLive:
		return "live"
	case AppliesRun:
		return "at the next run"
	case AppliesBusmon:
		return "on busmon restart"
	case AppliesService:
		return "on service restart"
	case AppliesAgent:
		return "on agent restart"
	}
	return ""
}

// Applies is when a change of k takes effect.
func (k *Key) Applies() Applies {
	if _, _, ok := ParseQuotaProviderKey(k.Name); ok { // read with the thresholds, each pass
		return AppliesLive
	}
	return applies[k.Name]
}

// applies is each key's, from its read sites; TestEveryKeySaysWhenItApplies
// fails on a key without. Live in busmon: the keys busmon's applySetting
// applies at once. Live in the dispatcher: read inside a check or a pass
// (stall checks, merge queue, notifications, health, quota readings).
var applies = map[string]Applies{
	// busmon, applied at once by Shift+C (cmd/busmon applySetting)
	"waiting.popup_after": AppliesLive, "waiting.snooze": AppliesLive, "waiting.bell": AppliesLive, "waiting.fade": AppliesLive,
	"recap.auto_after": AppliesLive, "display.theme": AppliesLive, "display.icons": AppliesLive, "display.graphs": AppliesLive, "display.rendering": AppliesLive,
	"compose.at_path": AppliesLive, "display.date_format": AppliesLive, "display.work_layout": AppliesLive, "display.follow": AppliesLive,
	"display.idle_view_after": AppliesLive, "display.confirm_quit": AppliesLive, "board.no_accept_after": AppliesLive, "board.no_reply_after": AppliesLive,
	"projects.delete_files": AppliesLive, "projects.delete_backups": AppliesLive,
	// busmon, read at its start
	"display.limit": AppliesBusmon, "display.idle_hint_after": AppliesBusmon, "identity.as": AppliesBusmon, "person.name": AppliesBusmon,
	"updates.check": AppliesBusmon, "updates.auto": AppliesBusmon, "updates.window": AppliesBusmon, "updates.max_wait": AppliesBusmon,
	// master reads it with agentbus config at its boot
	"person.language": AppliesAgent,
	// the dispatcher, built once at its start
	"dispatch.context_alert": AppliesService, "dispatch.context_urgent": AppliesService, "dispatch.context_handoff_deadline": AppliesService,
	"dispatch.background_notice_after": AppliesService, "dispatch.prompt_grace": AppliesService, "dispatch.stuck_after": AppliesService,
	"dispatch.focus_notice": AppliesService, "dispatch.quota": AppliesService, "dispatch.quota_long_hold": AppliesService,
	"quota.claude.probe": AppliesService, "dispatch.tokens": AppliesService, "dispatch.auto_clear": AppliesService,
	"dispatch.pop_retire": AppliesService, "dispatch.pop_retire_after": AppliesService, "dev": AppliesService,
	"dispatch.self_repair": AppliesService, "dispatch.self_repair_every": AppliesService, "dispatch.self_repair_per_day": AppliesService,
	"history.keep_days": AppliesService, "history.max_mb": AppliesService, "audit.retention": AppliesLive, // read at each append
	"health.runaway.silent_after": AppliesService, "health.runaway.context_growth": AppliesService,
	"health.runaway.growth_span": AppliesService, "health.runaway.screen_repeats": AppliesService,
	// the holder at its start; missions reads the base at its start; a broker
	// connection is opened at a service's start
	"holder.scrollback": AppliesService, "dispatch.submit_wait": AppliesService, "dispatch.person_quiet": AppliesService,
	"delivery.base": AppliesService, "broker.runtime": AppliesService, "broker.acl": AppliesService,
	// the dispatcher, re-read at each pass or check
	"dispatch.background_ignore": AppliesLive, "dispatch.quota_warn": AppliesLive, "dispatch.quota_drain": AppliesLive,
	"dispatch.quota_hold": AppliesLive, "dispatch.quota_max_age": AppliesLive, "dispatch.auto_compact": AppliesLive,
	"dispatch.stall_after": AppliesLive, "dispatch.clear_stall_after": AppliesLive, "dispatch.quiet_after": AppliesLive,
	"dispatch.idle_after": AppliesLive, "dispatch.master_idle_after": AppliesLive, "dispatch.checkback_max": AppliesLive,
	"dispatch.login_expiry_warn": AppliesLive, "dispatch.turn_stale_after": AppliesLive,
	"run.max_duration": AppliesLive, "run.max_deliveries": AppliesLive, "run.drain_max": AppliesLive,
	"ci.loop": AppliesLive, "ci.max_handoffs": AppliesLive, "conflict.max_handoffs": AppliesLive, "conflict.escalate_after": AppliesLive,
	"services.unplanned_restart": AppliesLive, "services.unplanned_max": AppliesLive, "services.unplanned_window": AppliesLive,
	"delivery.forge":  AppliesLive,
	"backup.schedule": AppliesLive, "backup.dir": AppliesLive, "backup.keep": AppliesLive, "backup.warn_after": AppliesLive,
	"backup.age_recipient": AppliesLive, "backup.after_command": AppliesLive, "backup.after_env": AppliesLive, "backup.after_timeout": AppliesLive,
	"notify.on": AppliesLive, "notify.targets": AppliesLive, "notify.events": AppliesLive, "notify.detail": AppliesLive,
	"notify.delay": AppliesLive, "notify.max_per_hour": AppliesLive, "notify.quiet": AppliesLive, "notify.quiet_exempt": AppliesLive,
	"health.disk_warn_pct": AppliesLive, "health.disk_warn_gib": AppliesLive, "health.disk_fail_pct": AppliesLive,
	"health.disk_fail_gib": AppliesLive, "models.source_max_age": AppliesLive,
	// each writer, at each write
	"bus.report_max": AppliesLive, "bus.report_full_max": AppliesLive, "bus.verdict_max": AppliesLive,
	// commands, at each run
	"services.manager": AppliesRun, "attach.prefix": AppliesRun, "services.start_at_login": AppliesRun, "start.team": AppliesRun,
	"sentinel.reload": AppliesLive, "suspend.wait": AppliesRun, "suspend.auto_resume": AppliesRun, "board.wip_limit": AppliesRun,
	"delivery.single_approval": AppliesRun, "delivery.small_max_lines": AppliesRun, "delivery.small_max_files": AppliesRun,
	"delivery.never_small": AppliesRun, "delivery.same_provider_ok": AppliesRun, "broker.image": AppliesRun,
}
