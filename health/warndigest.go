package health

import (
	"fmt"
	"strings"
	"time"
)

// The daily review's digest of long warns (sentinel-warn-digest: master's
// directive, the person asked for good practice). Sentinel sends it to
// master as one message and fixes nothing new; a warn is closed by a fix or
// by the person's explicit accept.
const (
	// WarnDigestAfter is how long a check must have been in warn to be
	// listed: a day, the daily review's own period.
	WarnDigestAfter = 24 * time.Hour
	// AcceptFor is how long a person's accept keeps a warn out of the
	// digest: a week on, it is reported again until fixed or accepted again.
	AcceptFor = 7 * 24 * time.Hour
)

// Digest is a review's long warns: one line each, and how many the person
// accepted this week (counted, not repeated).
type Digest struct {
	Lines    []string
	Accepted int
}

func dayHours(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%dd%dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
}

// WarnDigest lists, in the catalogue's order, each check whose effective
// status is warn (a stale record is unknown, not warn) since more than
// WarnDigestAfter, with its age and detail. An accept counts only for the
// current warn (written at or after its since): within AcceptFor it holds
// the check back, past it the line carries the accept and asks again.
func WarnDigest(records map[string]Record, accepts map[string]Note, nowMS int64) Digest {
	var d Digest
	for _, def := range Definitions() {
		rec, ok := records[def.ID]
		if !ok || EffectiveStatus(rec, nowMS, def.Interval) != Warn || rec.Since <= 0 {
			continue
		}
		age := time.Duration(nowMS-rec.Since) * time.Millisecond
		if age < WarnDigestAfter {
			continue
		}
		line := fmt.Sprintf("%s warn %s: %s", def.ID, dayHours(age), oneLine(rec.Detail))
		if a, accepted := accepts[def.ID]; accepted && a.At >= rec.Since {
			since := time.Duration(nowMS-a.At) * time.Millisecond
			if since < AcceptFor {
				d.Accepted++
				continue
			}
			line += fmt.Sprintf(" (accepted %s ago by %s: %s; still warn: fix it or accept it again)", dayHours(since), a.Author, oneLine(a.Text))
		}
		d.Lines = append(d.Lines, line)
	}
	return d
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// Message is the digest as sentinel sends it to master, "" when nothing is
// listed (an accepted warn alone needs no message).
func (d Digest) Message() string {
	if len(d.Lines) == 0 {
		return ""
	}
	msg := fmt.Sprintf("Health digest: %d checks in warn over 24 h (fix each, or the person accepts it: agentbus health accept <check> --as <person> <reason>)", len(d.Lines))
	if d.Accepted > 0 {
		msg += fmt.Sprintf("; %d accepted by the person this week (not repeated)", d.Accepted)
	}
	return msg + "\n- " + strings.Join(d.Lines, "\n- ")
}
