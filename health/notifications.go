package health

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The notifications check (notifications spec, "Health"): per project, for
// you on warn, never a fail (it wakes nobody). Unknown while notify.on is
// false or notify.toml cannot be read; warn when the file is open to others,
// a listed target is missing or refused, a target's latest send failed, or a
// source of the project has failed to read for over 10 minutes (the chore
// then sends nothing: the stop is said, never silent); ok otherwise. The
// detail names targets and sources, never a URL.

// NotifySourceWarnAfter is how long a source may fail to read before the
// check warns.
const NotifySourceWarnAfter = 10 * time.Minute

// NotifyTarget is one listed target as the check reads it.
type NotifyTarget struct {
	Name    string
	Missing bool // listed, not in notify.toml
	Refused bool // in notify.toml, refused by its rules
	Sent    bool // a latest outcome exists
	OK      bool
	Err     string // the latest outcome's short error
	At      time.Time
}

// NotificationsInput is what the check reads for one project.
type NotificationsInput struct {
	On        bool
	FileErr   string // notify.toml cannot be read: unknown
	Refused   string // notify.toml refused whole: warn
	RecordErr string // notify-last.json cannot be read: unknown
	Targets   []NotifyTarget
	Sources   map[string]time.Time // this project's sources failing, since
	Now       time.Time
}

func EvaluateNotifications(in NotificationsInput) Outcome {
	switch {
	case !in.On:
		return Outcome{Status: Unknown, Detail: "notifications are off (notify.on)"}
	case in.FileErr != "":
		return Outcome{Status: Unknown, Detail: "notify.toml: " + in.FileErr}
	case in.Refused != "":
		return Outcome{Status: Warn, Detail: "notify.toml refused: " + in.Refused + "; nothing is sent"}
	case in.RecordErr != "":
		return Outcome{Status: Unknown, Detail: "notify-last.json: " + in.RecordErr}
	case len(in.Targets) == 0:
		return Outcome{Status: Warn, Detail: "no target in notify.targets: nothing is sent"}
	}
	var warns, fine []string
	for _, t := range in.Targets {
		switch {
		case t.Missing:
			warns = append(warns, t.Name+": not in notify.toml")
		case t.Refused:
			warns = append(warns, t.Name+": refused in notify.toml (agentbus notifications status says why)")
		case t.Sent && !t.OK:
			warns = append(warns, "last send failed "+t.At.Local().Format("15:04")+" ("+t.Err+")")
		case t.Sent:
			fine = append(fine, t.Name+" ok")
		default:
			fine = append(fine, t.Name+" not needed yet")
		}
	}
	var srcs []string
	for s, since := range in.Sources {
		if in.Now.Sub(since) >= NotifySourceWarnAfter {
			srcs = append(srcs, fmt.Sprintf("%s since %s", s, since.Local().Format("15:04")))
		}
	}
	sort.Strings(srcs)
	if len(srcs) > 0 {
		warns = append(warns, "cannot read "+strings.Join(srcs, ", ")+": nothing is sent")
	}
	if len(warns) > 0 {
		return Outcome{Status: Warn, Detail: strings.Join(warns, "; ")}
	}
	return Outcome{Status: OK, Detail: strings.Join(fine, ", ")}
}
