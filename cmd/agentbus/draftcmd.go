package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// rolesDraft is `agentbus roles draft <role> --task <words...> [--out <dir>]
// [--force]` (specialist roles spec, addendum E): it writes the entry and the
// prompt under <out>/<role>/ and prints a JSON summary for master's ask. It
// never touches the manifest: a draft is inert until the person adopts it.
func rolesDraft(m *rolecfg.Manifest, rest, env []string, stdout io.Writer) error {
	rest, out := extractFlag(rest, "--out")
	rest, force := extractBool(rest, "--force")
	if len(rest) < 3 || rest[1] != "--task" {
		return errors.New("usage: draft <role> --task <words...> [--out <dir>] [--force]")
	}
	role, task := rest[0], strings.Join(rest[2:], " ")
	if out == "" {
		out = filepath.Join(".agent-bus", "drafts")
	}
	skills := rolecfg.DraftSkills(task, rolecfg.AvailableSkills(env))
	snippet, prompt, err := rolecfg.DraftRole(m, role, task, skills)
	if err != nil {
		return err
	}
	dir := filepath.Join(out, role)
	if _, err := os.Stat(dir); err == nil && !force {
		return fmt.Errorf("a draft of %s exists in %s (--force replaces it)", role, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := []string{filepath.Join(dir, "roles.toml"), filepath.Join(dir, role+".md")}
	for i, body := range []string{snippet, prompt} {
		if err := os.WriteFile(files[i], []byte(body), 0o644); err != nil {
			return err
		}
	}
	return json.NewEncoder(stdout).Encode(struct {
		Role   string                `json:"role"`
		Files  []string              `json:"files"`
		Skills []rolecfg.ScoredSkill `json:"skills"`
		QID    string                `json:"question_id"` // the id master's ask must give its question
	}{role, files, skills, draftQuestionID(role, []byte(snippet), []byte(prompt))})
}

// draftQuestionID binds the person's answer to one role and one draft: the id
// of the ask's question is "adopt-" and 16 hex digits of the SHA-256 of the
// role, the entry and the prompt. adopt recomputes it from the files, so an
// answer adopts that exact draft of that role, nothing edited since.
func draftQuestionID(role string, snippet, prompt []byte) string {
	h := sha256.New()
	h.Write([]byte(role))
	h.Write([]byte{0})
	h.Write(snippet)
	h.Write([]byte{0})
	h.Write(prompt)
	return "adopt-" + hex.EncodeToString(h.Sum(nil))[:16]
}

// draftEntryOnly accepts an entry that defines [roles.<role>] and nothing
// else: no other role, no profile, no top-level key.
func draftEntryOnly(snippet, role string) error {
	var doc map[string]interface{}
	if _, err := toml.Decode(snippet, &doc); err != nil {
		return fmt.Errorf("the draft's entry is not valid TOML: %w", err)
	}
	roles, ok := doc["roles"].(map[string]interface{})
	if len(doc) != 1 || !ok || len(roles) != 1 || roles[role] == nil {
		return fmt.Errorf("the draft's entry must define [roles.%s] and nothing else", role)
	}
	return nil
}

// adoptApproved: the ask's question qid (the draft's own id) is answered
// with the option "adopt", not withdrawn, by someone other than the asker.
func adoptApproved(a bus.Ask, qid string) bool {
	for _, q := range a.Questions {
		if q.ID == qid && q.Answer != nil && !q.Answer.Withdrawn && strings.EqualFold(q.Answer.Choice, "adopt") && q.Answer.By != a.From {
			return true
		}
	}
	return false
}

// runAdopt is `agentbus --project <p> roles adopt <role> --ask <id> [--from
// <dir>]`: only when the person answered "adopt" to that ask, it appends the
// draft's entry to the manifest (after checking the result loads) and copies
// the prompt to ROLES_DIR (default <repo>/roles), never over an existing one.
// A convenience, not a lock: a process with bus access can write an answer.
func runAdopt(ctx context.Context, b *bus.Bus, self string, args, env []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "agentbus roles adopt: "+format+"\n", a...)
		return 1
	}
	args, askID := extractFlag(args, "--ask")
	args, from := extractFlag(args, "--from")
	if len(args) != 1 || askID == "" {
		return fail("usage: roles adopt <role> --ask <id> [--from <dir>]")
	}
	role := args[0]
	if from == "" {
		from = filepath.Join(".agent-bus", "drafts")
	}
	asks, err := b.Asks(ctx)
	if err != nil {
		return fail("reading the asks: %v", err)
	}
	var ask *bus.Ask
	for i := range asks {
		if asks[i].ID == askID {
			ask = &asks[i]
		}
	}
	snippet, err := os.ReadFile(filepath.Join(from, role, "roles.toml"))
	if err != nil {
		return fail("no draft of %s in %s: %v", role, from, err)
	}
	prompt, err := os.ReadFile(filepath.Join(from, role, role+".md"))
	if err != nil {
		return fail("no prompt for %s in %s: %v", role, from, err)
	}
	qid := draftQuestionID(role, snippet, prompt)
	switch {
	case ask == nil:
		return fail("no ask %s", askID)
	case !adoptApproved(*ask, qid):
		return fail("ask %s has no 'adopt' answer from the person to question %s (this draft of %s): nothing adopted; a draft changed since the ask needs a new ask", askID, qid, role)
	}
	if err := draftEntryOnly(string(snippet), role); err != nil {
		return fail("%v", err)
	}
	path, err := manifestPath(env)
	if err != nil {
		return fail("%v", err)
	}
	rolesDir, ok := lookupEnv(env, "ROLES_DIR")
	if !ok || rolesDir == "" {
		rolesDir = filepath.Join(filepath.Dir(path), "roles")
		if repo, ok := lookupEnv(env, "AGENTBUS_REPO"); ok && repo != "" {
			rolesDir = filepath.Join(repo, "roles")
		}
	}
	target := filepath.Join(rolesDir, role+".md")
	if _, err := os.Stat(target); err == nil {
		return fail("%s exists: an adopted role never overwrites a prompt", target)
	}
	base, err := os.ReadFile(path)
	if err != nil {
		return fail("%v", err)
	}
	next := append(append(append([]byte{}, base...), '\n'), snippet...)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".roles-adopt-*.toml")
	if err != nil {
		return fail("%v", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(next); err != nil {
		tmp.Close()
		return fail("%v", err)
	}
	tmp.Close()
	if st, err := os.Stat(path); err == nil { // the manifest keeps its mode
		_ = os.Chmod(tmp.Name(), st.Mode().Perm())
	}
	m, err := rolecfg.Load(tmp.Name())
	if err != nil {
		return fail("the manifest with %s would not load: %v", role, err)
	}
	if _, err := m.Resolve(role); err != nil {
		return fail("%s would not resolve: %v", role, err)
	}
	if err := os.WriteFile(target, prompt, 0o644); err != nil {
		return fail("%v", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(target)
		return fail("%v", err)
	}
	fmt.Fprintf(stdout, "adopted %s (ask %s, answered by %s): %s and %s; pop it when needed (approval = person)\n",
		role, askID, adoptedBy(*ask, qid), path, target)
	return 0
}

func adoptedBy(a bus.Ask, qid string) string {
	for _, q := range a.Questions {
		if q.ID == qid && q.Answer != nil && strings.EqualFold(q.Answer.Choice, "adopt") {
			return q.Answer.By
		}
	}
	return ""
}
