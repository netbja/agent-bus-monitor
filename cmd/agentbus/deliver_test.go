package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
	"github.com/netbja/agent-bus-monitor/setup"
)

const (
	dlvH   = "1111111111111111111111111111111111111111"
	dlvOld = "2222222222222222222222222222222222222222"
)

// deliverRunner answers only the commands a test expects (keys "name
// arg...", a --body-file path written <file>); any other fails the test.
type deliverRunner struct {
	t     *testing.T
	paths map[string]string
	cmds  map[string]dlvOut
	outs  map[string][]dlvOut // successive answers, overriding cmds
	ran   []string
	cmd   []setup.Cmd
	body  string      // the body file's content when gh read it
	mode  os.FileMode // and its mode
	file  string
}

type dlvOut struct {
	out string
	err error
}

func (f *deliverRunner) LookPath(name string) (string, error) {
	if p, ok := f.paths[name]; ok {
		return p, nil
	}
	return "", errors.New("not found")
}

func (f *deliverRunner) Run(_ context.Context, c setup.Cmd) ([]byte, error) {
	args := slices.Clone(c.Args)
	for i := range args {
		if i > 0 && args[i-1] == "--body-file" {
			f.file = args[i]
			if b, err := os.ReadFile(args[i]); err == nil {
				f.body = string(b)
			}
			if st, err := os.Stat(args[i]); err == nil {
				f.mode = st.Mode().Perm()
			}
			args[i] = "<file>"
		}
	}
	key := strings.Join(append([]string{c.Name}, args...), " ")
	f.ran = append(f.ran, key)
	f.cmd = append(f.cmd, c)
	r, ok := f.cmds[key]
	if q := f.outs[key]; len(q) > 0 {
		r, ok, f.outs[key] = q[0], true, q[1:]
	}
	if !ok {
		f.t.Fatalf("unexpected command: %s", key)
	}
	return []byte(r.out), r.err
}

