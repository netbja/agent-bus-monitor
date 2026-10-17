package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/usage"
)

// `agentbus roles doctor [--json]` (v0.15.1 §7, §11.6–7): for every registered
// agent, the model roles.toml configures for the profile it runs, the model
// it was launched with, and the model its transcript shows it uses. It reads
// Redis, the manifest, /proc and transcripts; it never calls a model.

// doctorRow is one agent's verdict.
type doctorRow struct {
	Agent      string `json:"agent"`
	PID        int    `json:"pid,omitempty"` // the process judged: busmon shows the verdict only for it
	Profile    string `json:"profile"`
	Configured string `json:"configured"`
	Launched   string `json:"launched"`
	LaunchedBy string `json:"launched_source,omitempty"` // bind, or process args
	Used       string `json:"used"`
	UsedAge    string `json:"used_age,omitempty"`
	Verdict    string `json:"verdict"` // ok, drift, unknown model, not reported
	Why        string `json:"why,omitempty"`
}

type doctorDeps struct {
	Manifest   func() (*rolecfg.Manifest, error)
	ProcStart  func(pid int) (uint64, error)
	Cmdline    func(pid int) ([]string, error)
	Transcript func(session string) (usage.UsedModel, error)
	Now        func() time.Time
}

func realDoctorDeps(env []string) doctorDeps {
	root := usage.ProjectsRoot()
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		root = filepath.Join(dir, "projects")
	}
	return doctorDeps{
		Manifest: func() (*rolecfg.Manifest, error) {
			path, err := manifestPath(env)
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
		Now: time.Now,
	}
}

// claudeAlias: Claude Code's family aliases follow the latest model; an
// alias is never judged against the id it resolves to.
var claudeAlias = regexp.MustCompile(`^(opus|sonnet|haiku|fable|default|best)$`)

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

func doctorRows(ctx context.Context, b *bus.Bus, d doctorDeps) ([]doctorRow, error) {
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
	var rows []doctorRow
	for _, n := range names {
		s := snaps[n]
		r := doctorRow{Agent: n, PID: s.PID}
		own, err := m.Resolve(n)
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
		r.Used = used.Model
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
		case claudeAlias.MatchString(r.Configured) && strings.Contains(r.Launched, "-"+r.Configured+"-"):
			r.Verdict = "ok" // an alias launched as the id of its family: never drift (§7.1)
		case !sameModel(r.Launched, r.Configured):
			r.Verdict, r.Why = "drift", fmt.Sprintf("launched %s, roles.toml now %s: relaunch with agentbus swap %s --back", r.Launched, r.Configured, n)
		case r.Used != "" && !stale && !claudeAlias.MatchString(r.Launched) && !sameModel(r.Used, r.Launched):
			r.Verdict, r.Why = "drift", fmt.Sprintf("launched %s but its transcript shows %s", r.Launched, r.Used)
		default:
			r.Verdict = "ok"
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func runDoctor(ctx context.Context, b *bus.Bus, d doctorDeps, args []string, out, errw io.Writer) int {
	args, asJSON := extractBool(args, "--json")
	args, publish := extractBool(args, "--publish")
	if len(args) != 0 {
		fmt.Fprintln(errw, "usage: agentbus --project <p> roles doctor [--json] [--publish]")
		return 2
	}
	rows, err := doctorRows(ctx, b, d)
	if err != nil {
		fmt.Fprintf(errw, "roles doctor: %v\n", err)
		return 1
	}
	if publish { // for busmon, which reads Redis only (the sentinel's daily run)
		verdicts := map[string]bus.ModelVerdict{}
		for _, r := range rows {
			verdicts[r.Agent] = bus.ModelVerdict{PID: r.PID, Configured: r.Configured, Launched: r.Launched, Used: r.Used,
				Verdict: r.Verdict, Why: r.Why}
		}
		if err := b.PutModelVerdicts(ctx, verdicts); err != nil {
			fmt.Fprintf(errw, "roles doctor: publishing: %v\n", err)
			return 1
		}
	}
	code := 0
	for _, r := range rows {
		if r.Verdict == "drift" || r.Verdict == "unknown model" {
			code = 3
		}
	}
	if asJSON {
		if writeJSON(out, errw, rows) != 0 {
			return 1
		}
		return code
	}
	fmt.Fprintf(out, "%-12s %-10s %-22s %-22s %-22s %s\n", "AGENT", "PROFILE", "CONFIGURED", "LAUNCHED", "USED", "VERDICT")
	for _, r := range rows {
		fmt.Fprintf(out, "%-12s %-10s %-22s %-22s %-22s %s\n", r.Agent, dash(r.Profile), dash(r.Configured), dash(r.Launched), dash(r.Used), r.Verdict)
		if r.Why != "" && r.Verdict != "ok" {
			fmt.Fprintf(out, "%12s %s\n", "", r.Why)
		}
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no registered agent has a role in roles.toml")
	}
	return code
}
