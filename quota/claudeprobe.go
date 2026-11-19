package quota

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/internal/files"
)

// Claude Code answers a control request, get_usage, over its stream-json
// protocol: the account's usage windows, what /usage shows, with the CLI's
// own authentication, no prompt and no token (architect's report
// 1794690151257-0; the SDK names it experimental, so the statusline file and
// the transcript backstop stay as the fallback).

// ClaudeProbe asks the claude CLI for the account's windows, at most once
// per Every (a failure included), the answer kept in Cache.
type ClaudeProbe struct {
	Bin       string // the claude executable
	ConfigDir string // CLAUDE_CONFIG_DIR for the account; "" leaves the environment's
	Cache     string // the probe's own cache file
	Every     time.Duration
	Timeout   time.Duration
	Now       func() time.Time
}

// The probe's defaults: the quota cadence of an RPC reader (codex), and the
// bound of one CLI start (about 1.3 s measured).
const (
	ClaudeProbeEvery   = 5 * time.Minute
	ClaudeProbeTimeout = 20 * time.Second
)

// NewClaudeProbe is the probe of bin for the account of configDir, cached
// per account under CacheDir.
func NewClaudeProbe(bin, configDir string, now func() time.Time) *ClaudeProbe {
	sum := sha256.Sum256([]byte(configDir))
	return &ClaudeProbe{Bin: bin, ConfigDir: configDir, Every: ClaudeProbeEvery, Timeout: ClaudeProbeTimeout, Now: now,
		Cache: filepath.Join(CacheDir(), "quota-claude-usage-"+hex.EncodeToString(sum[:4])+".json")}
}

// probeRecord is the probe's cache: one answer, or why there is none.
type probeRecord struct {
	CollectedAt int64    `json:"collected_at"` // Unix ms
	Windows     []Window `json:"windows,omitempty"`
	Note        string   `json:"note,omitempty"`
}

// Windows is the account's windows and when they were true (Unix ms), or
// none and a note saying why.
func (p *ClaudeProbe) Windows(ctx context.Context) ([]Window, int64, string) {
	var rec probeRecord
	err := withLock(p.Cache, func() error {
		if b, err := os.ReadFile(p.Cache); err == nil && json.Unmarshal(b, &rec) == nil &&
			p.Now().Sub(time.UnixMilli(rec.CollectedAt)) < p.Every && p.Now().UnixMilli() >= rec.CollectedAt {
			return nil
		}
		rec = probeRecord{CollectedAt: p.Now().UnixMilli()}
		body, err := runClaudeUsage(ctx, p.Bin, p.ConfigDir, p.Timeout)
		if err != nil {
			rec.Note = "get_usage: " + err.Error()
		} else {
			rec.Windows, rec.Note = parseClaudeUsage(body)
		}
		b, _ := json.Marshal(rec)
		return files.WriteAtomic(p.Cache, b)
	})
	if err != nil {
		return nil, 0, "get_usage: " + err.Error()
	}
	return rec.Windows, rec.CollectedAt, rec.Note
}

// claudeUsageArgv: one-shot print mode in stream-json, only local settings,
// no MCP server, no hook, no session kept.
var claudeUsageArgv = []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
	"--setting-sources", "local", "--strict-mcp-config", "--settings", `{"disableAllHooks":true}`, "--no-session-persistence"}

// claudeUsageRequests: initialize, then get_usage without the behaviours.
const claudeUsageRequests = `{"type":"control_request","request_id":"r1","request":{"subtype":"initialize"}}` + "\n" +
	`{"type":"control_request","request_id":"r2","request":{"subtype":"get_usage","skip_behaviors":true}}` + "\n"

