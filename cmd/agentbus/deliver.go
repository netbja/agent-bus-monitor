package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/forge"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/plan"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/setup"
)

// `agentbus deliver <branch>` (spec
// docs/superpowers/specs/2026-11-01-forge-delivery-design.md): the approved
// head of a branch, pushed and opened as a pull or merge request on the
// repository's forge, or handed to the person in local mode. Every external
// command goes through deliverDeps.Run; nothing leaves the machine before the
// 4-eyes proof bound to the head holds.

const deliverUsage = "usage: deliver <branch> [--task <plan-task>] [--subject <s>] [--base <b>] [--title <t>] [--dry-run]"

// deliverBodyMax keeps the body under GitHub's 65 536 characters.
const deliverBodyMax = 60000

// glabDescMax keeps glab's description argv under Linux's 128 KiB for one
// argument (MAX_ARG_STRLEN), whatever its runes weigh.
const glabDescMax = 100000

type deliverDeps struct {
	Run      setup.Runner
	Dir      string                   // the repository deliver runs in
	TempDir  string                   // where the body file is written ("" = the system's)
	Setting  func(name string) string // delivery.forge, delivery.base
	Verdicts func(ctx context.Context, subject string) ([]bus.Verdict, error)
	Plan     func(ctx context.Context) (plan.File, bool, error)
	LinkPR   func(ctx context.Context, task string, n int) error
	LinkRef  func(ctx context.Context, task, ref string) error
	// Report tells master what was delivered; nil when master itself runs it.
	Report func(ctx context.Context, msg string) error
	// MayDeliver refuses a caller whose role may not deliver; nil = no check.
	MayDeliver func(self string) error
}

// gitEnvRunner is the real runner for deliver: execRunner without an
// inherited GIT_DIR or GIT_WORK_TREE, so git reads the repository in Dir.
type gitEnvRunner struct{}

func (gitEnvRunner) Run(ctx context.Context, c setup.Cmd) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_DIR=") && !strings.HasPrefix(kv, "GIT_WORK_TREE=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, c.Env...)
	cmd.Dir = c.Dir
	if c.Stdout {
		return cmd.Output()
	}
	return cmd.CombinedOutput()
}

