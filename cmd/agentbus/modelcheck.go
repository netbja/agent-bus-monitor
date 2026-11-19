package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/modelcheck"
	"github.com/netbja/agent-bus-monitor/models"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// checkFails: the verdicts that make models check exit 3. newer? is a hint,
// and the unjudged ones (unknown, not listed (partial)) are no finding.
func checkFails(v string) bool { return v == "gone" || v == "newer" || v == "untiered" }

func runModelsCheck(tbl *models.Table, cfg models.Config, manifest func() (*rolecfg.Manifest, error), now time.Time, asJSON bool, out, errw io.Writer) int {
	m, err := manifest()
	if err != nil {
		fmt.Fprintf(errw, "models check: %v\n", err)
		return 1
	}
	report := modelcheck.ModelsCheckReport(tbl, cfg, m, now)
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
