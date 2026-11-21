package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

const dlvH0 = "0000000000000000000000000000000000000001" // an earlier head of the branch

// verdict is one verdict on coder/x by reviewer: claude is coder's
// provider; reviewer-codex is openai's, foureyes claude's.
func verdict(reviewer, decision, head string, ts int64) bus.Verdict {
	prov := map[string]string{"reviewer-codex": "openai", "foureyes": "claude"}[reviewer]
	return bus.Verdict{ID: "v", Subject: "coder/x", Author: "coder", Reviewer: reviewer, Decision: decision,
		Message: "m", Ref: "r", Head: head, TS: ts, AuthorProvider: "claude", ReviewerProvider: prov}
}

// singleWorld: coder/x approved on dlvH by reviewer alone, a 12-line code
// change, the project's single-approval settings as p.
func singleWorld(t *testing.T, p smallPolicy, vs ...bus.Verdict) *deliverWorld {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.verdicts = vs
	w.deps.Policy = func() (smallPolicy, error) { return p, nil }
	return w
}

func setDiff(w *deliverWorld, numstat string) {
	w.r.cmds["git diff --no-renames --numstat -z "+dlvMB+" "+dlvH] = dlvOut{out: numstat}
}

var (
	small      = smallPolicy{Mode: "small", MaxLines: 150, MaxFiles: 5, SameProvider: "off"}
	docsChange = "40\t2\tdocs/a.md\x00"
	codeChange = "10\t2\tcmd/busmon/x.go\x00"
)

func with(p smallPolicy, sp string) smallPolicy { p.SameProvider = sp; return p }

// single-approval §4.1-§4.5, build step 2: with delivery.single_approval =
// small, one approval of another provider on H delivers a small change,
// says so in the body and traces it.
func TestDeliverOneApprovalOfAnotherProvider(t *testing.T) {
	w := singleWorld(t, small, verdict("reviewer-codex", "approve", dlvH, 1))
	code, _, errw := w.run("master", "coder/x")
	if code != 0 || !w.r.did("git push") {
		t.Fatalf("exit %d: %s", code, errw)
	}
	line := "**single approval (small: 12 lines, 1 files; approved by reviewer-codex)**"
	if !strings.Contains(w.r.body, line) {
		t.Errorf("body %q", w.r.body)
	}
	if len(w.traces) != 1 || !strings.Contains(w.traces[0], "single approval (small: 12 lines, 1 files; approved by reviewer-codex)") || !strings.Contains(w.traces[0], dlvH) {
		t.Errorf("traces %q", w.traces)
	}
}