// runClaudeUsage runs the CLI in a new empty directory (removed after), sends
// the two requests and returns get_usage's answer; the CLI is killed, its
// process group with it, as soon as the answer is read or timeout passes.
func runClaudeUsage(ctx context.Context, bin, configDir string, timeout time.Duration) ([]byte, error) {
	if bin == "" {
		return nil, fmt.Errorf("no claude CLI")
	}
	dir, err := os.MkdirTemp("", "agentbus-claude-usage-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command(bin, claudeUsageArgv...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = io.Discard
	if configDir != "" {
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+configDir)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	kill := func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	defer func() { kill(); _ = cmd.Wait() }()
	stop := context.AfterFunc(ctx, kill) // the timeout ends the read
	defer stop()
	if _, err := io.WriteString(in, claudeUsageRequests); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var line struct {
			Type     string `json:"type"`
			Response struct {
				Subtype   string          `json:"subtype"`
				RequestID string          `json:"request_id"`
				Response  json.RawMessage `json:"response"`
				Error     string          `json:"error"`
			} `json:"response"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != "control_response" || line.Response.RequestID != "r2" {
			continue
		}
		if line.Response.Subtype != "success" {
			return nil, fmt.Errorf("refused (%s)", oneLine(line.Response.Error))
		}
		return line.Response.Response, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("no answer within %v", timeout)
	}
	return nil, fmt.Errorf("the CLI ended without an answer")
}

func oneLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

type usageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

type usageLimit struct {
	Kind     string   `json:"kind"`
	Percent  *float64 `json:"percent"`
	ResetsAt string   `json:"resets_at"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

// parseClaudeUsage reads get_usage's answer, tolerantly: five_hour,
// seven_day and the other statusline names (claudeWindowNames) as windows;
// in limits[], weekly_scoped as 7d-<model>, and session and weekly_all
// only where five_hour or seven_day is missing. Unknown keys and kinds are
// ignored; a window without a percent or a readable reset is skipped. No
// window: the note says why.
func parseClaudeUsage(b []byte) ([]Window, string) {
	var u struct {
		Available  *bool                      `json:"rate_limits_available"`
		RateLimits map[string]json.RawMessage `json:"rate_limits"`
	}
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, "get_usage: an answer that is not JSON"
	}
	if u.Available != nil && !*u.Available {
		return nil, "get_usage: no rate limits on this account (an API key?)"
	}
	got := map[string]Window{}
	var order []string
	add := func(w Window) {
		if _, ok := got[w.Name]; !ok {
			order = append(order, w.Name)
			got[w.Name] = w
		}
	}
	for key, raw := range u.RateLimits {
		m, ok := claudeWindowNames[key]
		if !ok {
			continue
		}
		var w usageWindow
		if json.Unmarshal(raw, &w) != nil || w.Utilization == nil {
			continue
		}
		if at, ok := usageReset(w.ResetsAt); ok {
			add(Window{Name: m.name, UsedPct: *w.Utilization, ResetsAt: at, Minutes: m.minutes})
		}
	}
	var limits []usageLimit
	_ = json.Unmarshal(u.RateLimits["limits"], &limits)
	for _, l := range limits {
		at, ok := usageReset(l.ResetsAt)
		if l.Percent == nil || !ok {
			continue
		}
		switch l.Kind {
		case "session":
			add(Window{Name: "5h", UsedPct: *l.Percent, ResetsAt: at, Minutes: 300})
		case "weekly_all":
			add(Window{Name: "7d", UsedPct: *l.Percent, ResetsAt: at, Minutes: 10080})
		case "weekly_scoped":
			if l.Scope == nil || l.Scope.Model == nil {
				continue
			}
			if slug := modelSlug(l.Scope.Model.DisplayName); slug != "" {
				add(Window{Name: "7d-" + slug, UsedPct: *l.Percent, ResetsAt: at, Minutes: 10080})
			}
		}
	}
	if len(got) == 0 {
		return nil, "get_usage: no window in the answer (an unknown format)"
	}
	out := make([]Window, 0, len(order))
	for _, n := range order {
		out = append(out, got[n])
	}
	return out, ""
}

// usageReset reads an RFC 3339 reset as Unix seconds.
func usageReset(s string) (int64, bool) {
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || at.Unix() <= 0 {
		return 0, false
	}
	return at.Unix(), true
}

// modelSlug is a model's display name lowered, every run of other
// characters one '-': "Claude Opus" → "claude-opus".
func modelSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	return b.String()
}
