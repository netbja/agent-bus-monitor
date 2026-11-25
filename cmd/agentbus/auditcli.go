package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/bus"
)

// audit step 2 (spec §2): an act the CLI runs writes its audit line once,
// after the act, from fixed parts only; best effort (bus.AuditAfter): a line
// that cannot be written never fails the act.

// auditFromExit is e's line from the act's exit code: 0 ok with detail; 2
// (a usage error: no act) none; any other failed, the code named.
func auditFromExit(e bus.AuditEntry, detail string, code int) (bus.AuditEntry, bool) {
	switch code {
	case 0:
		e.Outcome, e.Detail = bus.AuditOK, detail
	case 2:
		return e, false
	default:
		e.Outcome, e.Detail = bus.AuditFailed, detail+", exit "+strconv.Itoa(code)
	}
	return e, true
}

// auditExit writes the line of an act that ended with code, and returns code.
func auditExit(ctx context.Context, b *bus.Bus, e bus.AuditEntry, detail string, code int) int {
	if line, ok := auditFromExit(e, detail, code); ok {
		b.AuditAfter(ctx, line)
	}
	return code
}

// forceAct is the audit line of a verb run with --force (master's list:
// agents forget, swap, mission load; shutdown writes its own), its target the
// first name after the verb; ok false for any other.
func forceAct(cmd string, rest []string) (bus.AuditEntry, bool) {
	if !slices.Contains(rest, "--force") {
		return bus.AuditEntry{}, false
	}
	verb, args := cmd, rest
	switch {
	case cmd == "agents" && len(rest) > 0 && rest[0] == "forget":
		verb, args = "agents forget", rest[1:]
	case cmd == "mission" && len(rest) > 0 && rest[0] == "load":
		verb, args = "mission load", rest[1:]
	case cmd == "swap":
	default:
		return bus.AuditEntry{}, false
	}
	target := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			target = a
			break
		}
	}
	if target == "" {
		return bus.AuditEntry{}, false
	}
	return bus.AuditEntry{Action: "force", Target: target, Detail: verb + " --force"}, true
}

// auditForce writes the --force line of cmd rest (forceAct) from its exit,
// and returns code; any other command passes through.
func auditForce(ctx context.Context, b *bus.Bus, self, cmd string, rest []string, code int) int {
	e, ok := forceAct(cmd, rest)
	if !ok {
		return code
	}
	e.Actor = self
	return auditExit(ctx, b, e, e.Detail, code)
}

// personActor is the actor of a host-level act the person runs from their
// shell (update, setup's reinstall, restore --force; audit step 2b): the
// name they recorded (identity.as), else "person".
func personActor(getenv func(string) string) string {
	if name, _ := personName(getenv); bus.ValidName(name) {
		return name
	}
	return "person"
}

// auditRestoreForced writes the force line of each project --force acted on
// (backup.Report.Forced), on that project's bus, after the restore: a
// replaced trail keeps it.
func auditRestoreForced(ctx context.Context, c *redis.Client, actor string, forced []backup.Forced) {
	for _, f := range forced {
		b, err := bus.Open(c, f.Project)
		if err != nil {
			bus.AuditWarn(err)
			continue
		}
		detail := fmt.Sprintf("restore --force, %d key(s) replaced", f.Keys)
		switch f.Outcome {
		case bus.AuditRefused:
			detail = fmt.Sprintf("restore --force, %d key(s) present, not confirmed", f.Keys)
		case bus.AuditFailed:
			detail = fmt.Sprintf("restore --force, %d key(s) present, the replacement failed", f.Keys)
		}
		b.AuditAfter(ctx, bus.AuditEntry{Action: "force", Actor: actor, Target: f.Project, Outcome: f.Outcome, Detail: detail})
	}
}
