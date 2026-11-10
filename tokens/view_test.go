package tokens

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestProjectDays(t *testing.T) {
	cases := []struct {
		name string
		rows map[string]bus.TokenRow
		want map[string]bus.TokenRow
	}{
		{"empty", nil, map[string]bus.TokenRow{}},
		{"one row", map[string]bus.TokenRow{
			"2026-11-04|coder|anthropic": {In: 1, Write: 2, Read: 3, Out: 4, N: 5},
		}, map[string]bus.TokenRow{
			"2026-11-04": {In: 1, Write: 2, Read: 3, Out: 4, N: 5},
		}},
		{"summed per day, days kept apart", map[string]bus.TokenRow{
			"2026-11-04|coder|anthropic":  {In: 1, Write: 2, Read: 3, Out: 4, N: 1},
			"2026-11-04|master|anthropic": {In: 10, Write: 20, Read: 30, Out: 40, N: 2},
			"2026-11-03|coder|openai":     {In: 7, Out: 1, N: 1},
		}, map[string]bus.TokenRow{
			"2026-11-04": {In: 11, Write: 22, Read: 33, Out: 44, N: 3},
			"2026-11-03": {In: 7, Out: 1, N: 1},
		}},
		{"a key without separators is its own day", map[string]bus.TokenRow{
			"_meta": {N: 1},
		}, map[string]bus.TokenRow{"_meta": {N: 1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ProjectDays(c.rows); !reflect.DeepEqual(got, c.want) {
				t.Errorf("ProjectDays = %v, want %v", got, c.want)
			}
		})
	}
}

func TestLastDays(t *testing.T) {
	utc := time.UTC
	now := time.Date(2026, 11, 2, 23, 30, 0, 0, utc)
	cases := []struct {
		name string
		loc  *time.Location
		n    int
		want []string
	}{
		{"none", utc, 0, []string{}},
		{"today only", utc, 1, []string{"2026-11-02"}},
		{"oldest first across a month", utc, 3, []string{"2026-10-31", "2026-11-01", "2026-11-02"}},
		{"local day after midnight", paris, 2, []string{"2026-11-02", "2026-11-03"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LastDays(now, c.loc, c.n); !reflect.DeepEqual(got, c.want) {
				t.Errorf("LastDays = %v, want %v", got, c.want)
			}
		})
	}
}

// rowView is an AgentRow with its pointers read, -1 for unknown.
type rowView struct {
	Agent, Provider string
	In, Out         int64
	Reason          string
}

func view(rows []AgentRow) []rowView {
	out := make([]rowView, 0, len(rows))
	for _, r := range rows {
		v := rowView{Agent: r.Agent, Provider: r.Provider, In: -1, Out: -1, Reason: r.Reason}
		if r.In != nil {
			v.In = *r.In
		}
		if r.Out != nil {
			v.Out = *r.Out
		}
		out = append(out, v)
	}
	return out
}

func TestAgentRows(t *testing.T) {
	const today = "2026-11-04"
	const sess = "0a1b2c3d-4e5f-6789-abcd-ef0123456789"
	cases := []struct {
		name   string
		rows   map[string]bus.TokenRow
		known  bool
		reason string
		agents map[string]bus.AgentSnapshot
		want   []rowView
	}{
		{name: "nothing", known: true, want: []rowView{}},
		{
			name:  "today's rows summed per agent, other days and malformed keys skipped",
			known: true,
			rows: map[string]bus.TokenRow{
				today + "|coder|anthropic":   {In: 100, Write: 50, Out: 10},
				today + "|coder|anthropic2":  {In: 1, Write: 0, Out: 1},
				"2026-11-03|coder|anthropic": {In: 999, Out: 999},
				today + "|broken":            {In: 5},
				today + "|master|anthropic":  {In: 1000, Out: 1},
			},
			want: []rowView{
				{Agent: "master", Provider: "anthropic", In: 1000, Out: 1},
				{Agent: "coder", Provider: "anthropic", In: 151, Out: 11},
			},
		},
		{
			name:   "no key: every registered agent unknown with the reason",
			known:  false,
			reason: "no data",
			agents: map[string]bus.AgentSnapshot{"coder": {Provider: "anthropic", PID: 1, IdentSession: sess}},
			want:   []rowView{{Agent: "coder", Provider: "anthropic", In: -1, Out: -1, Reason: "no data"}},
		},
		{
			name:  "reasons by client, provider and proof; a proved session is zero",
			known: true,
			agents: map[string]bus.AgentSnapshot{
				"a-codex":   {Client: "codex"},
				"b-openai":  {Provider: "openai"},
				"c-kimi":    {Client: "kimi"},
				"d-moon":    {Provider: "moonshot"},
				"e-nopid":   {Provider: "anthropic", IdentSession: sess},
				"f-badsess": {Provider: "anthropic", PID: 7, IdentSession: "../x"},
				"g-proved":  {Provider: "anthropic", PID: 7, IdentSession: sess},
			},
			want: []rowView{
				{Agent: "g-proved", Provider: "anthropic", In: 0, Out: 0},
				{Agent: "a-codex", In: -1, Out: -1, Reason: "Codex session not proved"},
				{Agent: "b-openai", Provider: "openai", In: -1, Out: -1, Reason: "Codex session not proved"},
				{Agent: "c-kimi", In: -1, Out: -1, Reason: "Kimi logs give no token counts"},
				{Agent: "d-moon", Provider: "moonshot", In: -1, Out: -1, Reason: "Kimi logs give no token counts"},
				{Agent: "e-nopid", Provider: "anthropic", In: -1, Out: -1, Reason: "no session proved with its process"},
				{Agent: "f-badsess", Provider: "anthropic", In: -1, Out: -1, Reason: "no session proved with its process"},
			},
		},
		{
			// idle-view-providers step 2: only Claude Code's sessions are counted,
			// so another client never reads 0, proved or not: it says it has no adapter.
			name:  "a client with no token adapter says so, never 0",
			known: true,
			agents: map[string]bus.AgentSnapshot{
				"a-grok":   {Client: "grok", Provider: "xai", PID: 7, IdentSession: sess},
				"b-agy":    {Client: "agy", Provider: "google"},
				"c-xai":    {Provider: "xai", PID: 7, IdentSession: sess},
				"d-other":  {Client: "vibe", Provider: "mistral", PID: 7, IdentSession: sess},
				"e-claude": {Client: "claude-code", Provider: "anthropic", PID: 7, IdentSession: sess},
			},
			want: []rowView{
				{Agent: "e-claude", Provider: "anthropic", In: 0, Out: 0},
				{Agent: "a-grok", Provider: "xai", In: -1, Out: -1, Reason: "no token count for grok (no adapter)"},
				{Agent: "b-agy", Provider: "google", In: -1, Out: -1, Reason: "no token count for agy (no adapter)"},
				{Agent: "c-xai", Provider: "xai", In: -1, Out: -1, Reason: "no token count for xai (no adapter)"},
				{Agent: "d-other", Provider: "mistral", In: -1, Out: -1, Reason: "no token count for vibe (no adapter)"},
			},
		},
		{
			name:   "an agent with rows today keeps its counts, not a reason",
			known:  true,
			rows:   map[string]bus.TokenRow{today + "|coder|openai": {In: 3, Out: 4}},
			agents: map[string]bus.AgentSnapshot{"coder": {Client: "codex", Provider: "openai"}},
			want:   []rowView{{Agent: "coder", Provider: "openai", In: 3, Out: 4}},
		},
		{
			name:  "equal totals sort by name",
			known: true,
			rows: map[string]bus.TokenRow{
				today + "|zed|anthropic":   {In: 1, Out: 1},
				today + "|alpha|anthropic": {In: 2},
			},
			want: []rowView{
				{Agent: "alpha", Provider: "anthropic", In: 2, Out: 0},
				{Agent: "zed", Provider: "anthropic", In: 1, Out: 1},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := view(AgentRows(c.rows, c.known, c.reason, c.agents, today))
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("AgentRows =\n%+v\nwant\n%+v", got, c.want)
			}
		})
	}
}

