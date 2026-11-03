package config

// Group is a key's theme on busmon's settings page (spec
// docs/superpowers/specs/2026-11-01-busmon-config-page-mockups.md, "Groups"):
// not always the key's first segment (waiting.fade is Display, recap.* and
// updates.* are Attention).
type Group string

const (
	GroupDisplay   Group = "Display"
	GroupAttention Group = "Attention"
	GroupIdentity  Group = "Identity"
	GroupServices  Group = "Services"
	GroupHealth    Group = "Health"
	GroupBackup    Group = "Backup"
	GroupBusCaps   Group = "Bus caps"
)

// Groups is the page's order.
var Groups = []Group{GroupDisplay, GroupAttention, GroupIdentity, GroupServices, GroupHealth, GroupBackup, GroupBusCaps}

// groups is each key's group; TestEveryKeyHasItsGroup fails on a key without.
var groups = map[string]Group{
	"display.theme": GroupDisplay, "display.icons": GroupDisplay, "display.limit": GroupDisplay, "display.idle_hint_after": GroupDisplay, "waiting.fade": GroupDisplay,
	"waiting.popup_after": GroupAttention, "waiting.snooze": GroupAttention, "waiting.bell": GroupAttention,
	"recap.auto_after": GroupAttention, "updates.check": GroupAttention,
	"identity.as": GroupIdentity, "start.team": GroupIdentity,
	"dispatch.context_alert": GroupServices, "dispatch.prompt_grace": GroupServices, "dispatch.quota": GroupServices,
	"dispatch.quota_warn": GroupServices, "dispatch.quota_hold": GroupServices, "dispatch.quota_max_age": GroupServices,
	"dispatch.quota_long_hold": GroupServices, "dispatch.tokens": GroupServices, "dispatch.auto_clear": GroupServices,
	"dispatch.pop_retire": GroupServices, "dispatch.pop_retire_after": GroupServices, "dispatch.stall_after": GroupServices, "dispatch.clear_stall_after": GroupServices, "dispatch.quiet_after": GroupServices,
	"dispatch.turn_stale_after": GroupServices, "history.keep_days": GroupServices, "history.max_mb": GroupServices, "sentinel.reload": GroupServices,
	"delivery.forge": GroupServices, "delivery.base": GroupServices,
	"health.disk_warn_pct": GroupHealth, "health.disk_warn_gib": GroupHealth, "health.disk_fail_pct": GroupHealth,
	"health.disk_fail_gib": GroupHealth, "health.runaway.silent_after": GroupHealth, "health.runaway.context_growth": GroupHealth,
	"health.runaway.growth_span": GroupHealth, "health.runaway.screen_repeats": GroupHealth,
	"backup.dir": GroupBackup, "backup.keep": GroupBackup,
	"bus.report_max": GroupBusCaps, "bus.report_full_max": GroupBusCaps, "bus.verdict_max": GroupBusCaps,
}

// Group is k's group on the settings page; "" for a key not placed yet.
func (k *Key) Group() Group { return groups[k.Name] }