func (f *deliverRunner) did(prefix string) bool {
	for _, k := range f.ran {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

type deliverWorld struct {
	r        *deliverRunner
	deps     deliverDeps
	verdicts []bus.Verdict
	plan     *plan.File
	settings map[string]string
	linkedPR map[string]int
	linkRef  map[string]string
	linkErr  error
	reports  []string
	subject  string
}

var errExit1 = errors.New("exit status 1")

// newDeliverWorld: branch coder/x at dlvH, two commits ahead of origin's main,
// origin on GitHub, approved on dlvH by foureyes, gh present and logged in.
func newDeliverWorld(t *testing.T, origin string) *deliverWorld {
	w := &deliverWorld{
		settings: map[string]string{"delivery.forge": "auto", "delivery.base": "main"},
		linkedPR: map[string]int{}, linkRef: map[string]string{},
		verdicts: []bus.Verdict{{ID: "1-0", Subject: "coder/x", Author: "coder", Reviewer: "foureyes",
			Decision: "approve", Message: "looks right\nmore", Ref: "dml1", Head: dlvH, TS: 1793600000000}},
	}
	w.r = &deliverRunner{t: t, paths: map[string]string{"gh": "/bin/gh", "glab": "/bin/glab"}, outs: map[string][]dlvOut{},
		cmds: map[string]dlvOut{
			"git rev-parse --verify --quiet refs/heads/coder/x^{commit}":            {out: dlvH + "\n"},
			"git remote get-url origin":                                             {out: origin + "\n"},
			"git fetch origin +refs/heads/main:refs/remotes/origin/main":            {},
			"git rev-list --count refs/remotes/origin/main.." + dlvH:                {out: "2\n"},
			"git log --reverse --format=%H%x09%s refs/remotes/origin/main.." + dlvH: {out: "aaaaaaaa1\tfirst change\n" + dlvH + "\tsecond change\n"},
			"git rev-list --count refs/heads/main.." + dlvH:                         {out: "2\n"},
			"git log --reverse --format=%H%x09%s refs/heads/main.." + dlvH:          {out: "aaaaaaaa1\tfirst change\n" + dlvH + "\tsecond change\n"},
			"git push origin " + dlvH + ":refs/heads/coder/x":                       {},
			"gh auth status": {},
			"gh pr create --base main --head coder/x --title first change --body-file <file>": {out: "https://github.com/o/r/pull/12\n"},
			"glab auth status": {},
		}}
	w.r.outs["gh pr view coder/x --json number,url,state"] = []dlvOut{{err: errExit1},
		{out: `{"number":12,"url":"https://github.com/o/r/pull/12","state":"OPEN"}`}}
	w.deps = deliverDeps{
		Run: w.r, Dir: t.TempDir(), TempDir: t.TempDir(),
		Setting: func(n string) string { return w.settings[n] },
		Verdicts: func(_ context.Context, s string) ([]bus.Verdict, error) {
			w.subject = s
			return w.verdicts, nil
		},
		Plan: func(context.Context) (plan.File, bool, error) {
			if w.plan == nil {
				return plan.File{}, false, nil
			}
			return *w.plan, true, nil
		},
		LinkPR:  func(_ context.Context, task string, n int) error { w.linkedPR[task] = n; return w.linkErr },
		LinkRef: func(_ context.Context, task, ref string) error { w.linkRef[task] = ref; return w.linkErr },
	}
	return w
}

func (w *deliverWorld) run(self string, args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	code := runDeliver(context.Background(), w.deps, self, args, &out, &errw)
	return code, out.String(), errw.String()
}

func TestDeliverGitHubOpensAPullRequestOnTheApprovedHead(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	code, out, errw := w.run("master", "coder/x", "--task", "t1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	want := []string{
		"git rev-parse --verify --quiet refs/heads/coder/x^{commit}",
		"git remote get-url origin",
		"git fetch origin +refs/heads/main:refs/remotes/origin/main",
		"git rev-list --count refs/remotes/origin/main.." + dlvH,
		"git log --reverse --format=%H%x09%s refs/remotes/origin/main.." + dlvH,
		"git push origin " + dlvH + ":refs/heads/coder/x",
		"gh auth status",
		"gh pr view coder/x --json number,url,state",
		"gh pr create --base main --head coder/x --title first change --body-file <file>",
		"gh pr view coder/x --json number,url,state",
	}
	if !slices.Equal(w.r.ran, want) {
		t.Fatalf("commands:\n%s\nwant:\n%s", strings.Join(w.r.ran, "\n"), strings.Join(want, "\n"))
	}
	if w.linkedPR["t1"] != 12 {
		t.Errorf("plan link = %v, want t1 -> 12", w.linkedPR)
	}
	if !strings.Contains(out, "delivered coder/x: https://github.com/o/r/pull/12") {
		t.Errorf("stdout = %q", out)
	}
	if w.r.mode != 0o600 {
		t.Errorf("body file mode %v, want 0600", w.r.mode)
	}
	if _, err := os.Stat(w.r.file); !os.IsNotExist(err) {
		t.Errorf("body file %s left behind", w.r.file)
	}
	for _, s := range []string{"first change\n", "## Commits", "- `aaaaaaa` first change", "- `1111111` second change",
		"## 4-eyes", "approved by foureyes", "on `" + dlvH + "` (`dml1`): looks right", "agentbus deliver"} {
		if !strings.Contains(w.r.body, s) {
			t.Errorf("body lacks %q:\n%s", s, w.r.body)
		}
	}
	if strings.Contains(w.r.body, "more") {
		t.Errorf("body carries more than a verdict's first line:\n%s", w.r.body)
	}
}

// Every refusal of the 4-eyes proof happens before anything is pushed.
func TestDeliverRefusesWithoutAnApprovalOnTheHead(t *testing.T) {
	ap := func(rev, head string) bus.Verdict {
		return bus.Verdict{Subject: "coder/x", Author: "coder", Reviewer: rev, Decision: "approve", Head: head}
	}
	rej := bus.Verdict{Subject: "coder/x", Author: "coder", Reviewer: "foureyes", Decision: "reject"}
	cases := map[string][]bus.Verdict{
		"none": nil,
		// A commit added after the approval, whatever its committer date
		// says: its SHA is not the approved one.
		"approved on an older head": {ap("foureyes", dlvOld)},
		"approved without --head":   {ap("foureyes", "")},
		"a self-approval only":      {ap("coder", dlvH)},
		"rejected after":            {ap("foureyes", dlvH), rej},
		"newest approve on another": {ap("foureyes", dlvH), ap("reviewer-codex", dlvOld)},
	}
	for name, vs := range cases {
		t.Run(name, func(t *testing.T) {
			w := newDeliverWorld(t, "git@github.com:o/r.git")
			w.verdicts = vs
			code, _, errw := w.run("master", "coder/x")
			if code != 3 {
				t.Fatalf("exit %d, want 3: %s", code, errw)
			}
			if w.r.did("git push") || w.r.did("gh ") {
				t.Errorf("ran %v", w.r.ran)
			}
			if !strings.Contains(errw, "coder/x") || !strings.Contains(errw, dlvH) {
				t.Errorf("refusal names neither subject nor head: %q", errw)
			}
		})
	}
}

func TestDeliverRefusesABranchNotAheadOfOriginsBase(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.r.cmds["git rev-list --count refs/remotes/origin/main.."+dlvH] = dlvOut{out: "0\n"}
	if code, _, errw := w.run("master", "coder/x"); code != 1 || !strings.Contains(errw, "not ahead") {
		t.Fatalf("exit %d: %q", code, errw)
	}
	if w.r.did("git push") {
		t.Error("pushed")
	}
}

func TestDeliverRefusesAnAbsentBranch(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.r.cmds["git rev-parse --verify --quiet refs/heads/coder/x^{commit}"] = dlvOut{err: errExit1}
	if code, _, _ := w.run("master", "coder/x"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if len(w.r.ran) != 1 {
		t.Errorf("ran %v", w.r.ran)
	}
}

func TestDeliverStopsWhenTheBaseCannotBeFetched(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.r.cmds["git fetch origin +refs/heads/main:refs/remotes/origin/main"] = dlvOut{err: errExit1}
	if code, _, errw := w.run("master", "coder/x"); code != 1 || !strings.Contains(errw, "cannot read origin/main") {
		t.Fatalf("exit %d: %q", code, errw)
	}
}

func TestDeliverReusesAnOpenPullRequest(t *testing.T) {
	w := newDeliverWorld(t, "https://github.com/o/r")
	w.r.outs["gh pr view coder/x --json number,url,state"] = []dlvOut{{out: `{"number":7,"url":"https://github.com/o/r/pull/7","state":"OPEN"}`}}
	code, out, errw := w.run("master", "coder/x", "--task", "t1")
	if code != 0 || w.r.did("gh pr create") || w.linkedPR["t1"] != 7 || !strings.Contains(out, "pull/7") {
		t.Fatalf("exit %d, ran %v, links %v, out %q, err %q", code, w.r.ran, w.linkedPR, out, errw)
	}
}

func TestDeliverLinkModeWhenGhIsAbsentOrLoggedOut(t *testing.T) {
	for name, set := range map[string]func(w *deliverWorld){
		"absent":     func(w *deliverWorld) { delete(w.r.paths, "gh") },
		"logged out": func(w *deliverWorld) { w.r.cmds["gh auth status"] = dlvOut{err: errExit1} },
		"create fails": func(w *deliverWorld) {
			w.r.cmds["gh pr create --base main --head coder/x --title first change --body-file <file>"] = dlvOut{err: errExit1}
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newDeliverWorld(t, "git@github.com:o/r.git")
			set(w)
			code, out, errw := w.run("master", "coder/x", "--task", "t1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errw)
			}
			if !w.r.did("git push origin " + dlvH) {
				t.Errorf("not pushed: %v", w.r.ran)
			}
			if !strings.Contains(out, "https://github.com/o/r/compare/main...coder/x?expand=1") {
				t.Errorf("no page: %q", out)
			}
			if w.linkRef["t1"] != "coder/x@"+dlvH {
				t.Errorf("link %v", w.linkRef)
			}
			if name != "create fails" && !strings.Contains(errw, "gh auth login") {
				t.Errorf("no login hint: %q", errw)
			}
		})
	}
}

func TestDeliverGitLabOpensAMergeRequest(t *testing.T) {
	w := newDeliverWorld(t, "git@gitlab.example.org:grp/sub/r.git")
	w.r.outs["glab mr view coder/x --output json"] = []dlvOut{{err: errExit1},
		{out: `{"iid":4,"web_url":"https://gitlab.example.org/grp/sub/r/-/merge_requests/4","state":"opened"}`}}
	w.r.cmds["glab mr create --source-branch coder/x --target-branch main --title first change --description "+
		"BODY --yes"] = dlvOut{}
	// The description is an argv: match it whatever its text.
	r := w.r
	w.deps.Run = descRunner{r}
	code, out, errw := w.run("master", "coder/x", "--task", "t1")
	if code != 0 {
		t.Fatalf("exit %d: %s (ran %v)", code, errw, r.ran)
	}
	if w.linkedPR["t1"] != 4 || !strings.Contains(out, "merge_requests/4") {
		t.Errorf("links %v, out %q", w.linkedPR, out)
	}
	if r.did("gh ") {
		t.Errorf("ran gh: %v", r.ran)
	}
}

// descRunner replaces glab's --description value with BODY before matching.
type descRunner struct{ *deliverRunner }

func (d descRunner) Run(ctx context.Context, c setup.Cmd) ([]byte, error) {
	args := slices.Clone(c.Args)
	for i := range args {
		if i > 0 && args[i-1] == "--description" {
			if !strings.Contains(args[i], "## 4-eyes") {
				d.t.Errorf("description %q", args[i])
			}
			args[i] = "BODY"
		}
	}
	c.Args = args
	return d.deliverRunner.Run(ctx, c)
}

func TestDeliverBitbucketRunsNoCLI(t *testing.T) {
	w := newDeliverWorld(t, "git@bitbucket.org:o/r.git")
	w.r.paths["bb"] = "/bin/bb"
	code, out, errw := w.run("master", "coder/x", "--task", "t1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	for _, k := range w.r.ran {
		if !strings.HasPrefix(k, "git ") {
			t.Errorf("ran %s", k)
		}
	}
	if !strings.Contains(out, "https://bitbucket.org/o/r/pull-requests/new?source=coder%2Fx&dest=main") {
		t.Errorf("page: %q", out)
	}
	if w.linkRef["t1"] != "coder/x@"+dlvH {
		t.Errorf("link %v", w.linkRef)
	}
}

func TestDeliverLocalPushesAndFetchesNothing(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.settings["delivery.forge"] = "local"
	code, out, errw := w.run("master", "coder/x", "--task", "t1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	if w.r.did("git push") || w.r.did("git fetch") || w.r.did("gh ") {
		t.Errorf("ran %v", w.r.ran)
	}
	if !w.r.did("git rev-list --count refs/heads/main..") {
		t.Errorf("not checked against the local base: %v", w.r.ran)
	}
	if !strings.Contains(out, "git switch main && git merge --no-ff "+dlvH) {
		t.Errorf("out %q", out)
	}
	if w.linkRef["t1"] != "coder/x@"+dlvH {
		t.Errorf("link %v", w.linkRef)
	}
}

func TestDeliverRefusesASetForgeWithoutOrigin(t *testing.T) {
	w := newDeliverWorld(t, "")
	w.settings["delivery.forge"] = "gitlab"
	w.r.cmds["git remote get-url origin"] = dlvOut{err: errExit1}
	if code, _, errw := w.run("master", "coder/x"); code != 1 || !strings.Contains(errw, "delivery.forge is gitlab but there is no origin remote") {
		t.Fatalf("exit %d: %q", code, errw)
	}
}

func TestDeliverAutoWithoutOriginIsLocal(t *testing.T) {
	w := newDeliverWorld(t, "")
	w.r.cmds["git remote get-url origin"] = dlvOut{err: errExit1}
	if code, out, errw := w.run("master", "coder/x"); code != 0 || !strings.Contains(out, "git merge --no-ff") {
		t.Fatalf("exit %d: %q %q", code, out, errw)
	}
}

func TestDeliverRecordsNothingWhenThePushIsRefused(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.r.cmds["git push origin "+dlvH+":refs/heads/coder/x"] = dlvOut{out: "! [rejected] non-fast-forward", err: errExit1}
	code, _, errw := w.run("master", "coder/x", "--task", "t1")
	if code != 1 || !strings.Contains(errw, "non-fast-forward") {
		t.Fatalf("exit %d: %q", code, errw)
	}
	if len(w.linkedPR)+len(w.linkRef) != 0 || w.r.did("gh ") {
		t.Errorf("recorded or opened after a refused push: %v %v %v", w.linkedPR, w.linkRef, w.r.ran)
	}
}

func TestDeliverStandsWhenThePlanLinkFails(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.linkErr = errors.New("plan: no task t1")
	code, out, errw := w.run("master", "coder/x", "--task", "t1")
	if code != 2 || !strings.Contains(out, "delivered coder/x") || !strings.Contains(errw, "not recorded") {
		t.Fatalf("exit %d: %q %q", code, out, errw)
	}
}

func TestDeliverReadsSubjectAndTitleFromThePlanTask(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.plan = &plan.File{Tasks: []plan.Task{{ID: "t1", Title: "Forge delivery", Review: "forge-delivery"}}}
	w.verdicts[0].Subject = "forge-delivery"
	w.r.cmds["gh pr create --base main --head coder/x --title Forge delivery --body-file <file>"] = dlvOut{}
	if code, _, errw := w.run("master", "coder/x", "--task", "t1"); code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	if w.subject != "forge-delivery" || !strings.HasPrefix(w.r.body, "Forge delivery\n") {
		t.Errorf("subject %q, body %q", w.subject, w.r.body)
	}
}

func TestDeliverDryRunPushesNothing(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	code, out, errw := w.run("master", "coder/x", "--task", "t1", "--dry-run")
	if code != 0 || w.r.did("git push") || w.r.did("gh ") || len(w.linkedPR)+len(w.linkRef) != 0 {
		t.Fatalf("exit %d, ran %v: %s", code, w.r.ran, errw)
	}
	if !strings.Contains(out, "would push "+dlvH) {
		t.Errorf("out %q", out)
	}
}

func TestDeliverByTheDocsAgentReportsToMaster(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.deps.Report = func(_ context.Context, msg string) error { w.reports = append(w.reports, msg); return nil }
	if code, _, errw := w.run("docs", "coder/x"); code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	if len(w.reports) != 1 || !strings.Contains(w.reports[0], "https://github.com/o/r/pull/12") {
		t.Errorf("reports %v", w.reports)
	}
}

// Deliver never reads, passes or prints a token: the CLI reads its own.
func TestDeliverNeverCarriesAToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "tok-SECRET")
	t.Setenv("GITLAB_TOKEN", "tok-SECRET")
	w := newDeliverWorld(t, "https://user:tok-SECRET@github.com/o/r.git")
	code, out, errw := w.run("master", "coder/x")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	for _, c := range w.r.cmd {
		if strings.Contains(strings.Join(append(c.Args, c.Env...), " "), "tok-SECRET") {
			t.Errorf("%s carries the token", c.Name)
		}
	}
	if strings.Contains(out+errw+w.r.body, "tok-SECRET") {
		t.Error("token printed")
	}
}

