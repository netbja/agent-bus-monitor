package modelcheck

import (
	"fmt"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/models"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// CheckRow is one profile of roles.toml judged against the model table
// (model-detection §3.4): what is configured, used or not.
type CheckRow struct {
	Profile   string   `json:"profile"`
	Provider  string   `json:"provider"`
	Model     string   `json:"model,omitempty"`
	Tier      string   `json:"tier,omitempty"`
	Resolved  string   `json:"resolved,omitempty"` // the id a tier stands for now
	UsedBy    []string `json:"used_by"`            // "coder", "fallback of coder", "alt of architect"; empty = unused
	Verdict   string   `json:"verdict"`            // ok, gone, newer, newer?, not listed (partial), untiered, unknown
	Why       string   `json:"why,omitempty"`
	Candidate string   `json:"candidate,omitempty"`
}

// UntieredNote: a provider a tier profile uses whose rows lack a tier (§3.7).
type UntieredNote struct {
	Provider string `json:"provider"`
	Count    int    `json:"count"`
}

type CheckReport struct {
	Profiles []CheckRow     `json:"profiles"`
	Untiered []UntieredNote `json:"untiered,omitempty"`
}

func ModelsCheckReport(tbl *models.Table, cfg models.Config, m *rolecfg.Manifest, now time.Time) CheckReport {
	usedBy := map[string][]string{}
	for _, role := range m.RoleNames() {
		if own, err := m.Resolve(role); err == nil {
			usedBy[own.Profile] = append(usedBy[own.Profile], role)
		}
		for _, fb := range m.Fallbacks(role) {
			usedBy[fb] = append(usedBy[fb], "fallback of "+role)
		}
		for _, alt := range m.Alternates(role) {
			usedBy[alt] = append(usedBy[alt], "alt of "+role)
		}
	}
	var rows []CheckRow
	add := func(r CheckRow) {
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
			add(CheckRow{Profile: name, Provider: provider, Model: model, Tier: tier, UsedBy: usedBy[name]})
		}
	} else { // schema 1: each role carries its model
		for _, role := range m.RoleNames() {
			if p, err := m.Resolve(role); err == nil {
				add(CheckRow{Profile: p.Profile, Provider: p.Provider, Model: p.Model, UsedBy: []string{role}})
			}
		}
	}
	report := CheckReport{Profiles: rows}
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
			report.Untiered = append(report.Untiered, UntieredNote{Provider: name, Count: n})
		}
	}
	return report
}

// judgeTier: a tier profile is ok when its provider's fresh table (its source
// within models.source_max_age) resolves
// the tier, untiered when it has no row of that tier (§3.7).
func judgeTier(tbl *models.Table, cfg models.Config, r CheckRow, now time.Time) CheckRow {
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