// Every refusal: nothing pushed, exit 3, the reason in the message.
func TestDeliverOneApprovalRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		p    smallPolicy
		diff string
		vs   []bus.Verdict
		want string
	}{
		{"off keeps today's rule", smallPolicy{Mode: "off", MaxLines: 150, MaxFiles: 5, SameProvider: "small"}, codeChange,
			[]bus.Verdict{verdict("reviewer-codex", "approve", dlvH, 1)}, "both reviewers must approve"},
		{"same provider alone, same_provider_ok off", small, codeChange,
			[]bus.Verdict{verdict("foureyes", "approve", dlvH, 1)}, "approved by foureyes, of the author's provider: delivery.same_provider_ok is off"},
		{"the other's reject on an earlier head", small, codeChange,
			[]bus.Verdict{verdict("foureyes", "reject", dlvH0, 1), verdict("reviewer-codex", "approve", dlvH, 2)}, "foureyes's latest verdict on coder/x is a reject"},
		{"the other's reject on H, a later approve", small, codeChange,
			[]bus.Verdict{verdict("foureyes", "reject", dlvH, 1), verdict("reviewer-codex", "approve", dlvH, 2)}, "foureyes's latest verdict on coder/x is a reject"},
		{"a changes verdict blocks too", small, codeChange,
			[]bus.Verdict{verdict("foureyes", "changes", dlvH0, 1), verdict("reviewer-codex", "approve", dlvH, 2)}, "foureyes's latest verdict on coder/x is a reject"},
		{"a push after the approve", small, codeChange,
			[]bus.Verdict{verdict("reviewer-codex", "approve", dlvH0, 1)}, "nothing delivered"},
		{"not small", small, "100\t100\tcmd/busmon/x.go\x00",
			[]bus.Verdict{verdict("reviewer-codex", "approve", dlvH, 1)},
			"coder/x@" + dlvH + " has 1 approval; one is enough only for a small change by a reviewer of another provider: not small (200 lines > 150). Two approvals, or the person's yes (--single-approval)"},
		{"never small", small, "1\t0\tbus/stream.go\x00",
			[]bus.Verdict{verdict("reviewer-codex", "approve", dlvH, 1)}, "not small (bus/stream.go is never small)"},
		{"an added never-small glob", smallPolicy{Mode: "small", MaxLines: 150, MaxFiles: 5, SameProvider: "off", Never: []string{"cmd/busmon/**"}}, codeChange,
			[]bus.Verdict{verdict("reviewer-codex", "approve", dlvH, 1)}, "not small (cmd/busmon/x.go is never small)"},
		{"the project's limits", smallPolicy{Mode: "small", MaxLines: 10, MaxFiles: 5, SameProvider: "off"}, codeChange,
			[]bus.Verdict{verdict("reviewer-codex", "approve", dlvH, 1)}, "not small (12 lines > 10)"},
		// codex approved H, then H2: its latest is H2, so H's current approver is
		// foureyes alone, of the author's provider.
		{"an approver who moved on", small, codeChange,
			[]bus.Verdict{verdict("reviewer-codex", "approve", dlvH, 1), verdict("reviewer-codex", "approve", dlvH0, 2), verdict("foureyes", "approve", dlvH, 3)},
			"approved by foureyes, of the author's provider"},
		{"an empty reviewer provider", small, codeChange,
			[]bus.Verdict{func() bus.Verdict {
				v := verdict("reviewer-codex", "approve", dlvH, 1)
				v.ReviewerProvider = ""
				return v
			}()},
			"approved by reviewer-codex, of an unknown provider: delivery.same_provider_ok is off"},
		{"an empty author provider", small, codeChange,
			[]bus.Verdict{func() bus.Verdict {
				v := verdict("reviewer-codex", "approve", dlvH, 1)
				v.AuthorProvider = ""
				return v
			}()},
			"approved by reviewer-codex, of an unknown provider: delivery.same_provider_ok is off"},
		// §4.8
		{"same_provider_ok docs, a code change", with(small, "docs"), codeChange,
			[]bus.Verdict{verdict("foureyes", "approve", dlvH, 1)}, "delivery.same_provider_ok is docs, and this change is not docs-only"},
		{"same_provider_ok small, single_approval off", smallPolicy{Mode: "off", MaxLines: 150, MaxFiles: 5, SameProvider: "small"}, docsChange,
			[]bus.Verdict{verdict("foureyes", "approve", dlvH, 1)}, "both reviewers must approve"},
		{"same_provider_ok small, never small", with(small, "small"), "1\t0\tskills/a/SKILL.md\x00",
			[]bus.Verdict{verdict("foureyes", "approve", dlvH, 1)}, "not small (skills/a/SKILL.md is never small)"},
		{"same_provider_ok docs, never small", with(small, "docs"), "1\t0\tskills/a/SKILL.md\x00",
			[]bus.Verdict{verdict("foureyes", "approve", dlvH, 1)}, "not small (skills/a/SKILL.md is never small)"},
		{"same_provider_ok docs, docs over the limits", with(small, "docs"), "400\t0\tdocs/a.md\x00",
			[]bus.Verdict{verdict("foureyes", "approve", dlvH, 1)}, "not small (400 lines > 150)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := singleWorld(t, c.p, c.vs...)
			setDiff(w, c.diff)
			code, _, errw := w.run("master", "coder/x")
			if code != 3 || w.r.did("git push") || !strings.Contains(errw, c.want) {
				t.Fatalf("exit %d: %q", code, errw)
			}
			if len(w.traces) != 0 {
				t.Errorf("traced %q", w.traces)
			}
		})
	}
}