func (gitEnvRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func realDeliverDeps(b *bus.Bus, self, dir string) deliverDeps {
	settings := config.Machine(os.Getenv)
	d := deliverDeps{
		Run: gitEnvRunner{}, Dir: dir,
		Setting:  settings.String,
		Verdicts: b.Verdicts,
		Plan: func(ctx context.Context) (plan.File, bool, error) {
			rec, ok, err := b.Plan(ctx)
			if err != nil || !ok {
				return plan.File{}, ok, err
			}
			f, err := plan.Parse([]byte(rec.File))
			return f, err == nil, err
		},
		LinkPR: b.PlanLink, LinkRef: b.PlanLinkRef,
		MayDeliver: deliverRoleCheck(os.Environ()),
	}
	if self != "master" {
		d.Report = func(ctx context.Context, msg string) error {
			id, err := b.Report(ctx, self, bus.ReportNote, msg)
			if err == nil {
				_, err = b.Cmd(ctx, self, "master", bus.CmdDirective, id, reportNotice(id, msg))
			}
			return err
		}
	}
	return d
}

type deliverOpts struct {
	branch, task, subject, base, title string
	dry                                bool
}

// deliverRoleCheck reads the manifest the launcher used (ROLES_TOML, else
// the repository's): only master and docs deliver (forge-delivery answer 3),
// checked here whatever the command's spelling, beside the tool rules. No
// manifest: no role holds the deny. A manifest that does not load refuses.
// Defence in depth, not a boundary: AGENT_BUS_AGENT is the caller's word.
func deliverRoleCheck(env []string) func(self string) error {
	return func(self string) error {
		path, err := manifestPath(env)
		if err != nil {
			return nil
		}
		if _, err := os.Stat(path); err != nil {
			return nil
		}
		m, err := rolecfg.Load(path)
		if err != nil {
			return fmt.Errorf("cannot read %s to check who delivers: %v", path, err)
		}
		ok, err := m.MayDeliver(self)
		if err != nil {
			return fmt.Errorf("cannot read %s's rules in %s: %v", self, path, err)
		}
		if !ok {
			return fmt.Errorf("%s may not deliver: only master and docs push and open requests (its tool_rules deny agentbus deliver)", self)
		}
		return nil
	}
}

func runDeliver(ctx context.Context, d deliverDeps, self string, args []string, out, errw io.Writer) int {
	if d.MayDeliver != nil {
		if err := d.MayDeliver(self); err != nil {
			fmt.Fprintf(errw, "deliver: %v\n", err)
			return 1
		}
	}
	var o deliverOpts
	args, o.task = extractFlag(args, "--task")
	args, o.subject = extractFlag(args, "--subject")
	args, o.base = extractFlag(args, "--base")
	args, o.title = extractFlag(args, "--title")
	args, o.dry = extractBool(args, "--dry-run")
	if len(args) != 1 {
		fmt.Fprintln(errw, deliverUsage)
		return 1
	}
	o.branch = args[0]
	fail := func(code int, format string, a ...any) int {
		fmt.Fprintf(errw, "deliver: "+format+"\n", a...)
		return code
	}
	if !config.ValidBranchName(o.branch) {
		return fail(1, "%q is not a valid branch name", o.branch)
	}
	if o.base == "" {
		o.base = d.Setting("delivery.base")
	}
	if !config.ValidBranchName(o.base) || o.base == "HEAD" {
		return fail(1, "base %q is not a valid branch name", o.base)
	}
	git := func(args ...string) (string, error) {
		b, err := d.Run.Run(ctx, setup.Cmd{Name: "git", Args: args, Dir: d.Dir})
		return string(b), err
	}

	// 1. The head, resolved once: every later step names H.
	h, err := git("rev-parse", "--verify", "--quiet", "refs/heads/"+o.branch+"^{commit}")
	h = strings.TrimSpace(h)
	if err != nil || !bus.ValidHead(h) {
		return fail(1, "no local branch %s", o.branch)
	}

	// The forge, from the machine's setting and origin.
	setting := d.Setting("delivery.forge")
	originURL, err := git("remote", "get-url", "origin")
	hasOrigin := err == nil && strings.TrimSpace(originURL) != ""
	if !hasOrigin && setting != "auto" && setting != string(forge.Local) {
		return fail(1, "delivery.forge is %s but there is no origin remote", setting)
	}
	f := forge.Resolve(setting, strings.TrimSpace(originURL), hasOrigin)
	if f.Note != "" {
		fmt.Fprintln(errw, f.Note)
	}
	// git push origin honours remote.origin.pushurl: a push target that is
	// not origin's repository would push to one place and open the request
	// on another (reviewer-codex). Compared as host/owner/repo, never printed
	// with user info.
	if f.Kind != forge.Local {
		pushURL, err := git("remote", "get-url", "--push", "origin")
		if err != nil {
			return fail(1, "cannot read origin's push URL")
		}
		fetchAt := forge.Resolve("auto", strings.TrimSpace(originURL), true)
		pushAt := forge.Resolve("auto", strings.TrimSpace(pushURL), true)
		if fetchAt.Host != pushAt.Host || fetchAt.Owner != pushAt.Owner || fetchAt.Repo != pushAt.Repo {
			return fail(1, "origin fetches from %s but pushes to %s (remote.origin.pushurl): deliver cannot tell which forge the request belongs on; make them the same repository",
				placeOf(fetchAt), placeOf(pushAt))
		}
	}

	// 2. Ahead of the base: origin's, fetched, in a forge mode; the local one in local mode.
	baseRef := "refs/heads/" + o.base
	if f.Kind != forge.Local {
		// An explicit refspec: a single-branch clone's or a narrowed fetch
		// refspec would leave origin/<base> absent or stale (foureyes).
		if _, err := git("fetch", "origin", "+refs/heads/"+o.base+":refs/remotes/origin/"+o.base); err != nil {
			return fail(1, "cannot read origin/%s", o.base)
		}
		baseRef = "refs/remotes/origin/" + o.base
	}
	n, err := git("rev-list", "--count", baseRef+".."+h)
	if c, cerr := strconv.Atoi(strings.TrimSpace(n)); err != nil || cerr != nil || c == 0 {
		return fail(1, "%s is not ahead of %s", o.branch, strings.TrimPrefix(strings.TrimPrefix(baseRef, "refs/remotes/"), "refs/heads/"))
	}

	// 3. The 4-eyes proof, bound to H.
	var task *plan.Task
	if o.task != "" && d.Plan != nil {
		if pf, ok, err := d.Plan(ctx); err == nil && ok {
			for i := range pf.Tasks {
				if pf.Tasks[i].ID == o.task {
					task = &pf.Tasks[i]
				}
			}
		}
	}
	subject := o.subject
	if subject == "" && task != nil {
		subject = task.ReviewSubject()
	}
	if subject == "" {
		subject = o.branch
	}
	vs, err := d.Verdicts(ctx, subject)
	if err != nil {
		return fail(1, "cannot read the verdicts on %s: %v", subject, err)
	}
	state, idx := bus.RollUp(vs)
	if state != "APPROVED" || vs[idx].Head != h {
		found := "no approved head"
		if state == "APPROVED" && vs[idx].Head != "" {
			found = "approved head " + vs[idx].Head
		} else if state == "APPROVED" {
			found = "an approval that names no head (verdict --head)"
		}
		return fail(3, "%s is %s with %s; the branch head is %s: nothing delivered", subject, state, found, h)
	}
	approvals := approvalsOn(vs, h)

	// The commits and the body, every line through SafeText.
	log, err := git("log", "--reverse", "--format=%H%x09%s", baseRef+".."+h)
	if err != nil {
		return fail(1, "cannot list the commits of %s", o.branch)
	}
	var commits [][2]string
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		if sha, subj, ok := strings.Cut(line, "\t"); ok {
			commits = append(commits, [2]string{sha, subj})
		}
	}
	title := o.title
	if title == "" && task != nil {
		title = task.Title
	}
	if title == "" && len(commits) > 0 {
		title = commits[0][1]
	}
	title = deliverTitle(title, o.branch)
	body := deliverBody(title, commits, approvals, h)

	link := func(prN int) int {
		if o.task == "" {
			return 0
		}
		var err error
		if prN > 0 {
			err = d.LinkPR(ctx, o.task, prN)
		} else {
			err = d.LinkRef(ctx, o.task, o.branch+"@"+h)
		}
		if err != nil {
			fmt.Fprintf(errw, "deliver: the delivery stands, but the plan link was not recorded: %v\n", err)
			return 2
		}
		return 0
	}
	tell := func(msg string) {
		if d.Report == nil {
			return
		}
		if err := d.Report(ctx, msg); err != nil {
			fmt.Fprintf(errw, "deliver: telling master failed: %v\n", err)
		}
	}

	if o.dry {
		if f.Kind == forge.Local {
			fmt.Fprintf(out, "would hand %s (%s) to the person: git switch %s && git merge --no-ff %s\n", o.branch, h, o.base, h)
		} else {
			fmt.Fprintf(out, "would push %s to origin %s and deliver it on %s to %s\n", h, o.branch, f.Kind, o.base)
		}
		fmt.Fprintf(out, "title: %s\n\n%s", title, body)
		return 0
	}

	// Local: nothing pushed, nothing fetched; the person merges the approved commit.
	if f.Kind == forge.Local {
		code := link(0)
		cmd := fmt.Sprintf("git switch %s && git merge --no-ff %s", o.base, h)
		fmt.Fprintf(out, "delivered %s@%s (local): the person merges with\n  %s\n", o.branch, h, cmd)
		tell(fmt.Sprintf("delivered %s@%s (local mode): the person merges with `%s`", o.branch, h, cmd))
		return code
	}

	// Push the checked commit, no upstream.
	if msg, err := git("push", "origin", h+":refs/heads/"+o.branch); err != nil {
		return fail(1, "push refused: %s", strings.TrimSpace(msg))
	}

	prN, prURL := 0, ""
	switch f.Kind {
	case forge.GitHub:
		prN, prURL = deliverGitHub(ctx, d, o, title, body, errw)
	case forge.GitLab:
		prN, prURL = deliverGitLab(ctx, d, o, title, body, errw)
	}
	if prN > 0 {
		code := link(prN)
		fmt.Fprintf(out, "delivered %s: %s\n", o.branch, prURL)
		tell(fmt.Sprintf("delivered %s@%s: %s", o.branch, h, prURL))
		return code
	}
	// Link mode: the forge's own new-request page.
	page := linkPage(f, o.base, o.branch)
	code := link(0)
	if page == "" {
		fmt.Fprintf(out, "pushed %s@%s; page unknown: origin names no owner and repository, open the request on %s by hand\n", o.branch, h, f.Kind)
		tell(fmt.Sprintf("pushed %s@%s; the request is to open on %s by hand (page unknown)", o.branch, h, f.Kind))
		return code
	}
	fmt.Fprintf(out, "pushed %s@%s; open the request at\n  %s\n", o.branch, h, page)
	tell(fmt.Sprintf("pushed %s@%s; the request is to open at %s", o.branch, h, page))
	return code
}

