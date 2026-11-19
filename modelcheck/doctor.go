// Package modelcheck judges the models the team runs and configures against
// the machine's model table: the doctor's rows (roles doctor, the daily
// health check) and the models check (models check, the model catalogue).
// The commands that print them stay in cmd/agentbus.
package modelcheck

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/models"
	"github.com/netbja/agent-bus-monitor/relaunch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/usage"
)

// DoctorRow is one agent's verdict.
type DoctorRow struct {
	Agent      string `json:"agent"`
	Required   bool   `json:"required"`      // roles.toml's required (self-repair 4.2): its closed tab is recreated
	PID        int    `json:"pid,omitempty"` // the process judged: busmon shows the verdict only for it
	Profile    string `json:"profile"`
	Configured string `json:"configured"`
	Launched   string `json:"launched"`
	LaunchedBy string `json:"launched_source,omitempty"` // bind, or process args
	Used       string `json:"used"`
	UsedAge    string `json:"used_age,omitempty"`
	Verdict    string `json:"verdict"` // ok, drift, unknown model, not reported
	Why        string `json:"why,omitempty"`
	// Risk is an agy role's elevated options and the denies agy cannot
	// enforce, in its launch's words (agy-client §4.2, §4.4); never a verdict.
	Risk string `json:"risk,omitempty"`
	// Pops is, for a role that pops, the roles its session may spawn without
	// a prompt (allow) and with the person's yes (ask): the pop rules
	// it launches with (rolecfg.SpawnRules). A role in neither is not covered.
	Pops *DoctorPops `json:"pops,omitempty"`
	// UsedAt is when the transcript last showed Used (Unix ms; 0 = none):
	// recordAliases trusts only a fresh one.
	UsedAt int64 `json:"-"`
}

type DoctorPops struct {
	Allow []string `json:"allow"`
	Ask   []string `json:"ask"`
}

type DoctorDeps struct {
	Manifest   func() (*rolecfg.Manifest, error)
	ProcStart  func(pid int) (uint64, error)
	Cmdline    func(pid int) ([]string, error)
	Transcript func(session string) (usage.UsedModel, error)
	Now        func() time.Time
	// Table and Config (v0.15.2): the machine's model table and models.toml;
	// a nil Table judges nothing against a catalogue.
	Table  *models.Table
	Config models.Config
	// Env finds the project's template (rolecfg.ProjectsDir): a role's own
	// profile is its tab's --profile there (relaunch.OwnProfile); nil reads roles.toml
	// only.
	Env func(string) string
	// LaunchRisk is rolecfg's LaunchRisk (a test replaces it); nil is
	// Manifest.LaunchRisk.
	LaunchRisk func(m *rolecfg.Manifest, role, project, profile string) (string, error)
}

// RealDoctorDeps reads this host: roles.toml as the launcher finds it in
// getenv, the model table and models.toml (its settings read with
// modelsGetenv), /proc and the transcripts.
func RealDoctorDeps(getenv, modelsGetenv func(string) string) DoctorDeps {
	root := usage.ProjectsRoot()
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		root = filepath.Join(dir, "projects")
	}
	return DoctorDeps{
		Env: os.Getenv,
		Manifest: func() (*rolecfg.Manifest, error) {
			path, err := rolecfg.ManifestPath(getenv)
			if err != nil {
				return nil, err
			}
			return rolecfg.Load(path)
		},
		ProcStart: dispatch.ProcStart,
		Cmdline: func(pid int) ([]string, error) {
			b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
			if err != nil {
				return nil, err
			}
			return strings.Split(strings.TrimRight(string(b), "\x00"), "\x00"), nil
		},
		Transcript: func(session string) (usage.UsedModel, error) {
			path, err := usage.FindTranscript(root, session)
			if err != nil {
				return usage.UsedModel{}, err
			}
			return usage.ReadUsedModel(path)
		},
		Now:    time.Now,
		Table:  doctorTable(),
		Config: doctorConfig(modelsGetenv),
	}
}

func doctorTable() *models.Table {
	t, err := models.Load(models.CachePath())
	if err != nil {
		return nil
	}
	return t
}

func doctorConfig(modelsGetenv func(string) string) models.Config {
	c, _ := models.LoadConfigWithSettings(models.ConfigPath(), modelsGetenv)
	return c
}

// ClaudeAlias: Claude Code's family aliases follow the latest model; an
// alias is never judged against the id it resolves to.
var ClaudeAlias = regexp.MustCompile(`^(opus|sonnet|haiku|fable|default|best)$`)

// datedID is a model id with its release date appended
// ("claude-haiku-4-5-20251001"): the same model as the id without it.
var datedID = regexp.MustCompile(`-\d{8}$`)

