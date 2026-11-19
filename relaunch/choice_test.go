package relaunch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// Replaced: a new process of the role, proved live (its recorded start
// matches), in another pane, on the expected client and provider (dispatchsvc
// P1, moved from cmd/agentbus swap.go unchanged).
func TestReplaced(t *testing.T) {
	old := bus.AgentSnapshot{PID: 100, PIDStart: 7, Pane: "t1"}
	p := rolecfg.Profile{Client: "claude-code", Provider: "anthropic"}
	starts := map[int]uint64{200: 9, 100: 7}
	procStart := func(pid int) (uint64, error) {
		if s, ok := starts[pid]; ok {
			return s, nil
		}
		return 0, errors.New("gone")
	}
	good := bus.AgentSnapshot{PID: 200, PIDStart: 9, Pane: "t2", Client: "claude-code", Provider: "anthropic"}
	if !Replaced(good, old, p, procStart) {
		t.Fatal("a new live process in a new pane on the profile: not replaced")
	}
	for name, s := range map[string]bus.AgentSnapshot{
		"no pid":          {PIDStart: 9, Pane: "t2", Client: "claude-code", Provider: "anthropic"},
		"the old process": {PID: 100, PIDStart: 7, Pane: "t2", Client: "claude-code", Provider: "anthropic"},
		"no pane":         {PID: 200, PIDStart: 9, Client: "claude-code", Provider: "anthropic"},
		"the old pane":    {PID: 200, PIDStart: 9, Pane: "t1", Client: "claude-code", Provider: "anthropic"},
		"start differs":   {PID: 200, PIDStart: 8, Pane: "t2", Client: "claude-code", Provider: "anthropic"},
		"process gone":    {PID: 300, PIDStart: 9, Pane: "t2", Client: "claude-code", Provider: "anthropic"},
		"other client":    {PID: 200, PIDStart: 9, Pane: "t2", Client: "codex", Provider: "anthropic"},
		"other provider":  {PID: 200, PIDStart: 9, Pane: "t2", Client: "claude-code", Provider: "openai"},
	} {
		if Replaced(s, old, p, procStart) {
			t.Errorf("%s: replaced", name)
		}
	}
	// The same pid with a new start is a new process (a recycled pid).
	recycled := good
	recycled.PID, recycled.PIDStart = 100, 9
	starts[100] = 9
	if !Replaced(recycled, old, p, procStart) {
		t.Error("a recycled pid with a new start: not replaced")
	}
}

func TestRecoveryHint(t *testing.T) {
	for _, c := range []struct{ profile, session, want string }{
		{"", "", "recover with agentbus --project demo pop coder, or agentbus up demo --resume"},
		{"opus", "", "recover with agentbus --project demo pop coder --profile opus, or agentbus up demo --resume"},
		{"opus", "abc12345", "recover with agentbus --project demo pop coder --profile opus --resume abc12345, or agentbus up demo --resume"},
		{"", "abc12345", "recover with agentbus --project demo pop coder --resume abc12345, or agentbus up demo --resume"},
	} {
		if got := RecoveryHint("demo", "coder", c.profile, c.session); got != c.want {
			t.Errorf("RecoveryHint(%q, %q) = %q, want %q", c.profile, c.session, got, c.want)
		}
	}
}

// ResumeChoice: the profile and session a recorded resume runs on, or why it
// cannot, and a note when the recorded profile is replaced by the role's own.
func TestResumeChoice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roles.toml")
	toml := "schema_version = 2\n[defaults]\nprofile = \"opus\"\n" +
		"[profiles.main]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"m\"\n" +
		"[profiles.opus]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"manual\" }\n" +
		"[profiles.k]\nclient = \"kimi-cli\"\nprovider = \"moonshot\"\nmodel = \"alias\"\n" +
		"[profiles.g]\nclient = \"claude-code\"\nprovider = \"other\"\nmodel = \"x\"\noptions = { permission = \"manual\" }\n" +
		"[profiles.h]\nclient = \"claude-code\"\nprovider = \"third\"\nmodel = \"y\"\noptions = { permission = \"manual\" }\n" +
		"[roles.coder]\ntier = \"pop\"\nskills = [\"s\"]\nfallback_profiles = [\"h\", \"k\"]\n"
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := rolecfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	const s = "abcdef12-3456"
	for _, c := range []struct {
		name, role                      string
		rec                             *bus.ResumeRecord
		profile, session, invalid, note string
	}{
		{name: "no record", role: "coder"},
		{name: "own profile", role: "coder", rec: &bus.ResumeRecord{Session: s}, session: s},
		{name: "own profile by name", role: "coder", rec: &bus.ResumeRecord{Profile: "opus", Session: s}, session: s},
		{name: "a fallback", role: "coder", rec: &bus.ResumeRecord{Profile: "h", Session: s}, profile: "h", session: s},
		{name: "a fallback without verified resume", role: "coder", rec: &bus.ResumeRecord{Profile: "k", Session: s}, invalid: "client kimi-cli has no verified resume"},
		{name: "another profile on the own client", role: "coder", rec: &bus.ResumeRecord{Profile: "g", Session: s}, session: s,
			note: "its recorded profile g is neither coder's own nor one of its fallback_profiles: session " + s + " resumes on its own profile opus"},
		{name: "another client's profile", role: "coder", rec: &bus.ResumeRecord{Profile: "main", Session: s}, invalid: "profile main is neither coder's own nor one of its fallback_profiles"},
		{name: "an invalid session", role: "coder", rec: &bus.ResumeRecord{Profile: "g", Session: "bad"}, invalid: "its session id is not valid"},
		{name: "an unknown role", role: "nobody", rec: &bus.ResumeRecord{Session: s}, invalid: "*"},
	} {
		profile, session, invalid, note := ResumeChoice(m, c.role, c.rec)
		if c.invalid == "*" {
			if invalid == "" || profile != "" || session != "" {
				t.Errorf("%s: (%q, %q, %q), want an error", c.name, profile, session, invalid)
			}
			continue
		}
		if profile != c.profile || session != c.session || invalid != c.invalid || note != c.note {
			t.Errorf("%s: (%q, %q, %q, %q), want (%q, %q, %q, %q)", c.name, profile, session, invalid, note, c.profile, c.session, c.invalid, c.note)
		}
	}
}
