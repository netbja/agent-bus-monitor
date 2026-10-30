package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// draftEnv: a copy of the shipped manifest, the repository's skills only
// (an empty external root), drafts and prompts in temp directories.
func draftEnv(t *testing.T) (env []string, manifest, rolesDir, out string) {
	t.Helper()
	repo, _ := filepath.Abs("../..")
	dir := t.TempDir()
	body, err := os.ReadFile(filepath.Join(repo, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest = filepath.Join(dir, "roles.toml")
	if err := os.WriteFile(manifest, body, 0o644); err != nil {
		t.Fatal(err)
	}
	rolesDir, out = filepath.Join(dir, "roles"), filepath.Join(dir, "drafts")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	env = []string{"ROLES_TOML=" + manifest, "AGENTBUS_REPO=" + repo, "ROLES_DIR=" + rolesDir,
		"POCOCK_SKILLS_ROOT=" + filepath.Join(dir, "none"), "HOME=" + dir}
	return env, manifest, rolesDir, out
}

func TestRolesDraft(t *testing.T) {
	env, manifest, _, out := draftEnv(t)
	task := []string{"keep", "the", "documentation", "current", "and", "open", "the", "pull", "request"}
	var stdout, errw bytes.Buffer
	args := append(append([]string{"draft", "writer", "--task"}, task...), "--out", out)
	if code := runRoles(args, env, &stdout, &errw); code != 0 {
		t.Fatalf("draft = %d: %s", code, errw.String())
	}
	var res struct {
		Role   string                `json:"role"`
		Files  []string              `json:"files"`
		Skills []rolecfg.ScoredSkill `json:"skills"`
		QID    string                `json:"question_id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("output %q: %v", stdout.String(), err)
	}
	if !strings.HasPrefix(res.QID, "adopt-") || len(res.QID) != len("adopt-")+16 {
		t.Fatalf("question id %q", res.QID)
	}
	if res.Role != "writer" || len(res.Skills) < 2 || res.Skills[0].Name != "agent-bus" || res.Skills[1].Name != "agent-bus-docs" {
		t.Fatalf("draft %+v", res)
	}
	snip, err := os.ReadFile(filepath.Join(out, "writer", "roles.toml"))
	if err != nil || !strings.Contains(string(snip), "[roles.writer]") {
		t.Fatalf("snippet %q %v", snip, err)
	}
	if prompt, err := os.ReadFile(filepath.Join(out, "writer", "writer.md")); err != nil || !strings.Contains(string(prompt), "documentation current") {
		t.Fatalf("prompt %v", err)
	}
	if before, _ := os.ReadFile(manifest); strings.Contains(string(before), "writer") {
		t.Fatal("a draft touched the manifest")
	}
	// An existing draft is kept unless --force; an existing role is refused.
	if code := runRoles(args, env, &stdout, &errw); code == 0 {
		t.Fatal("overwrote a draft without --force")
	}
	if code := runRoles(append(args, "--force"), env, &stdout, &errw); code != 0 {
		t.Fatalf("--force = %d: %s", code, errw.String())
	}
	if code := runRoles([]string{"draft", "coder", "--task", "x", "--out", out}, env, &stdout, &errw); code == 0 {
		t.Fatal("drafted an existing role")
	}
}

func TestRolesAdoptNeedsThePersonsAdopt(t *testing.T) {
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "adopt"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _, _ = bus.DeleteProject(ctx, r, b.Project()); _ = r.Close() })

	env, manifest, rolesDir, out := draftEnv(t)
	var stdout, errw bytes.Buffer
	draft := func(role string) string { // the question id the draft binds
		t.Helper()
		stdout.Reset()
		if code := runRoles([]string{"draft", role, "--task", "documentation", "--out", out, "--force"}, env, &stdout, &errw); code != 0 {
			t.Fatalf("draft %s: %s", role, errw.String())
		}
		var res struct {
			QID string `json:"question_id"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		return res.QID
	}
	qid := draft("writer")
	askText := func(q, text string) string {
		id, _, err := b.Ask(ctx, bus.AskDef{From: "master", Title: "Adopt the drafted role?", Questions: []bus.AskQuestion{{
			ID: q, Header: "Role", Question: text,
			Options: []bus.AskOption{{Label: "adopt"}, {Label: "change"}, {Label: "discard"}}}}}, "")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ask := func() string { return askText(qid, "Adopt the drafted role writer ("+qid+")?") }
	adoptRole := func(role, id string) int {
		return runAdopt(ctx, b, "master", []string{role, "--ask", id, "--from", out}, env, &stdout, &errw)
	}
	adopt := func(id string) int { return adoptRole("writer", id) }
	unchanged := func(label string) {
		t.Helper()
		body, _ := os.ReadFile(manifest)
		if strings.Contains(string(body), "[roles.writer]") {
			t.Fatalf("%s: the manifest was changed", label)
		}
		if _, err := os.Stat(filepath.Join(rolesDir, "writer.md")); err == nil {
			t.Fatalf("%s: the prompt was written", label)
		}
	}

	if adopt("1-0") == 0 {
		t.Fatal("adopted with an unknown ask")
	}
	unchanged("unknown ask")

	open := ask()
	if adopt(open) == 0 {
		t.Fatal("adopted with an open ask")
	}
	unchanged("open ask")
	if _, _, err := b.Answer(ctx, open, qid, "charlie", "discard", ""); err != nil {
		t.Fatal(err)
	}
	if adopt(open) == 0 {
		t.Fatal("adopted on discard")
	}
	unchanged("discard")

	self := ask()
	if _, _, err := b.Answer(ctx, self, qid, "master", "adopt", ""); err != nil {
		t.Fatal(err)
	}
	if adopt(self) == 0 {
		t.Fatal("adopted on the asking agent's own answer")
	}
	unchanged("self answer")

	yes := ask()
	if _, _, err := b.Answer(ctx, yes, qid, "charlie", "adopt", ""); err != nil {
		t.Fatal(err)
	}
	// The person must see what they approve: a question that does not name
	// the role and the draft's id is not enough, even with the right id.
	vague := askText(qid, "Adopt it?")
	if _, _, err := b.Answer(ctx, vague, qid, "charlie", "adopt", ""); err != nil {
		t.Fatal(err)
	}
	if adopt(vague) == 0 {
		t.Fatal("adopted on a question that names neither the role nor the draft")
	}
	unchanged("vague question")

	// The answer is bound to the role and to the draft it was asked about.
	draft("reader")
	if adoptRole("reader", yes) == 0 {
		t.Fatal("adopted another role on writer's answer")
	}
	snipPath := filepath.Join(out, "writer", "roles.toml")
	orig, _ := os.ReadFile(snipPath)
	if err := os.WriteFile(snipPath, []byte(strings.Replace(string(orig), "tier = \"pop\"", "tier = \"pop\"\npops = true", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if adopt(yes) == 0 {
		t.Fatal("adopted a draft edited after the answer")
	}
	if err := os.WriteFile(snipPath, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	unchanged("edited draft")

	// An existing prompt is never overwritten, and nothing is written then.
	if err := os.WriteFile(filepath.Join(rolesDir, "writer.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if adopt(yes) == 0 {
		t.Fatal("adopted over an existing prompt")
	}
	if body, _ := os.ReadFile(manifest); strings.Contains(string(body), "[roles.writer]") {
		t.Fatal("the manifest changed although the prompt was refused")
	}
	os.Remove(filepath.Join(rolesDir, "writer.md"))

	if code := adopt(yes); code != 0 {
		t.Fatalf("adopt = %d: %s", code, errw.String())
	}
	m, err := rolecfg.Load(manifest)
	if err != nil {
		t.Fatalf("the adopted manifest does not load: %v", err)
	}
	if _, err := m.Resolve("writer"); err != nil {
		t.Fatalf("writer: %v", err)
	}
	if p, err := os.ReadFile(filepath.Join(rolesDir, "writer.md")); err != nil || !strings.Contains(string(p), "# Role: writer") {
		t.Fatalf("prompt %v", err)
	}
	if adopt(yes) == 0 {
		t.Fatal("adopted twice")
	}
}

// A draft whose entry defines anything but [roles.<role>] is refused, even
// with a matching answer.
func TestAdoptRefusesAnExtraTable(t *testing.T) {
	for _, snippet := range []string{
		"[roles.writer]\ntier = \"pop\"\nskills = [\"agent-bus\"]\n[roles.other]\ntier = \"pop\"\nskills = [\"agent-bus\"]\n",
		"[profiles.evil]\nclient = \"claude-code\"\n[roles.writer]\ntier = \"pop\"\nskills = [\"agent-bus\"]\n",
		"pops = true\n[roles.writer]\ntier = \"pop\"\n",
	} {
		if err := draftEntryOnly(snippet, "writer"); err == nil {
			t.Errorf("accepted:\n%s", snippet)
		}
	}
	if err := draftEntryOnly("# a comment\n[roles.writer]\nprofile = \"x\"\ntier = \"pop\"\napproval = \"person\"\nskills = [\"agent-bus\"]\n", "writer"); err != nil {
		t.Fatalf("refused a plain entry: %v", err)
	}
}