// sameModel compares two model names, ignoring Claude Code's context suffix
// ("claude-fable-5-1[1m]") and a release date suffix.
func sameModel(a, b string) bool {
	trim := func(s string) string {
		if i := strings.Index(s, "["); i > 0 {
			s = s[:i]
		}
		return datedID.ReplaceAllString(s, "")
	}
	return trim(a) == trim(b)
}

// argModel is the --model / -m value of a client's command line.
func argModel(argv []string) string {
	for i, a := range argv {
		switch {
		case (a == "--model" || a == "-m") && i+1 < len(argv):
			return argv[i+1]
		case strings.HasPrefix(a, "--model="):
			return strings.TrimPrefix(a, "--model=")
		}
	}
	return ""
}

func DoctorRows(ctx context.Context, b *bus.Bus, d DoctorDeps) ([]DoctorRow, error) {
	m, err := d.Manifest()
	if err != nil {
		return nil, err
	}
	snaps, err := b.Agents(ctx)
	if err != nil {
		return nil, err
	}
	fbs, err := b.Fallbacks(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(snaps))
	for n := range snaps {
		if _, ok := m.Roles[n]; ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var rows []DoctorRow
	for _, n := range names {
		s := snaps[n]
		r := DoctorRow{Agent: n, PID: s.PID, Required: m.RoleRequired(n)}
		own, err := m.Resolve(n)
		if d.Env != nil {
			own, err = relaunch.OwnProfile(m, d.Env, b.Project(), n)
		}
		if err != nil {
			r.Verdict, r.Why = "not reported", err.Error()
			rows = append(rows, r)
			continue
		}
		cur := own
		if f, ok := fbs[n]; ok {
			if p, err := m.ResolveWith(n, f.Profile); err == nil {
				cur = p
			}
		}
		r.Profile, r.Configured = cur.Profile, cur.Model
		if risk, err := d.launchRisk(m, n, b.Project(), cur.Profile); err != nil {
			r.Risk = "not judged: " + err.Error() // never silent (foureyes): the launch would fail the same way
		} else {
			r.Risk = risk
		}
		if allow, ask, err := m.SpawnRules(n); err == nil && (allow != nil || ask != nil) {
			r.Pops = &DoctorPops{Allow: allow, Ask: ask}
		}
		if cur.Tier != "" { // the model the tier stands for now (§10.3)
			r.Configured = "tier " + cur.Tier
			if d.Table != nil {
				if row, ok := d.Table.Resolve(cur.Provider, cur.Tier, d.Now()); ok {
					r.Configured = row.ID
				}
			}
		}
		// Launched: the bound identity's model, else the live process's own
		// command line (agents launched before v0.15.1 recorded none).
		r.Launched, r.LaunchedBy = s.Model, "bind"
		if r.Launched == "" && s.PID > 0 {
			if start, err := d.ProcStart(s.PID); err == nil && start == s.PIDStart {
				if argv, err := d.Cmdline(s.PID); err == nil {
					r.Launched, r.LaunchedBy = argModel(argv), "process"
				}
			}
		}
		if r.Launched == "" {
			r.LaunchedBy = ""
		}
		var used usage.UsedModel
		if s.Session != "" && (s.Client == "" || s.Client == "claude-code") {
			if u, err := d.Transcript(s.Session); err == nil {
				used = u
			}
		}
		r.Used, r.UsedAt = used.Model, used.TS
		stale := false
		if used.TS > 0 {
			age := d.Now().Sub(time.UnixMilli(used.TS))
			r.UsedAge = age.Round(time.Minute).String()
			stale = age > 24*time.Hour
		}
		// A model error counts for a day: an old refusal says nothing now (§11.7).
		if used.ModelError != "" && (used.ErrorTS == 0 || d.Now().Sub(time.UnixMilli(used.ErrorTS)) > 24*time.Hour) {
			used.ModelError = ""
		}
		switch {
		case used.ModelError != "":
			r.Verdict, r.Why = "unknown model", used.ModelError
		case r.Launched == "":
			r.Verdict, r.Why = "not reported", "no launched model recorded and none on the process command line"
		case strings.HasPrefix(r.Configured, "tier "):
			r.Verdict, r.Why = "not reported", "the model table cannot resolve this "+r.Configured+" (catalogue unavailable): agentbus models --refresh"
		case ClaudeAlias.MatchString(r.Configured) && strings.Contains(r.Launched, "-"+r.Configured+"-"):
			r.Verdict = "ok" // an alias launched as the id of its family: never drift (§7.1)
		case !sameModel(r.Launched, r.Configured):
			r.Verdict, r.Why = "drift", fmt.Sprintf("launched %s, roles.toml now %s: relaunch with agentbus swap %s --back", r.Launched, r.Configured, n)
		case r.Used != "" && !stale && !ClaudeAlias.MatchString(r.Launched) && !sameModel(r.Used, r.Launched):
			r.Verdict, r.Why = "drift", fmt.Sprintf("launched %s but its transcript shows %s", r.Launched, r.Used)
		default:
			r.Verdict = "ok"
			if v, why := catalogueVerdict(d, cur.Provider, r.Launched); v != "" {
				r.Verdict, r.Why = v, why
			}
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// catalogueVerdict (v0.15.2 §10.4, §12): what the model table says about a
// launched model that matches its configuration: gone, newer or newer?
// (judgeModel), else nothing. An old or failed catalogue judges nothing.
func catalogueVerdict(d DoctorDeps, provider, launched string) (string, string) {
	if launched == "" || provider == "" {
		return "", ""
	}
	j := judgeModel(d.Table, d.Config, d.Now(), provider, launched)
	switch j.Verdict {
	case "gone": // what runs: its next launch fails
		return j.Verdict, j.Why + ": relaunch on a listed model"
	case "newer", "newer?":
		return j.Verdict, j.Why
	}
	return "", ""
}

// DoctorFails: the verdicts that make roles doctor exit 3 (health's one list).
func DoctorFails(verdict string) bool { return health.ModelVerdictFails(verdict) }

// sourceTooOld: p's source is older than models.source_max_age (§3.2); an
// unknown source age (0) is never too old. Every verdict and summary that
// trusts p's rows asks it first.
func sourceTooOld(p *models.Provider, cfg models.Config, now time.Time, provider string) (string, bool) {
	if p == nil || p.SourceAt <= 0 || cfg.SourceMaxAge <= 0 {
		return "", false
	}
	age := now.Sub(time.UnixMilli(p.SourceAt))
	if age <= cfg.SourceMaxAge {
		return "", false
	}
	return fmt.Sprintf("%s's catalogue is %s old (models.source_max_age %s)", provider, AgeWords(age), AgeWords(cfg.SourceMaxAge)), true
}

// AgeWords is a catalogue age: whole days from 2 days, else hours.
func AgeWords(d time.Duration) string {
	if d >= 48*time.Hour {
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	}
	return fmt.Sprintf("%dh", int(d/time.Hour))
}

// judgement is the model table's verdict on one model id, shared by roles
// doctor (what runs) and models check (what is configured), so the two never
// disagree on a model (model-detection §3.4).
type judgement struct {
	Verdict   string // ok, gone, newer, newer?, not listed (partial), unknown
	Why       string
	Candidate string // newer, newer?: the listed successor
}

// judgeModel: "gone" when the provider's fresh, complete catalogue no longer
// lists id (aliases resolved first: an alias is a row of its own); "newer"
// only on stated evidence (the source's upgrade or generation words, or a
// family order in models.toml), naming a model the same catalogue lists;
// "newer?" when the catalogue lists a higher version of the same stem and
// suffix and no family order speaks for id (§3.3); "unknown" when there is
// no fresh read, or its source is older than cfg.SourceMaxAge.
func judgeModel(tbl *models.Table, cfg models.Config, now time.Time, provider, id string) judgement {
	if tbl == nil {
		return judgement{Verdict: "unknown", Why: "no model table: agentbus models --refresh"}
	}
	p, fresh := tbl.Fresh(provider, now)
	if !fresh {
		why := provider + ": no read in the last 7 days"
		if p != nil && p.Error != "" {
			why += " (last read failed: " + p.Error + ")"
		}
		return judgement{Verdict: "unknown", Why: why}
	}
	if why, old := sourceTooOld(p, cfg, now, provider); old {
		return judgement{Verdict: "unknown", Why: why}
	}
	row, found := p.Find(id)
	if !found && p.Partial {
		return judgement{Verdict: "not listed (partial)", Why: provider + "'s source does not list every id it accepts"}
	}
	if !found {
		return judgement{Verdict: "gone", Why: fmt.Sprintf("%s is no longer in %s's model list (agentbus models)", id, provider)}
	}
	if row.Alias {
		return judgement{Verdict: "ok"}
	}
	// Evidence names a model, and that model must be in the same fresh
	// catalogue: never point a relaunch at something the provider lacks.
	for _, cand := range []string{cfg.NewerByOrder(id), row.Newer} {
		if cand == "" {
			continue
		}
		if _, ok := p.Find(cand); ok {
			return judgement{Verdict: "newer", Candidate: cand, Why: fmt.Sprintf("%s lists %s as newer than %s", provider, cand, id)}
		}
	}
	if !cfg.InFamily(id) {
		if cand, ok := p.ListedNewer(id); ok {
			return judgement{Verdict: "newer?", Candidate: cand, Why: fmt.Sprintf("%s lists %s: listed, not stated as a successor", provider, cand)}
		}
	}
	return judgement{Verdict: "ok"}
}

// launchRisk is d.LaunchRisk, else the manifest's own.
func (d DoctorDeps) launchRisk(m *rolecfg.Manifest, role, project, profile string) (string, error) {
	if d.LaunchRisk != nil {
		return d.LaunchRisk(m, role, project, profile)
	}
	return m.LaunchRisk(role, project, profile)
}
