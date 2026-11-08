package health

import (
	"fmt"
	"strings"
	"time"
)

// model-catalogue (spec 2026-11-08-model-detection-design.md §3.5): what the
// machine's model table says about the models roles.toml configures, and
// what is new. The reader is cmd/agentbus/modelcatalogue.go.

// CatalogueFinding is one configured model the table judges gone, newer or
// newer?: a profile's model (or tier) or a fallback's.
type CatalogueFinding struct {
	Profile, Field, Model, Verdict, Candidate string
}

// Key is sentinel's de-duplication key (§3.6): the finding, never an event.
func (f CatalogueFinding) Key() string {
	return strings.Join([]string{f.Profile, f.Field, f.Model, f.Verdict, f.Candidate}, "|")
}

// OldSource is a provider some profile uses whose source is older than
// models.source_max_age; Hint says how to refresh it.
type OldSource struct {
	Provider string
	Age      time.Duration
	Hint     string
}

// FailedRead is a provider some profile uses whose last read failed, For
// this long since its last good one.
type FailedRead struct {
	Provider, Error string
	For             time.Duration
}

// CatalogueEvent is one of the table's events.
type CatalogueEvent struct {
	At                     time.Time
	Provider, Kind, ID, To string
}

type ModelCatalogueInput struct {
	Known       bool
	ReadErr     string
	Now         time.Time
	Findings    []CatalogueFinding
	OldSources  []OldSource
	FailedReads []FailedRead
	Events      []CatalogueEvent
}

// failedReadAfter: a read failing longer than this is a finding (§3.5).
const failedReadAfter = 48 * time.Hour

// EvaluateModelCatalogue: warn on a configured model gone, newer or newer?,
// a source too old or a read failing for more than 2 days; otherwise ok, its
// detail the news of the last 24 h (added, default, alias).
func EvaluateModelCatalogue(in ModelCatalogueInput) Outcome {
	if !in.Known {
		return Outcome{Status: Unknown, Detail: truncateDetail(in.ReadErr)}
	}
	var warn []string
	for _, f := range in.Findings {
		s := fmt.Sprintf("profile %s: %s %s", f.Profile, f.Model, f.Verdict)
		if f.Field != "model" {
			s = fmt.Sprintf("profile %s %s: %s %s", f.Profile, f.Field, f.Model, f.Verdict)
		}
		if f.Candidate != "" {
			s += " " + f.Candidate
		}
		warn = append(warn, s)
	}
	for _, o := range in.OldSources {
		s := fmt.Sprintf("%s's catalogue is %s old", o.Provider, days(o.Age))
		if o.Hint != "" {
			s += ": " + o.Hint
		}
		warn = append(warn, s)
	}
	for _, f := range in.FailedReads {
		if f.For <= failedReadAfter {
			continue
		}
		s := fmt.Sprintf("%s's read has failed for %s", f.Provider, days(f.For))
		if f.Error != "" {
			s += " (" + f.Error + ")"
		}
		warn = append(warn, s)
	}
	news := catalogueNews(in.Events, in.Now)
	if len(warn) > 0 {
		detail := strings.Join(warn, "; ")
		if news != "" {
			detail += "; " + news
		}
		return Outcome{Status: Warn, Detail: truncateDetail(detail)}
	}
	if news == "" {
		return Outcome{Status: OK, Detail: "every configured model is listed; no news in 24h"}
	}
	return Outcome{Status: OK, Detail: truncateDetail(news)}
}

// catalogueNews is the info line: the added, default and alias events of the
// last 24 h, newest first ("new: xai grok-4.7 (2h) · anthropic opus now …").
func catalogueNews(events []CatalogueEvent, now time.Time) string {
	var parts []string
	for _, e := range events {
		if now.Sub(e.At) > 24*time.Hour || now.Before(e.At.Add(-time.Minute)) {
			continue
		}
		switch e.Kind {
		case "added":
			parts = append(parts, fmt.Sprintf("%s %s (%s)", e.Provider, e.ID, compactAge(now.Sub(e.At))))
		case "alias":
			parts = append(parts, fmt.Sprintf("%s %s now %s", e.Provider, e.ID, e.To))
		case "default":
			parts = append(parts, fmt.Sprintf("%s default %s", e.Provider, e.ID))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "new: " + strings.Join(parts, " · ")
}

// days is an age in whole days from 2 days, else hours.
func days(d time.Duration) string {
	if d >= 48*time.Hour {
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	}
	return fmt.Sprintf("%dh", int(d/time.Hour))
}

// compactAge is "2h", "45m" or "1d".
func compactAge(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dm", max(int(d/time.Minute), 0))
}