// placeOf names a remote's repository for a message: host/owner/repo, never
// the URL (it may hold user info).
func placeOf(f forge.Forge) string {
	if f.Host == "" {
		return "a local path"
	}
	return f.Host + "/" + f.Owner + "/" + f.Repo
}

// approvalsOn is every independent approve naming h, oldest first.
func approvalsOn(vs []bus.Verdict, h string) []bus.Verdict {
	var a []bus.Verdict
	for _, v := range vs {
		if v.Decision == "approve" && v.Reviewer != v.Author && v.Head == h {
			a = append(a, v)
		}
	}
	return a
}

// safeLine is s's first line, or [redacted] whole when it fails SafeText.
func safeLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	s = strings.TrimSpace(s)
	if !health.SafeText(s) {
		return health.Redacted
	}
	return s
}

func deliverTitle(t, branch string) string {
	t = safeLine(t)
	if t == "" {
		t = branch
	}
	if r := []rune(t); len(r) > 256 {
		t = string(r[:256])
	}
	return t
}

func deliverBody(title string, commits [][2]string, approvals []bus.Verdict, h string) string {
	var sb strings.Builder
	sb.WriteString(title + "\n\n## Commits\n")
	for _, c := range commits {
		short := c[0]
		if len(short) > 7 {
			short = short[:7]
		}
		fmt.Fprintf(&sb, "- `%s` %s\n", short, safeLine(c[1]))
	}
	sb.WriteString("\n## 4-eyes\n")
	for _, v := range approvals {
		at := time.UnixMilli(v.TS).UTC().Format("2006-01-02 15:04")
		ref := ""
		if v.Ref != "" {
			ref = " (`" + safeLine(v.Ref) + "`)"
		}
		fmt.Fprintf(&sb, "- approved by %s at %s on `%s`%s: %s\n", v.Reviewer, at, h, ref, safeLine(v.Message))
	}
	sb.WriteString("\n🤖 Generated with agent-bus (`agentbus deliver`)\n")
	s := sb.String()
	if r := []rune(s); len(r) > deliverBodyMax {
		s = string(r[:deliverBodyMax])
	}
	return s
}

