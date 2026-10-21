package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"slices"
	"time"

	"github.com/netbja/agent-bus-monitor/models"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// reviewCandidate is a model of another provider than the author's that can
// review code (specialist roles spec, part C): a frontier model can be the
// reviewer, a workhorse model only a second reviewer.
type reviewCandidate struct {
	Provider string `json:"provider"`
	Client   string `json:"client"`
	Model    string `json:"model"`
	Tier     string `json:"tier"`
	As       string `json:"as"` // "foureyes" or "second reviewer"
}

// reviewCandidates reads the model table: per provider other than author's,
// with a successful read under models.MaxAge and its client installed, the
// best frontier row (its default first), else the best workhorse row. A row
// whose maker (Row.Family) is the author's provider is never offered: the
// same model through another provider is no independent review. Ordered by
// provider.
func reviewCandidates(t *models.Table, author string, installed func(client string) bool, now time.Time) []reviewCandidate {
	var out []reviewCandidate
	for _, name := range slices.Sorted(maps.Keys(t.Providers)) {
		if name == author {
			continue
		}
		p, ok := t.Fresh(name, now)
		if !ok {
			continue
		}
		pick := func(tier string) (models.Row, bool) {
			var best models.Row
			found := false
			for _, r := range p.Rows {
				maker := r.Family // a provider serving another maker's model (agy)
				if maker == "" {
					maker = name
				}
				if r.Tier == tier && maker != author && installed(r.Client) && (!found || r.Default && !best.Default) {
					best, found = r, true
				}
			}
			return best, found
		}
		if r, ok := pick(models.Frontier); ok {
			out = append(out, reviewCandidate{name, r.Client, r.ID, r.Tier, "foureyes"})
		} else if r, ok := pick(models.Workhorse); ok {
			out = append(out, reviewCandidate{name, r.Client, r.ID, r.Tier, "second reviewer"})
		}
	}
	return out
}

// rolesReviewers is `agentbus roles reviewers [--json]`: the candidates for
// review on another provider than coder's, from this machine's model table.
// It decides nothing; master asks the person (agent-bus-master skill).
func rolesReviewers(m *rolecfg.Manifest, asJSON bool, out io.Writer) error {
	author := ""
	if p, err := m.Resolve("coder"); err == nil {
		author = p.Provider
	}
	tbl, err := models.Load(models.CachePath())
	if err != nil {
		return fmt.Errorf("reading the model table: %w (agentbus models --refresh)", err)
	}
	clients := map[string]string{"claude-code": "claude", "codex": "codex", "kimi-cli": "kimi", "grok": "grok", "agy": "agy"}
	installed := func(client string) bool {
		bin := clients[client]
		if bin == "" {
			return false
		}
		_, err := exec.LookPath(bin)
		return err == nil
	}
	cands := reviewCandidates(tbl, author, installed, time.Now())
	if asJSON {
		return json.NewEncoder(out).Encode(cands)
	}
	if len(cands) == 0 {
		fmt.Fprintf(out, "no reviewer on another provider than coder's (%s): none installed with a fresh frontier or workhorse model\n", author)
		return nil
	}
	for _, c := range cands {
		fmt.Fprintf(out, "%s\t%s\t%s (%s)\tcan be %s\n", c.Provider, c.Client, c.Model, c.Tier, c.As)
	}
	return nil
}