func TestDay(t *testing.T) {
	cases := []struct {
		ts, want string
		loc      *time.Location
	}{
		{"2026-11-04T10:00:00Z", "2026-11-04", time.UTC},
		{"2026-11-04T23:30:00.123Z", "2026-11-05", paris},
		{"not a time", "", time.UTC},
		{"", "", time.UTC},
	}
	for _, c := range cases {
		if got := day(c.ts, c.loc); got != c.want {
			t.Errorf("day(%q) = %q, want %q", c.ts, got, c.want)
		}
	}
}

func TestBaseName(t *testing.T) {
	cases := map[string]string{
		"/a/b/rollout.jsonl": "rollout.jsonl",
		"rollout.jsonl":      "rollout.jsonl",
		"/a/b/":              "",
		"":                   "",
	}
	for in, want := range cases {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Used reports the last pass's spend, reset at each pass: a file read once
// costs nothing on the next pass when nothing was appended.
func TestScansReportWhatTheyUsed(t *testing.T) {
	dir := t.TempDir()
	cpath := filepath.Join(dir, "claude.jsonl")
	write(t, cpath, cLine("2026-11-04T10:00:00Z", "m1", "r1", 1, 2, 3, 4))
	xpath := filepath.Join(dir, "rollout.jsonl")
	write(t, xpath, "{}\n")

	cs := NewClaudeScan(paris)
	xs := NewCodexScan(paris)
	cases := []struct {
		name string
		path string
		pass func([]string, Budget) bool
		used func() Budget
	}{
		{"claude", cpath, cs.Pass, cs.Used},
		{"codex", xpath, xs.Pass, xs.Used},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info, err := os.Stat(c.path)
			if err != nil {
				t.Fatal(err)
			}
			if got := c.used(); got != (Budget{}) {
				t.Errorf("Used before any pass = %+v, want zero", got)
			}
			if partial := c.pass([]string{c.path}, big); partial {
				t.Errorf("first pass partial")
			}
			if got, want := c.used(), (Budget{Files: 1, Bytes: info.Size()}); got != want {
				t.Errorf("Used after first pass = %+v, want %+v", got, want)
			}
			c.pass([]string{c.path}, big)
			if got := c.used(); got != (Budget{}) {
				t.Errorf("Used after an idle pass = %+v, want zero", got)
			}
		})
	}
}

