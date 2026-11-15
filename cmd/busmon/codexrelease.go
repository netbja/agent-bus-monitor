package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/clientfp"
)

// A newer Codex published (spec 2026-11-15-login-updates-design.md 4): one
// line in busmon, beside the release check and under the same opt-out
// (updates.check). Codex needs the person's install step; nothing installs
// it (busmon, the dispatcher or an agent). Grok and agy have no documented
// version endpoint, Claude Code updates itself: no line for them.

// codexLatestURL is npm's answer for the published Codex; only its version
// is read, and the request carries nothing of the person's.
const codexLatestURL = "https://registry.npmjs.org/@openai/codex/latest"

// versionPattern is all a version shown in busmon may be (foureyes): the
// registry's answer, the cache and an agent's label are drawn in the
// terminal, so anything else (an escape sequence included) is no version.
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)

// plainVersion is v when it is a version, else "".
func plainVersion(v string) string {
	if len(v) > 64 || !versionPattern.MatchString(v) {
		return ""
	}
	return v
}

// manifestMax bounds the registry's answer (the /latest manifest is a few KB):
// a longer one is refused, whatever it holds (reviewer-codex: a cut answer
// can still parse when only whitespace follows the object).
const manifestMax = 1 << 20

// clientCache is latestCodex's day: the last check and what it found ("" when
// it failed).
type clientCache struct {
	CheckedAt time.Time `json:"checked_at"`
	Version   string    `json:"version"`
}

// latestCodex is the published Codex version, asking the registry at most
// once every 24 hours (cache file) within 3 s. A failure is silent and
// recorded, so it is not retried before the next day.
func latestCodex(ctx context.Context, hc *http.Client, url, cachePath string, now time.Time) string {
	var c clientCache
	if b, err := os.ReadFile(cachePath); err == nil && json.Unmarshal(b, &c) == nil && now.Sub(c.CheckedAt) < 24*time.Hour {
		return plainVersion(c.Version)
	}
	c = clientCache{CheckedAt: now}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil); err == nil {
		if resp, err := hc.Do(req); err == nil {
			var v struct {
				Version string `json:"version"`
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, manifestMax+1))
			if resp.StatusCode == http.StatusOK && err == nil && len(body) <= manifestMax && json.Unmarshal(body, &v) == nil {
				c.Version = plainVersion(v.Version)
			}
			resp.Body.Close()
		}
	}
	if b, err := json.Marshal(c); err == nil {
		_ = os.MkdirAll(filepath.Dir(cachePath), 0o700)
		_ = os.WriteFile(cachePath, b, 0o600)
	}
	return c.Version
}

// codexCachePath is ${XDG_CACHE_HOME:-~/.cache}/agentbus/client-codex.json.
func codexCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "agentbus", "client-codex.json")
}

// release is a bare npm version as buildinfo compares it ("v" prefixed).
func release(v string) string {
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// codexInstalled is the oldest Codex version the project's agents run: the
// label of a Codex agent's client_fp (its package.json); "" when none says.
func codexInstalled(snaps map[string]bus.AgentSnapshot) string {
	oldest := ""
	for _, s := range snaps {
		if s.Client != "codex" {
			continue
		}
		fp, err := clientfp.Decode(s.ClientFP)
		if err != nil || plainVersion(fp.Label) == "" {
			continue
		}
		if oldest == "" || buildinfo.Newer(release(oldest), release(fp.Label)) {
			oldest = fp.Label
		}
	}
	return oldest
}

// codexNotice is the line when published is newer than installed, else "".
func codexNotice(installed, published string) string {
	if installed == "" || !buildinfo.Newer(release(published), release(installed)) {
		return ""
	}
	return "codex " + published + " available (installed " + installed + "): npm install -g @openai/codex@latest"
}

// checkCodex is the line now: nothing asked while updates.check is off or no
// Codex agent says its version.
func checkCodex(ctx context.Context, on bool, agents func(context.Context) (map[string]bus.AgentSnapshot, error),
	hc *http.Client, url, cachePath string, now time.Time) string {
	if !on || cachePath == "" {
		return ""
	}
	snaps, err := agents(ctx)
	if err != nil {
		return ""
	}
	installed := codexInstalled(snaps)
	if installed == "" {
		return ""
	}
	return codexNotice(installed, latestCodex(ctx, hc, url, cachePath, now))
}

// watchCodex keeps st.codexUpdate current in the background, never on the
// UI path: at start, then every hour (the cache asks the registry once a day;
// the hour finds a Codex agent registered since).
func watchCodex(ctx context.Context, st *shared, on bool, agents func(context.Context) (map[string]bus.AgentSnapshot, error)) {
	for {
		line := checkCodex(ctx, on, agents, http.DefaultClient, codexLatestURL, codexCachePath(), time.Now())
		st.mu.Lock()
		st.codexUpdate = line
		st.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

// withCodexRow adds the line to the About page: a line of its text, and a
// row of THIS BUILD with the install command as its note.
func withCodexRow(line string, v aboutVersions) aboutVersions {
	if line == "" {
		return v
	}
	v.Lines = append(v.Lines, line)
	what, cmd, _ := strings.Cut(strings.TrimPrefix(line, "codex "), ": ")
	v.Build = append(v.Build, aboutRow{Label: "codex", Value: GlyphUp + " " + what, Role: RoleAttention, Note: cmd, NoteRole: RoleMuted})
	return v
}

// withCodexUpdate gives the About page the watch's current line.
func withCodexUpdate(src aboutSources, st *shared) aboutSources {
	src.codex = func() string {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.codexUpdate
	}
	return src
}