// §4.3 reads the reviewers' verdicts: one the author records on its own
// change (reviewer == author) is not an objection.
func TestDeliverOneApprovalIgnoresTheAuthorsOwnVerdict(t *testing.T) {
	self := verdict("reviewer-codex", "reject", dlvH0, 1)
	self.Reviewer = "coder"
	w := singleWorld(t, small, self, verdict("reviewer-codex", "approve", dlvH, 2))
	if code, _, errw := w.run("master", "coder/x"); code != 0 || !strings.Contains(w.r.body, "single approval (small") {
		t.Fatalf("exit %d: %s", code, errw)
	}
}

// §4.8: with same_provider_ok, a same-provider approval delivers its class:
// docs a small docs-only change, small any small change.
func TestDeliverOneApprovalOfTheSameProvider(t *testing.T) {
	for _, c := range []struct {
		sp, diff, line string
	}{
		{"docs", docsChange, "**single approval, same provider (small docs: 42 lines, 1 files; approved by foureyes)**"},
		{"small", docsChange, "**single approval, same provider (small docs: 42 lines, 1 files; approved by foureyes)**"},
		{"small", codeChange, "**single approval, same provider (small: 12 lines, 1 files; approved by foureyes)**"},
	} {
		w := singleWorld(t, with(small, c.sp), verdict("foureyes", "approve", dlvH, 1))
		setDiff(w, c.diff)
		code, _, errw := w.run("master", "coder/x")
		if code != 0 || !strings.Contains(w.r.body, c.line) || len(w.traces) != 1 || !strings.Contains(w.traces[0], strings.Trim(c.line, "*")) {
			t.Fatalf("%s %q: exit %d: %s; body %q; traces %q", c.sp, c.diff, code, errw, w.r.body, w.traces)
		}
	}
}

// Two approvals read no settings and no diff, and say nothing of a single
// approval; the emergency flag still delivers a change that is not small.
func TestDeliverTwoApprovalsAndTheEmergencyUnchanged(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.deps.Policy = func() (smallPolicy, error) { t.Fatal("settings read with two approvals"); return smallPolicy{}, nil }
	if code, _, errw := w.run("master", "coder/x"); code != 0 || strings.Contains(w.r.body, "single approval") || w.r.did("git diff") {
		t.Fatalf("exit %d: %s", code, errw)
	}
	w = singleWorld(t, small, verdict("reviewer-codex", "approve", dlvH, 1))
	setDiff(w, "1\t0\tbus/stream.go\x00")
	code, _, errw := w.run("master", "coder/x", "--single-approval", "the person's yes, ask 12")
	if code != 0 || !strings.Contains(w.r.body, "delivered with one approval: the person's yes, ask 12") || len(w.traces) != 1 {
		t.Fatalf("emergency: exit %d: %s; traces %q", code, errw, w.traces)
	}
}

// --dry-run names the rule that would apply.
func TestDeliverDryRunNamesTheRule(t *testing.T) {
	for _, c := range []struct {
		p    smallPolicy
		vs   []bus.Verdict
		want string
	}{
		{small, []bus.Verdict{verdict("reviewer-codex", "approve", dlvH, 1)}, "approval: single approval (small: 12 lines, 1 files; approved by reviewer-codex)\n"},
		{with(small, "small"), []bus.Verdict{verdict("foureyes", "approve", dlvH, 1)}, "approval: single approval, same provider (small: 12 lines, 1 files; approved by foureyes)\n"},
	} {
		w := singleWorld(t, c.p, c.vs...)
		code, out, errw := w.run("master", "coder/x", "--dry-run")
		if code != 0 || !strings.Contains(out, c.want) || w.r.did("git push") || len(w.traces) != 0 {
			t.Fatalf("exit %d: %q %s", code, out, errw)
		}
	}
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	if _, out, _ := w.run("master", "coder/x", "--dry-run"); !strings.Contains(out, "approval: two approvals (reviewer-codex, foureyes)\n") {
		t.Fatalf("%q", out)
	}
}

