package mergequeue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

// ghLimit is how many open PRs one read asks for (gh pages itself up to it).
// A list of exactly the limit may be cut, so it is ErrIncomplete.
const ghLimit = 1000

// ErrIncomplete: the forge's list may be cut. A pass that reads it is unknown
// and keeps its previous items, never a partial list read as whole.
var ErrIncomplete = errors.New("the forge's list of open PRs may be incomplete")

// ghPR is gh pr list's JSON, the fields asked for.
type ghPR struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
	BaseRefName string `json:"baseRefName"`
	IsDraft     bool   `json:"isDraft"`
}

// ReadGitHub lists the open PRs of the repository in dir with gh, run with
// exactly env (the caller hardens it: no askpass, GH_PROMPT_DISABLED, LC_ALL;
// gh is looked up on env's PATH). agentbus never reads a token: gh uses its
// own credentials. Bounded by ctx.
func ReadGitHub(ctx context.Context, dir string, env []string) ([]PR, error) {
	out, err := run(ctx, dir, env, "gh", "pr", "list", "--state", "open",
		"--json", "number,title,url,headRefName,headRefOid,baseRefName,isDraft", "--limit", fmt.Sprint(ghLimit))
	if err != nil {
		return nil, err
	}
	var raw []ghPR
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, errors.New("gh answered something that is not its JSON list")
	}
	if len(raw) >= ghLimit {
		return nil, ErrIncomplete
	}
	prs := make([]PR, 0, len(raw))
	for _, p := range raw {
		if p.Number <= 0 || !bus.ValidHead(p.HeadRefOid) {
			return nil, errors.New("gh listed a PR with no valid number or head")
		}
		prs = append(prs, PR{Number: p.Number, Title: cleanText(p.Title), URL: cleanText(p.URL), Branch: cleanText(p.HeadRefName),
			Head: p.HeadRefOid, Base: cleanText(p.BaseRefName), Draft: p.IsDraft})
	}
	return prs, nil
}

// cleanText is a forge's free text as the record keeps it: one line, no
// control, the whole field withheld on anything secret-shaped.
func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return health.Redact(s)
}

// run runs name, looked up on env's PATH only, in dir with exactly env;
// bounded by ctx (killed, then a second to finish). Its error names the
// command and how it ended, never its output.
func run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	path, err := lookIn(env, name)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = dir, env, time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s timed out", name)
		}
		return nil, &runError{name: name, err: err}
	}
	return out.Bytes(), nil
}

// lookIn finds an executable name on env's PATH.
func lookIn(env []string, name string) (string, error) {
	path := ""
	for _, kv := range env {
		if p, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = p
		}
	}
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			continue
		}
		f := filepath.Join(d, name)
		if st, err := os.Stat(f); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return f, nil
		}
	}
	return "", fmt.Errorf("%s is not on PATH", name)
}

// runError is a command that ended badly: its name and how, never its output.
// It unwraps to the exec error, so a caller can read the exit code.
type runError struct {
	name string
	err  error
}

func (e *runError) Error() string { return fmt.Sprintf("%s failed (%v)", e.name, e.err) }
func (e *runError) Unwrap() error { return e.err }