// cliReady: the forge CLI is on PATH and its own status command exits 0
// (its output is never read).
func cliReady(ctx context.Context, d deliverDeps, name string, errw io.Writer) bool {
	if _, err := d.Run.LookPath(name); err != nil {
		fmt.Fprintf(errw, "%s is not on PATH: link mode (install it, then %s auth login)\n", name, name)
		return false
	}
	if _, err := d.Run.Run(ctx, setup.Cmd{Name: name, Args: []string{"auth", "status"}, Dir: d.Dir}); err != nil {
		fmt.Fprintf(errw, "%s is not logged in: link mode (run %s auth login)\n", name, name)
		return false
	}
	return true
}

func deliverGitHub(ctx context.Context, d deliverDeps, o deliverOpts, title, body string, errw io.Writer) (int, string) {
	if !cliReady(ctx, d, "gh", errw) {
		return 0, ""
	}
	view := func() (int, string) {
		b, err := d.Run.Run(ctx, setup.Cmd{Name: "gh", Args: []string{"pr", "view", o.branch, "--json", "number,url,state"}, Dir: d.Dir, Stdout: true})
		var pr struct {
			Number int    `json:"number"`
			URL    string `json:"url"`
			State  string `json:"state"`
		}
		if err != nil || json.Unmarshal(b, &pr) != nil || pr.State != "OPEN" {
			return 0, ""
		}
		return pr.Number, pr.URL
	}
	if n, u := view(); n > 0 {
		return n, u
	}
	file, err := writeBody(d.TempDir, body)
	if err != nil {
		fmt.Fprintf(errw, "cannot write the body: %v: link mode\n", err)
		return 0, ""
	}
	defer os.Remove(file)
	if _, err := d.Run.Run(ctx, setup.Cmd{Name: "gh", Args: []string{"pr", "create", "--base", o.base, "--head", o.branch,
		"--title", title, "--body-file", file}, Dir: d.Dir}); err != nil {
		fmt.Fprintf(errw, "gh pr create failed (%v): link mode for this run\n", err)
		return 0, ""
	}
	return view()
}