// A settings read that fails refuses one approval, never assumes off.
func TestDeliverOneApprovalSettingsUnreadable(t *testing.T) {
	w := singleWorld(t, small, verdict("reviewer-codex", "approve", dlvH, 1))
	w.deps.Policy = func() (smallPolicy, error) { return smallPolicy{}, errExit1 }
	if code, _, errw := w.run("master", "coder/x"); code != 1 || w.r.did("git push") || !strings.Contains(errw, "single-approval settings") {
		t.Fatalf("exit %d: %q", code, errw)
	}
}

// The real settings read: config.toml's [project.<p>.delivery], no env
// variable, another project's values never read, never_small split.
func TestProjectSmallPolicy(t *testing.T) {
	cfg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfg, "agentbus"), 0o700); err != nil {
		t.Fatal(err)
	}
	text := "[project.morning.delivery]\nsingle_approval = \"small\"\nsame_provider_ok = \"docs\"\nsmall_max_lines = 40\nnever_small = [\"roles/**\", \"*.sql\"]\n" +
		"[project.other.delivery]\nsame_provider_ok = \"small\"\nsmall_max_files = 2\n"
	if err := os.WriteFile(filepath.Join(cfg, "agentbus", "config.toml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"XDG_CONFIG_HOME": cfg, "AGENT_BUS_DELIVERY_SINGLE_APPROVAL": "off", "AGENT_BUS_DELIVERY_SAME_PROVIDER_OK": "small"}
	p, err := projectSmallPolicy(func(k string) string { return env[k] }, "morning")
	want := smallPolicy{Mode: "small", MaxLines: 40, MaxFiles: 5, Never: []string{"roles/**", "*.sql"}, SameProvider: "docs"}
	if err != nil || !reflect.DeepEqual(p, want) {
		t.Fatalf("%+v %v", p, err)
	}
	p, _ = projectSmallPolicy(func(k string) string { return env[k] }, "third")
	if !reflect.DeepEqual(p, defaultPolicy) {
		t.Fatalf("third: %+v", p)
	}
	if err := os.WriteFile(filepath.Join(cfg, "agentbus", "config.toml"), []byte("[[[ not toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := projectSmallPolicy(func(k string) string { return env[k] }, "morning"); err == nil {
		t.Fatal("an unreadable config.toml read as the defaults")
	}
}

// foureyes: the real deps wire Policy to this project's settings. Without
// the wiring every deliver test still passes (each injects its own), and
// the person's single_approval would do nothing; wired to another project,
// one project's looser settings would decide another's deliveries.
func TestRealDeliverDepsReadTheProjectsPolicy(t *testing.T) {
	b, r := dialMain(t)
	other, err := bus.Open(r, b.Project()+"x")
	if err != nil {
		t.Fatal(err)
	}
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if err := os.MkdirAll(filepath.Join(cfg, "agentbus"), 0o700); err != nil {
		t.Fatal(err)
	}
	text := "[project." + b.Project() + ".delivery]\nsingle_approval = \"small\"\nsame_provider_ok = \"docs\"\n"
	if err := os.WriteFile(filepath.Join(cfg, "agentbus", "config.toml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	d := realDeliverDeps(b, "master", t.TempDir())
	if d.Policy == nil {
		t.Fatal("Policy not wired")
	}
	if p, err := d.Policy(); err != nil || p.Mode != "small" || p.SameProvider != "docs" {
		t.Fatalf("this project: %+v %v", p, err)
	}
	if p, err := realDeliverDeps(other, "master", t.TempDir()).Policy(); err != nil || p.Mode != "off" || p.SameProvider != "off" {
		t.Fatalf("another project: %+v %v", p, err)
	}
}