// A verdict line that looks like a credential reaches the forge redacted.
func TestDeliverRedactsACredentialShapedLine(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:o/r.git")
	w.verdicts[0].Message = "ok, key ghp_" + strings.Repeat("a", 36)
	if code, _, errw := w.run("master", "coder/x"); code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	if strings.Contains(w.r.body, "ghp_") || !strings.Contains(w.r.body, "[redacted]") {
		t.Errorf("body %q", w.r.body)
	}
}

// A multibyte body stays under Linux's one-argument limit on glab's argv.
func TestDeliverCapsTheGitLabDescriptionInBytes(t *testing.T) {
	long := strings.Repeat("é", 40000)
	b := capBytes("x"+long+long, glabDescMax)
	if len(b) > glabDescMax || !utf8.ValidString(b) {
		t.Fatalf("len %d, valid %v", len(b), utf8.ValidString(b))
	}
}

// A set forge with an origin that names no owner or repository has no page to print.
func TestDeliverSaysWhenThePageIsUnknown(t *testing.T) {
	w := newDeliverWorld(t, "git@github.com:r")
	w.settings["delivery.forge"] = "github"
	delete(w.r.paths, "gh")
	code, out, errw := w.run("master", "coder/x")
	if code != 0 || strings.Contains(out, "github.com//") || !strings.Contains(out, "page unknown") {
		t.Fatalf("exit %d: %q %q", code, out, errw)
	}
}
