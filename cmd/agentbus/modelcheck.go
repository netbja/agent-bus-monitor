package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/models"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// checkRow is one profile of roles.toml judged against the model table
// (model-detection §3.4): what is configured, used or not.
type checkRow struct {
	Profile   string   `json:"profile"`
	Provider  string   `json:"provider"`
	Model     string   `json:"model,omitempty"`
	Tier      string   `json:"tier,omitempty"`
	Resolved  string   `json:"resolved,omitempty"` // the id a tier stands for now
	UsedBy    []string `json:"used_by"`            // "coder", "fallback of coder"; empty = unused
	Verdict   string   `json:"verdict"`            // ok, gone, newer, newer?, not listed (partial), untiered, unknown
	Why       string   `json:"why,omitempty"`
	Candidate string   `json:"candidate,omitempty"`
}

// untieredNote: a provider a tier profile uses whose rows lack a tier (§3.7).
type untieredNote struct {
	Provider string `json:"provider"`
	Count    int    `json:"count"`
}

type checkReport struct {
	Profiles []checkRow     `json:"profiles"`
	Untiered []untieredNote `json:"untiered,omitempty"`
}

// checkFails: the verdicts that make models check exit 3. newer? is a hint,
// and the unjudged ones (unknown, not listed (partial)) are no finding.
func checkFails(v string) bool { return v == "gone" || v == "newer" || v == "untiered" }

func modelsCheckReport(tbl *models.Table, cfg models.Config, m *rolecfg.Manifest, now time.Time) checkReport {
	usedBy := map[string][]string{}
	for _, role := range m.RoleNames() {
		if own, err := m.Resolve(role); err == nil {
			usedBy[own.Profile] = append(usedBy[own.Profile], role)
		}
		for _, fb := range m.Fallbacks(role) {
			usedBy[fb] = append(usedBy[fb], "fallback of "+role)
		}
	}
	var rows []checkRow
	add := func(r checkRow) {
		if r.Tier != "" {
			r = judgeTier(tbl, cfg, r, now)
		} else {
			j := judgeModel(tbl, cfg, now, r.Provider, r.Model)
			r.Verdict, r.Why, r.Candidate = j.Verdict, j.Why, j.Candidate
		}
		if r.UsedBy == nil {
			r.UsedBy = []string{}
		}
		rows = append(rows, r)
	}
	if names := m.ProfileNames(); len(names) > 0 {
		for _, name := range names {
			provider, model, tier, _ := m.ProfileModel(name)
			add(checkRow{Profile: name, Provider: provider, Model: model, Tier: tier, UsedBy: usedBy[name]})
		}
	} else { // schema 1: each role carries its model
		for _, role := range m.RoleNames() {
			if p, err := m.Resolve(role); err == nil {
				add(checkRow{Profile: p.Profile, Provider: p.Provider, Model: p.Model, UsedBy: []string{role}})
			}
		}
	}
	report := checkReport{Profiles: rows}
	providers := map[string]bool{} // only where a tier profile must resolve (foureyes)
	for _, r := range rows {
		if r.Tier != "" {
			providers[r.Provider] = true
		}
	}
	names := make([]string, 0, len(providers))
	for p := range providers {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, name := range names {
		p, fresh := tbl.Fresh(name, now)
		if _, old := sourceTooOld(p, cfg, now, name); !fresh || old {
			continue
		}
		n := 0
		for _, r := range p.Rows {
			if r.Tier == "" {
				n++
			}
		}
		if n > 0 {
			report.Untiered = append(report.Untiered, untieredNote{Provider: name, Count: n})
		}
	}
	return report
}

// judgeTier: a tier profile is ok when its provider's fresh table (its source
// within models.source_max_age) resolves
// the tier, untiered when it has no row of that tier (§3.7).
func judgeTier(tbl *models.Table, cfg models.Config, r checkRow, now time.Time) checkRow {
	if tbl != nil {
		if p, fresh := tbl.Fresh(r.Provider, now); fresh {
			if why, old := sourceTooOld(p, cfg, now, r.Provider); old { // reviewer-codex
				r.Verdict, r.Why = "unknown", why
				return r
			}
			if row, ok := tbl.Resolve(r.Provider, r.Tier, now); ok {
				r.Verdict, r.Resolved = "ok", row.ID
				return r
			}
			r.Verdict, r.Why = "untiered", fmt.Sprintf("%s has no %s row: the profile cannot resolve (models.toml [[tier]] rules)", r.Provider, r.Tier)
			return r
		}
	}
	j := judgeModel(tbl, models.Config{}, now, r.Provider, "")
	r.Verdict, r.Why = "unknown", j.Why
	return r
}

func runModelsCheck(tbl *models.Table, cfg models.Config, manifest func() (*rolecfg.Manifest, error), now time.Time, asJSON bool, out, errw io.Writer) int {
	m, err := manifest()
	if err != nil {
		fmt.Fprintf(errw, "models check: %v\n", err)
		return 1
	}
	report := modelsCheckReport(tbl, cfg, m, now)
	code := 0
	for _, r := range report.Profiles {
		if checkFails(r.Verdict) {
			code = 3
		}
	}
	if asJSON {
		if writeJSON(out, errw, report) != 0 {
			return 1
		}
		return code
	}
	fmt.Fprintf(out, "%-10s %-10s %-34s %-22s %s\n", "PROFILE", "PROVIDER", "MODEL", "VERDICT", "USED BY")
	for _, r := range report.Profiles {
		model := r.Model
		if r.Tier != "" {
			model = "tier " + r.Tier
			if r.Resolved != "" {
				model += " = " + r.Resolved
			}
		}
		used := "unused"
		if len(r.UsedBy) > 0 {
			used = strings.Join(r.UsedBy, ", ")
		}
		fmt.Fprintf(out, "%-10s %-10s %-34s %-22s %s\n", r.Profile, r.Provider, model, r.Verdict, used)
		if r.Why != "" && r.Verdict != "ok" {
			fmt.Fprintf(out, "%10s %s\n", "", r.Why)
		}
	}
	for _, u := range report.Untiered {
		n := fmt.Sprintf("%d models have", u.Count)
		if u.Count == 1 {
			n = "1 model has"
		}
		fmt.Fprintf(out, "%s: %s no tier; a tier profile cannot resolve there (models.toml [[tier]] rules)\n", u.Provider, n)
	}
	return code
}
