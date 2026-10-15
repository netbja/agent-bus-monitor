package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

// Codex (spec §5.2): the documented app-server request account/rateLimits/read
// first, then the newest rate_limits record of its session logs.

// CodexReader reads Codex's windows.
type CodexReader struct {
	Bin  string // codex executable; "" disables the RPC
	Home string // $CODEX_HOME or ~/.codex
	Now  func() time.Time
}

func (CodexReader) Provider() string { return "openai" }

func (c CodexReader) Read(ctx context.Context) (Reading, error) {
	now := c.Now()
	if c.Bin != "" {
		if ws, err := codexRPC(ctx, c.Bin); err == nil && len(ws) > 0 {
			return Reading{Provider: "openai", Windows: ws, ObservedAt: now.UnixMilli(), CollectedAt: now.UnixMilli(), Source: "codex app-server"}, nil
		}
	}
	r, ok := codexJSONL(filepath.Join(c.Home, "sessions"), now)
	if !ok {
		return Unknown("openai", "codex", "no rate_limits from the app-server or the session logs"), nil
	}
	r.CollectedAt = now.UnixMilli()
	return r, nil
}

type codexRPCWindow struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins int      `json:"windowDurationMins"`
	ResetsAt           int64    `json:"resetsAt"`
}

type codexRPCLimits struct {
	Primary   *codexRPCWindow `json:"primary"`
	Secondary *codexRPCWindow `json:"secondary"`
}

func (l *codexRPCLimits) windows(prefix string) []Window {
	var out []Window
	for _, w := range []*codexRPCWindow{l.Primary, l.Secondary} {
		if w == nil || w.UsedPercent == nil || w.ResetsAt <= 0 {
			continue
		}
		name := WindowName(w.WindowDurationMins)
		if prefix != "" && prefix != "codex" {
			name = prefix + "-" + name
		}
		out = append(out, Window{Name: name, UsedPct: *w.UsedPercent, ResetsAt: w.ResetsAt, Minutes: w.WindowDurationMins})
	}
	return out
}

// codexRPC runs `codex app-server` over stdio: initialize, initialized,
// account/rateLimits/read. Fixed argv, stderr discarded, 10 s bound, process
// group killed. No model turn.
func codexRPC(ctx context.Context, bin string) ([]Window, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.Command(bin, "app-server")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	}()
	go func() {
		<-ctx.Done()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}()
	send := func(v any) error {
		b, _ := json.Marshal(v)
		_, err := in.Write(append(b, '\n'))
		return err
	}
	if err := send(map[string]any{"method": "initialize", "id": 0,
		"params": map[string]any{"clientInfo": map[string]string{"name": "agentbus", "title": "agentbus", "version": "0.13"}}}); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(outPipe)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var msg struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil || msg.ID == nil {
			continue // notifications
		}
		switch *msg.ID {
		case 0:
			if len(msg.Error) > 0 {
				return nil, errors.New("codex app-server refused initialize")
			}
			if err := send(map[string]any{"method": "initialized"}); err != nil {
				return nil, err
			}
			if err := send(map[string]any{"method": "account/rateLimits/read", "id": 1}); err != nil {
				return nil, err
			}
		case 1:
			if len(msg.Error) > 0 {
				return nil, errors.New("codex app-server refused account/rateLimits/read")
			}
			return parseCodexRateLimits(msg.Result)
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, errors.New("codex app-server closed without an answer")
}

func parseCodexRateLimits(raw json.RawMessage) ([]Window, error) {
	var res struct {
		RateLimits          *codexRPCLimits           `json:"rateLimits"`
		RateLimitsByLimitID map[string]codexRPCLimits `json:"rateLimitsByLimitId"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	var out []Window
	if len(res.RateLimitsByLimitID) > 0 {
		ids := make([]string, 0, len(res.RateLimitsByLimitID))
		for id := range res.RateLimitsByLimitID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			l := res.RateLimitsByLimitID[id]
			out = append(out, l.windows(id)...)
		}
	} else if res.RateLimits != nil {
		out = res.RateLimits.windows("")
	}
	if len(out) == 0 {
		return nil, errors.New("no rate limit window in the answer")
	}
	return out, nil
}

// codexJSONL: the newest token_count rate_limits across the session logs
// modified in the last 7 days (a resumed session keeps writing to the file of
// the day it was created).
func codexJSONL(root string, now time.Time) (Reading, bool) {
	var files []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".jsonl" {
			return nil
		}
		if info, err := d.Info(); err == nil && now.Sub(info.ModTime()) <= 7*24*time.Hour {
			files = append(files, p)
		}
		return nil
	})
	best := Reading{}
	for _, f := range files {
		for _, line := range tailLines(f, 2<<20) {
			var e struct {
				Timestamp string `json:"timestamp"`
				Type      string `json:"type"`
				Payload   struct {
					Type       string `json:"type"`
					RateLimits *struct {
						Primary   *codexJSONLWindow `json:"primary"`
						Secondary *codexJSONLWindow `json:"secondary"`
					} `json:"rate_limits"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &e) != nil || e.Type != "event_msg" || e.Payload.Type != "token_count" || e.Payload.RateLimits == nil {
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
			if err != nil || at.UnixMilli() <= best.ObservedAt {
				continue
			}
			var ws []Window
			for _, w := range []*codexJSONLWindow{e.Payload.RateLimits.Primary, e.Payload.RateLimits.Secondary} {
				if w != nil && w.UsedPercent != nil && w.ResetsAt > 0 {
					ws = append(ws, Window{Name: WindowName(w.WindowMinutes), UsedPct: *w.UsedPercent, ResetsAt: w.ResetsAt, Minutes: w.WindowMinutes})
				}
			}
			if len(ws) > 0 {
				best = Reading{Provider: "openai", Windows: ws, ObservedAt: at.UnixMilli(), Source: "codex session log"}
			}
		}
	}
	return best, best.ObservedAt > 0
}

type codexJSONLWindow struct {
	UsedPercent   *float64 `json:"used_percent"`
	WindowMinutes int      `json:"window_minutes"`
	ResetsAt      int64    `json:"resets_at"`
}