// An agent with rows under two providers today shows one provider by a
// stable rule, never by map order: the most tokens today, else (a tie) the
// agent's snapshot provider, else the lexically first.
func TestAgentRowsProviderRule(t *testing.T) {
	const today = "2026-11-04"
	cases := []struct {
		name   string
		rows   map[string]bus.TokenRow
		agents map[string]bus.AgentSnapshot
		want   string
	}{
		{
			name: "the most tokens today wins over the snapshot and the lexical order",
			rows: map[string]bus.TokenRow{
				today + "|coder|anthropic": {In: 10, Out: 1},
				today + "|coder|zeta":      {In: 5, Write: 5, Out: 2},
			},
			agents: map[string]bus.AgentSnapshot{"coder": {Provider: "anthropic"}},
			want:   "zeta",
		},
		{
			name: "a tie: the snapshot's provider, though lexically later",
			rows: map[string]bus.TokenRow{
				today + "|coder|anthropic": {In: 10, Out: 2},
				today + "|coder|zeta":      {In: 6, Write: 4, Out: 2},
			},
			agents: map[string]bus.AgentSnapshot{"coder": {Provider: "zeta"}},
			want:   "zeta",
		},
		{
			name: "a tie the snapshot does not settle: the lexically first",
			rows: map[string]bus.TokenRow{
				today + "|coder|zeta":      {In: 10, Out: 2},
				today + "|coder|mid":       {In: 1},
				today + "|coder|anthropic": {In: 12},
			},
			agents: map[string]bus.AgentSnapshot{"coder": {Provider: "mid"}},
			want:   "anthropic",
		},
		{
			name: "a tie and no snapshot: the lexically first",
			rows: map[string]bus.TokenRow{
				today + "|coder|zeta":      {Out: 3},
				today + "|coder|anthropic": {In: 3},
			},
			want: "anthropic",
		},
	}
	for _, c := range cases {
		for i := 0; i < 50; i++ { // map order varies from run to run
			got := AgentRows(c.rows, true, "", c.agents, today)
			if len(got) != 1 || got[0].Provider != c.want {
				t.Fatalf("%s (run %d): %+v", c.name, i, got)
			}
		}
	}
}