// glab mr create takes the description only as an argument: the one argv
// exception, holding only SafeText lines (spec, "The steps per forge").
func deliverGitLab(ctx context.Context, d deliverDeps, o deliverOpts, title, body string, errw io.Writer) (int, string) {
	if !cliReady(ctx, d, "glab", errw) {
		return 0, ""
	}
	view := func() (int, string) {
		b, err := d.Run.Run(ctx, setup.Cmd{Name: "glab", Args: []string{"mr", "view", o.branch, "--output", "json"}, Dir: d.Dir, Stdout: true})
		var mr struct {
			IID    int    `json:"iid"`
			WebURL string `json:"web_url"`
			State  string `json:"state"`
		}
		if err != nil || json.Unmarshal(b, &mr) != nil || mr.State != "opened" {
			return 0, ""
		}
		return mr.IID, mr.WebURL
	}
	if n, u := view(); n > 0 {
		return n, u
	}
	if _, err := d.Run.Run(ctx, setup.Cmd{Name: "glab", Args: []string{"mr", "create", "--source-branch", o.branch,
		"--target-branch", o.base, "--title", title, "--description", capBytes(body, glabDescMax), "--yes"}, Dir: d.Dir}); err != nil {
		fmt.Fprintf(errw, "glab mr create failed (%v): link mode for this run\n", err)
		return 0, ""
	}
	return view()
}

// capBytes cuts s to at most n bytes on a rune boundary.
func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func writeBody(dir, body string) (string, error) {
	f, err := os.CreateTemp(dir, "agentbus-deliver-*.md")
	if err != nil {
		return "", err
	}
	if err := f.Chmod(0o600); err == nil {
		_, err = f.WriteString(body)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// pathEscape escapes each segment of a branch, keeping its slashes.
func pathEscape(s string) string {
	parts := strings.Split(s, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

// linkPage is the forge's own page for a new request, built from origin
// (never fetched), each part escaped; "" when origin names no owner and
// repository (or no host, for GitLab).
func linkPage(f forge.Forge, base, branch string) string {
	if f.Owner == "" || f.Repo == "" || (f.Kind == forge.GitLab && f.Host == "") {
		return ""
	}
	repo := pathEscape(f.Owner) + "/" + url.PathEscape(f.Repo)
	switch f.Kind {
	case forge.GitHub:
		return "https://github.com/" + repo + "/compare/" + pathEscape(base) + "..." + pathEscape(branch) + "?expand=1"
	case forge.GitLab:
		return "https://" + f.Host + "/" + repo + "/-/merge_requests/new?merge_request%5Bsource_branch%5D=" +
			url.QueryEscape(branch) + "&merge_request%5Btarget_branch%5D=" + url.QueryEscape(base)
	case forge.Bitbucket:
		return "https://bitbucket.org/" + repo + "/pull-requests/new?source=" + url.QueryEscape(branch) + "&dest=" + url.QueryEscape(base)
	}
	return ""
}
