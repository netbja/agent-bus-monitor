package update

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// What master hears of an automatic pass (quiet-update-auto spec §4.6, step
// 4c): one report from updater to each touched project's escalation target,
// each kind sent once per tag or snapshot (auto.json's told). A broker that
// cannot be reached loses the notice, not the update: the history line and
// auto.json still record it.

// The notice kinds, auto.json's told keys.
const (
	NoticeInstalled      = "installed"       // no wake, to every project restarted or left on the old build; once per snapshot
	NoticeFailed         = "failed"          // wake: rolled back, the tag not retried; once per tag
	NoticeRollbackFailed = "rollback_failed" // wake: the manual commands; once per snapshot
	NoticeInterrupted    = "interrupted"     // wake: agentbus update --rollback <id>; once per snapshot
	NoticeMigrate        = "migrate"         // no wake: a release that needs a migration; once per tag
	NoticeWaiting        = "waiting"         // wake: a release waiting longer than updates.max_wait; once per tag
)

// Notice is one of master's notices: its kind, the tag or snapshot it is
// once for, whether it wakes, its text, and the projects it goes to.
type Notice struct {
	Kind, Key string
	Wake      bool
	Text      string
	Projects  []string
}

// tell sends n once per key (st.Told), to its projects; with none, nobody
// is told and nothing is marked, so a later pass with a project tells it. A
// failed send is said, and marked all the same: the notice is lost, never
// repeated each tick.
func (d Deps) tell(ctx context.Context, st *AutoState, n Notice) {
	if d.Notify == nil || len(n.Projects) == 0 || st.Told[n.Kind] == n.Key {
		return
	}
	if err := d.Notify(ctx, n); err != nil {
		fmt.Fprintln(d.Err, "agentbus update: master's notice ("+n.Kind+") could not be sent: "+err.Error()+" (the history line and auto.json keep it)")
	}
	if st.Told == nil {
		st.Told = map[string]string{}
	}
	st.Told[n.Kind] = n.Key
}

// hostProjects are the projects judged on this host (§4.3 step 4: a running
// dispatch or missions, or an active holder), sorted: the projects a
// failure, an interrupted update, a migration or a long wait is told to.
// What cannot be read is left out: a notice goes to what can be named.
func (d Deps) hostProjects(ctx context.Context) []string {
	seen := map[string]bool{}
	if d.Running != nil {
		if rs, err := d.Running(ctx); err == nil {
			for _, s := range rs {
				seen[s.Project] = true
			}
		}
	}
	if d.Holders != nil {
		if hs, err := d.Holders(ctx); err == nil {
			for _, p := range hs {
				seen[p] = true
			}
		}
	}
	return sortedKeys(seen)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// installedNotice: from, to, the services restarted, those left on the old
// build, the skills changed with their skill-users command, and the undo. It
// goes to every project restarted and every project left on the old build
// (master's 4d directive: the binaries are per host, so a project left
// behind runs the new agentbus at its next call and must hear of it).
func (d Deps) installedNotice(tag, id string, out installOut, changed []string) Notice {
	projects := map[string]bool{}
	names := []string{}
	for _, s := range out.restarted {
		projects[s.Project] = true
		names = append(names, s.Project+" "+s.Kind)
	}
	for _, p := range out.leftOf {
		projects[p] = true
	}
	restarted := strings.Join(names, ", ")
	if restarted == "" {
		restarted = "none"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "agentbus %s -> %s installed automatically; restarted: %s.", d.Version, tag, restarted)
	for _, l := range out.left {
		b.WriteString("\nNot restarted: " + l)
	}
	if len(changed) > 0 {
		c := strings.Join(changed, " ")
		fmt.Fprintf(&b, "\nSkills changed: %s (agents to tell: agentbus roles skill-users %s).", c, c)
	}
	fmt.Fprintf(&b, "\nUndo: agentbus update --rollback %s", id)
	return Notice{Kind: NoticeInstalled, Key: id, Text: b.String(), Projects: sortedKeys(projects)}
}

// failedNotice: rolled back, why, and that the tag is not retried.
func (d Deps) failedNotice(ctx context.Context, tag string, out installOut) Notice {
	return Notice{Kind: NoticeFailed, Key: tag, Wake: true, Projects: d.hostProjects(ctx),
		Text: fmt.Sprintf("agentbus update --auto: %s -> %s failed and was rolled back to %s: %s. %s is not installed automatically again; agentbus update installs it by hand.",
			d.Version, tag, d.Version, out.why, tag)}
}

// rollbackFailedNotice: the rollback failed too; the manual commands.
func (d Deps) rollbackFailedNotice(ctx context.Context, tag, id string, out installOut) Notice {
	return Notice{Kind: NoticeRollbackFailed, Key: id, Wake: true, Projects: d.hostProjects(ctx),
		Text: fmt.Sprintf("agentbus update --auto: %s -> %s failed (%s) and its rollback failed too. The person rolls back by hand:\n%s",
			d.Version, tag, out.why, strings.Join(out.manual, "\n"))}
}

// interruptedNotice: an update that did not finish; nothing installs
// automatically until it is rolled back.
func (d Deps) interruptedNotice(ctx context.Context, id string) Notice {
	return Notice{Kind: NoticeInterrupted, Key: id, Wake: true, Projects: d.hostProjects(ctx),
		Text: fmt.Sprintf("agentbus update --auto: the update that took snapshot %s did not finish; nothing installs automatically until the person runs agentbus update --rollback %s", id, id)}
}

// migrateNotice: a release that needs a broker migration, the person's.
func (d Deps) migrateNotice(ctx context.Context, tag, why string) Notice {
	return Notice{Kind: NoticeMigrate, Key: tag, Projects: d.hostProjects(ctx),
		Text: "agentbus update --auto: " + why + "; it is not installed automatically."}
}

// waitingNotice is the max_wait notice, once per tag, when the release
// found has waited longer than updates.max_wait: since when, and the reason
// of the last pass (none since it was found: the window was never reached;
// else its result and why, the project and agent in work).
func (d Deps) waitingNotice(ctx context.Context, st AutoState, now time.Time) (Notice, bool) {
	if d.MaxWait == nil || st.Waiting == nil {
		return Notice{}, false
	}
	max := d.MaxWait()
	if max <= 0 || now.Sub(st.Waiting.Since) <= max {
		return Notice{}, false
	}
	reason := "no automatic pass ran since it was found: the window (updates.window) was never reached"
	if p := st.LastPass; p != nil && !p.At.Before(st.Waiting.Since) {
		reason = "the last pass, " + p.At.Format(time.RFC3339) + ", was " + p.Result
		if p.Why != "" {
			reason += ": " + p.Why
		}
	}
	tag := st.Waiting.Tag
	return Notice{Kind: NoticeWaiting, Key: tag, Wake: true, Projects: d.hostProjects(ctx),
		Text: fmt.Sprintf("agentbus update --auto: %s has waited since %s, longer than updates.max_wait (%s); %s. agentbus update installs it by hand.",
			tag, st.Waiting.Since.Format(time.RFC3339), max, reason)}, true
}
