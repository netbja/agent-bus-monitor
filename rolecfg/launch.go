package rolecfg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/netbja/agent-bus-monitor/clientfp"
	"github.com/netbja/agent-bus-monitor/models"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// builtinBinary is the executable each builtin client runs.
var builtinBinary = map[string]string{"claude-code": "claude", "codex": "codex", "kimi-cli": "kimi"}

// repoRoot is the agent-bus-monitor checkout that holds the default skills
// and role prompts (Python's REPO): AGENTBUS_REPO, set by the scripts, else
// the directory of this binary (the scripts run <repo>/agentbus).
func repoRoot(e *envMap) string {
	if r, ok := e.get("AGENTBUS_REPO"); ok && r != "" {
		return r
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(absResolved(exe))
	}
	return "."
}

// envMap is an ordered environment: lookups, edits, and back to KEY=VALUE.
type envMap struct {
	keys []string
	vals map[string]string
}

func newEnv(env []string) *envMap {
	e := &envMap{vals: map[string]string{}}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		e.set(k, v)
	}
	return e
}

func (e *envMap) get(k string) (string, bool) { v, ok := e.vals[k]; return v, ok }

func (e *envMap) or(k, def string) string {
	if v, ok := e.vals[k]; ok {
		return v
	}
	return def
}

func (e *envMap) set(k, v string) {
	if _, ok := e.vals[k]; !ok {
		e.keys = append(e.keys, k)
	}
	e.vals[k] = v
}

func (e *envMap) del(k string) {
	if _, ok := e.vals[k]; !ok {
		return
	}
	delete(e.vals, k)
	for i, x := range e.keys {
		if x == k {
			e.keys = append(e.keys[:i], e.keys[i+1:]...)
			break
		}
	}
}

func (e *envMap) list() []string {
	out := make([]string, 0, len(e.keys))
	for _, k := range e.keys {
		out = append(out, k+"="+e.vals[k])
	}
	return out
}

// shellSafe is Python's shlex safe set: ASCII letters and digits plus @%+=:,./_-.
func shellSafe(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("@%+=:,./_-", c) >= 0:
		default:
			return false
		}
	}
	return true
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if shellSafe(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// ShellJoin is Python's shlex.join: what the dry run prints and the template embeds.
func ShellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}

func homeDir(e *envMap) string {
	if h, ok := e.get("HOME"); ok && h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

// SkillSources resolves skill names to their directories: the repo's skills
// (REPO_SKILLS) first, then the Pocock collection's engineering folder.
func SkillSources(names []string, repo string, env []string) ([]string, error) {
	return skillSources(names, repo, newEnv(env))
}

// skillRoots is where a skill is looked for, in order: the repository's
// skills (REPO_SKILLS), then the external collection's engineering folder —
// POCOCK_SKILLS_ROOT if set, else where setup.sh clones mattpocock/skills
// (${XDG_DATA_HOME:-~/.local/share}/agentbus/skills/mattpocock), else the
// historical ~/Tools/herdr-plugins/skills checkout.
func skillRoots(repo string, e *envMap) []string {
	roots := []string{e.or("REPO_SKILLS", filepath.Join(repo, "skills"))}
	if r, ok := e.get("POCOCK_SKILLS_ROOT"); ok {
		return append(roots, filepath.Join(r, "engineering"))
	}
	data := e.or("XDG_DATA_HOME", filepath.Join(homeDir(e), ".local", "share"))
	return append(roots,
		filepath.Join(data, "agentbus", "skills", "mattpocock", "skills", "engineering"),
		filepath.Join(homeDir(e), "Tools", "herdr-plugins", "skills", "skills", "engineering"))
}

// findSkill returns a skill's resolved directory, or "" when no root has it.
func findSkill(name string, roots []string) string {
	for _, root := range roots {
		if st, err := os.Stat(filepath.Join(root, name, "SKILL.md")); err == nil && st.Mode().IsRegular() {
			return absResolved(filepath.Join(root, name))
		}
	}
	return ""
}

// skillSources resolves every skill or fails: InstallSkills never writes a
// partial set of links.
func skillSources(names []string, repo string, e *envMap) ([]string, error) {
	roots := skillRoots(repo, e)
	sources := make([]string, 0, len(names))
	for _, name := range names {
		found := findSkill(name, roots)
		if found == "" {
			return nil, fmt.Errorf("skill not found in repo or Pocock collection: %s", name)
		}
		sources = append(sources, found)
	}
	return sources, nil
}

// absResolved is Python's Path.resolve(): absolute, symlinks followed when possible.
func absResolved(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

// promptFor returns the role prompt file and the full prompt: the file's text
// after a header naming it, plus (schema 2) the role's skill paths.
func promptFor(p Profile, repo string, e *envMap) (path, prompt string, missing []string, err error) {
	file := filepath.Join(e.or("ROLES_DIR", filepath.Join(repo, "roles")), p.Role+".md")
	st, err := os.Stat(file)
	if err != nil || !st.Mode().IsRegular() {
		return "", "", nil, fmt.Errorf("missing role prompt file: %s", file)
	}
	body, err := os.ReadFile(file)
	if err != nil {
		return "", "", nil, err
	}
	path = absResolved(file)
	prompt = fmt.Sprintf("Role source: %s (resolve its relative links from this directory: %s).\n\n", path, filepath.Dir(path)) + string(body)
	if !p.Legacy {
		roots := skillRoots(repo, e)
		var sources []string
		for _, name := range p.Skills {
			if s := findSkill(name, roots); s != "" {
				sources = append(sources, s)
			} else {
				missing = append(missing, name)
			}
		}
		prompt += "\n\nRead and follow these role skills before work (paths to SKILL.md; resolve their relative links from their own directory):\n"
		lines := make([]string, len(sources))
		for i, s := range sources {
			lines[i] = filepath.Join(s, "SKILL.md")
		}
		prompt += strings.Join(lines, "\n")
		prompt += "\nUse native skill invocation when available, otherwise read the files directly.\n"
	}
	return path, prompt, missing, nil
}

// cacheFile writes text once under the launch cache (0700), named by its
// sha256, with an atomic rename, and returns its path.
func cacheFile(env []string, text, suffix string) (string, error) {
	e := newEnv(env)
	root, ok := e.get("AGENT_LAUNCH_CACHE")
	if !ok {
		root = filepath.Join(e.or("XDG_CACHE_HOME", filepath.Join(homeDir(e), ".cache")), "agentbus/launch")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	// The cached file becomes a client's prompt or spec: a directory others can
	// write to would let them plant one.
	if st, err := os.Stat(root); err != nil {
		return "", err
	} else if st.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("launch cache %s is writable by others; use a private directory (chmod 700)", root)
	}
	sum := sha256.Sum256([]byte(text))
	target := filepath.Join(root, hex.EncodeToString(sum[:])+suffix)
	// Reuse an entry only if it is a regular file whose content still matches
	// its name; a symlink or a tampered file is replaced (rename never follows it).
	if st, err := os.Lstat(target); err == nil && st.Mode().IsRegular() {
		if b, err := os.ReadFile(target); err == nil && sha256.Sum256(b) == sum {
			return target, nil
		}
	}
	tmp, err := os.CreateTemp(root, "tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return target, os.Rename(tmp.Name(), target)
}

// jsonString is Python's json.dumps of one string (codex -c values).
func jsonString(s string) string {
	q, _ := pyJSON(s) // Python json.dumps: ASCII escapes
	return q
}

// build is Python's build(): the client command line. dry replaces the
// prompt and the generated files with placeholders and writes nothing.
func build(p Profile, rules ToolRules, project, prompt, promptPath, resumeID string, dry bool, env []string) ([]string, error) {
	model, opts := p.Model, p.Options
	switch p.Client {
	case "claude-code":
		cmd := []string{"claude", "--model", model}
		if resumeID != "" {
			cmd = append(cmd, "--resume", resumeID)
		}
		if opts["fallback"] != "" {
			cmd = append(cmd, "--fallback-model", opts["fallback"])
		}
		if perm := opts["permission"]; perm != "" && perm != "default" {
			cmd = append(cmd, "--permission-mode", perm)
		}
		settings, err := settingsJSON(rules, resumeHookCommand(newEnv(env)))
		if err != nil {
			return nil, err
		}
		cmd = append(cmd, "--settings", settings)
		if p.Reasoning != "" {
			cmd = append(cmd, "--effort", p.Reasoning)
		}
		cmd = append(cmd, "--name", project+":"+p.Role)
		if dry {
			return append(cmd, "--append-system-prompt-file", promptPath), nil
		}
		return append(cmd, "--append-system-prompt", prompt), nil
	case "codex":
		sandbox, approval := opts["sandbox"], opts["approval"]
		if sandbox == "" {
			sandbox = "read-only"
		}
		if approval == "" {
			approval = "on-request"
		}
		cmd := []string{"codex", "--model", model, "-c", "model_provider=" + jsonString(p.Provider),
			"--sandbox", sandbox, "--ask-for-approval", approval}
		if p.Reasoning != "" {
			cmd = append(cmd, "-c", "model_reasoning_effort="+jsonString(p.Reasoning))
		}
		// The initial prompt is additive: never replace the user's developer instructions.
		if dry {
			return append(cmd, "<role prompt and skill paths>"), nil
		}
		return append(cmd, prompt), nil
	case "kimi-cli":
		body := "---\nname: bus-" + strings.ReplaceAll(p.Role, "_", "-") + "\ndescription: Agent Bus role\n---\n\n${base_prompt}\n\n" + prompt
		file := "<generated agent.md>"
		if !dry {
			var err error
			if file, err = cacheFile(env, body, ".md"); err != nil {
				return nil, err
			}
		}
		cmd := []string{"kimi", "--model", model, "--agent-file", file}
		if perm := opts["permission"]; perm != "" && perm != "default" {
			cmd = append(cmd, "--"+perm)
		}
		return cmd, nil
	}
	file := "<resolved launch.json>"
	if !dry {
		spec, err := adapterSpec(p, project, prompt, promptPath)
		if err != nil {
			return nil, err
		}
		text, err := pyJSON(spec)
		if err != nil {
			return nil, err
		}
		if file, err = cacheFile(env, text, ".json"); err != nil {
			return nil, err
		}
	}
	return []string{p.Adapter, "--spec-file", file}, nil
}

// preflight checks a builtin client's --help for the flags this launch will
// pass, before any cache file is written. --help never starts a session.
func preflight(p Profile, rules ToolRules, resume bool, e *envMap) error {
	binary := builtinBinary[p.Client]
	path, err := lookPath(binary, e.or("PATH", ""))
	if err != nil {
		return fmt.Errorf("client executable missing: %s", binary)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--help")
	cmd.Env = e.list()
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("%s --help failed", binary)
	}
	needed := map[string][]string{
		"claude-code": {"--model", "--append-system-prompt", "--name"},
		"codex":       {"--model", "--config", "--sandbox", "--ask-for-approval"},
		"kimi-cli":    {"--model", "--agent-file"},
	}[p.Client]
	perm := p.Options["permission"]
	if p.Client == "claude-code" {
		if resume {
			needed = append(needed, "--resume")
		}
		if p.Reasoning != "" {
			needed = append(needed, "--effort")
		}
		if p.Options["fallback"] != "" {
			needed = append(needed, "--fallback-model")
		}
		if perm != "" && perm != "default" {
			needed = append(needed, "--permission-mode")
		}
		needed = append(needed, "--settings") // the resume hook, and the tool rules
	}
	if p.Client == "kimi-cli" && perm != "" && perm != "default" {
		needed = append(needed, "--"+perm)
	}
	for _, flag := range needed {
		if !strings.Contains(string(out), flag) {
			return fmt.Errorf("%s lacks required launch flags; inspect its version/help", binary)
		}
	}
	return nil
}

// lookPath is execvpe's search: a name with a slash is used as is, else the
// first executable file on the given PATH.
func lookPath(name, pathEnv string) (string, error) {
	if strings.Contains(name, "/") {
		if st, err := os.Stat(name); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return name, nil
		}
		return "", fmt.Errorf("%s: not executable", name)
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: not found on PATH", name)
}

// Launch is Python's launch(): resolve the role, check the client, build the
// command, bind the pane inside herdr, set the environment and exec the
// client (or print the plan when AGENT_LAUNCH_DRYRUN=1).
// env is the launcher's environment (os.Environ() in production); execFn
// replaces the process (syscall.Exec in production, which keeps the pid).
func Launch(m *Manifest, role, project string, env []string, stdout, stderr io.Writer, execFn func(argv0 string, argv, env []string) error) error {
	return LaunchProfile(m, role, project, "", env, stdout, stderr, execFn)
}

// LaunchProfile is Launch on another profile than the role's (v0.15 §6, the
// swap; "" is the role's own). With AGENT_LAUNCH_REQUIRE_BIND=1 (swap mode) a
// launch that cannot bind its pane fails instead of starting an agent the
// dispatcher cannot verify.
func LaunchProfile(m *Manifest, role, project, profile string, env []string, stdout, stderr io.Writer, execFn func(argv0 string, argv, env []string) error) error {
	return LaunchProfileResume(m, role, project, profile, "", env, stdout, stderr, execFn)
}

// LaunchResume restarts a Claude Code session through the role launcher so
// its permissions, identity, and role environment remain attached.
func LaunchResume(m *Manifest, role, project, resumeID string, env []string, stdout, stderr io.Writer, execFn func(argv0 string, argv, env []string) error) error {
	if resumeID == "" {
		return errors.New("resume session id is required")
	}
	return LaunchProfileResume(m, role, project, "", resumeID, env, stdout, stderr, execFn)
}

// LaunchProfileResume is LaunchProfile with an optional Claude Code session id.
func LaunchProfileResume(m *Manifest, role, project, profile, resumeID string, env []string, stdout, stderr io.Writer, execFn func(argv0 string, argv, env []string) error) error {
	cfg := struct {
		Env            []string
		Stdout, Stderr io.Writer
		Exec           func(argv0 string, argv, env []string) error
	}{env, stdout, stderr, execFn}
	if !namePattern.MatchString(role) || !namePattern.MatchString(project) {
		return errors.New("invalid role or project name")
	}
	if profile != "" && !namePattern.MatchString(profile) {
		return errors.New("invalid profile name")
	}
	if resumeID != "" && !sessionIDPattern.MatchString(resumeID) {
		return errors.New("invalid resume session id")
	}
	p, err := m.ResolveWith(role, profile)
	if err != nil {
		return err
	}
	if p.Tier != "" { // v0.15.2 §10.3: the table's model for this tier, now
		id, err := ResolveTier(p.Provider, p.Tier)
		if err != nil {
			return err
		}
		p.Model = id
	}
	if resumeID != "" && p.Client != "claude-code" {
		return fmt.Errorf("session resume is supported only for Claude Code (role %s uses %s)", role, p.Client)
	}
	e := newEnv(cfg.Env)
	requireBind := e.or("AGENT_LAUNCH_REQUIRE_BIND", "") == "1"
	promptPath, prompt, missing, err := promptFor(p, repoRoot(e), e)
	if err != nil {
		return err
	}
	for _, name := range missing { // a team that does not start is worse than an agent short of a skill
		fmt.Fprintf(cfg.Stderr, "warning: skill %s not found (run ./setup.sh install); %s starts without it\n", name, role)
	}
	rules, err := m.LaunchRules(role)
	if err != nil {
		return err
	}
	if p.Client != "claude-code" && !rules.Empty() { // v0.17 §6: Codex and Kimi have no such rules; their own approval mode applies
		fmt.Fprintf(cfg.Stderr, "note: tool_rules not applied for %s\n", p.Client)
		rules = ToolRules{}
	}
	dry := e.or("AGENT_LAUNCH_DRYRUN", "") == "1"
	if _, builtin := builtinBinary[p.Client]; !dry && !p.Legacy && builtin {
		if err := preflight(p, rules, resumeID != "", e); err != nil {
			return err
		}
	}
	cmd, err := build(p, rules, project, prompt, promptPath, resumeID, dry, cfg.Env)
	if err != nil {
		return err
	}
	if el := elevated(p, rules); el != "" { // a swapped ROLES_TOML must not start an unsandboxed client silently
		fmt.Fprintf(cfg.Stderr, "notice: %s runs %s with elevated %s\n", role, p.Client, el)
	}
	// Inside herdr, bind this process's pid before exec hands it (and its
	// start time) to the client: the dispatcher can then verify the pane even
	// when the client's own commands do not descend from it.
	var bind []string
	pane, _ := e.get("HERDR_PANE_ID")
	if e.or("HERDR_ENV", "") == "1" && pane != "" {
		bin, ok := e.get("AGENTBUS_BIN")
		if !ok {
			if bin, err = os.Executable(); err != nil {
				bin = "agentbus"
			}
		}
		// The pane is the one this launcher started in, read before env_refs apply.
		bind = []string{bin, "--project", project, "bind", role, "--pid", strconv.Itoa(os.Getpid()), "--pane", pane}
	}
	if requireBind && bind == nil {
		return errors.New("AGENT_LAUNCH_REQUIRE_BIND=1 but not inside a herdr pane: nothing to bind")
	}
	if dry {
		if bind != nil {
			fmt.Fprintln(cfg.Stdout, ShellJoin(bind))
		}
		fmt.Fprintln(cfg.Stdout, ShellJoin(cmd))
		if !p.Legacy {
			reasoning := p.Reasoning
			if reasoning == "" {
				reasoning = "client-default"
			}
			fmt.Fprintf(cfg.Stdout, "profile=%s client=%s provider=%s reasoning=%s; skills=%s\n",
				p.Profile, p.Client, p.Provider, reasoning, strings.Join(p.Skills, ","))
		}
		return nil
	}
	for k, v := range map[string]string{"AGENT_BUS_PROJECT": project, "AGENT_BUS_AGENT": role,
		"AGENT_BUS_CLIENT": p.Client, "AGENT_BUS_PROVIDER": p.Provider, "AGENT_BUS_MODEL": p.Model, "AGENT_BUS_MODEL_TIER": p.Tier,
		"AGENT_BUS_PROFILE": p.Profile, "AGENT_BUS_REASONING": p.Reasoning, "ROLES_TOML": p.Manifest} {
		e.set(k, v)
	}
	// A child session must never publish the parent Claude session's transcript as its own.
	e.del("CLAUDE_CODE_SESSION_ID")
	// The API key reaches no client (it bills pay-as-you-go and a prompt could read it)
	// unless AGENT_LAUNCH_KEEP_API_KEY=1, or the profile names it in env_refs below.
	if e.or("AGENT_LAUNCH_KEEP_API_KEY", "") != "1" {
		e.del("ANTHROPIC_API_KEY")
	}
	for _, target := range sortedKeys(p.EnvRefs) {
		source := p.EnvRefs[target]
		v, ok := newEnv(cfg.Env).get(source)
		if !ok {
			return fmt.Errorf("missing environment source for %s", target)
		}
		e.set(target, v)
	}
	// The client is resolved before the bind, so its fingerprint reaches the
	// bind and the agent's own status (client reload spec): what the
	// dispatcher later compares with the client installed then.
	path, err := lookPath(cmd[0], e.or("PATH", ""))
	if err != nil {
		return err
	}
	if fp, err := clientfp.Take(path); err == nil {
		e.set("AGENT_BUS_CLIENT_FP", fp.Encode())
	} else {
		e.del("AGENT_BUS_CLIENT_FP") // never another launch's
	}
	if bind != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		c := exec.CommandContext(ctx, bind[0], bind[1:]...)
		c.Env, c.Stdout, c.Stderr = e.list(), cfg.Stderr, cfg.Stderr
		err := c.Run()
		cancel()
		if err != nil && requireBind { // a swap proves the replacement by this bind
			return fmt.Errorf("agentbus bind failed (AGENT_LAUNCH_REQUIRE_BIND=1): %w", err)
		}
		if err != nil { // an agent the dispatcher cannot wake beats no agent
			fmt.Fprintf(cfg.Stderr, "warning: agentbus bind failed; the dispatcher will not wake %s until it is bound (agentbus --project %s bind %s --pane %s)\n",
				role, project, role, pane)
		}
	}
	return cfg.Exec(path, cmd, e.list())
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// settingsJSON is the --settings value of a Claude Code launch: a role's
// tool rules, {"permissions": {"allow": [...], "ask": [...], "deny": [...]}}
// with empty lists left out (no permissions without rules), then the
// SessionStart hook that reports the resume command (resume.go), in
// Python's json.dumps form like every other generated JSON here.
func settingsJSON(r ToolRules, hook string) (string, error) {
	root := newOrdered()
	if !r.Empty() {
		perms := newOrdered()
		for _, l := range []struct {
			key  string
			list []string
		}{{"allow", r.Allow}, {"ask", r.Ask}, {"deny", r.Deny}} {
			if len(l.list) > 0 {
				perms.set(l.key, l.list)
			}
		}
		root.set("permissions", perms)
	}
	command := newOrdered()
	command.set("type", "command")
	command.set("command", hook)
	matcher := newOrdered()
	matcher.set("hooks", []interface{}{command})
	hooks := newOrdered()
	hooks.set("SessionStart", []interface{}{matcher})
	root.set("hooks", hooks)
	return pyJSON(root)
}

// ordered is a JSON object that keeps its keys in insertion order, as a
// Python dict does.
type ordered struct {
	keys []string
	vals map[string]interface{}
}

func newOrdered() *ordered { return &ordered{vals: map[string]interface{}{}} }

func (o *ordered) set(k string, v interface{}) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// adapterSpec rebuilds Python's spec dict in its key order: the profile's own
// TOML fields as written, then profile, role, legacy, skills and manifest,
// options (in place if the profile has it), adapter, then project, prompt and
// prompt_path. The profile is re-read from the manifest: decoding into maps
// loses TOML order.
func adapterSpec(p Profile, project, prompt, promptPath string) (*ordered, error) {
	var raw map[string]interface{}
	meta, err := toml.DecodeFile(p.Manifest, &raw)
	if err != nil {
		return nil, err
	}
	profiles, _ := raw["profiles"].(map[string]interface{})
	table, _ := profiles[p.Profile].(map[string]interface{})
	prefix := []string{"profiles", p.Profile}
	spec, err := orderedTable(table, meta.Keys(), prefix)
	if err != nil {
		return nil, err
	}
	skills := make([]interface{}, len(p.Skills))
	for i, s := range p.Skills {
		skills[i] = s
	}
	spec.set("profile", p.Profile)
	spec.set("role", p.Role)
	spec.set("legacy", p.Legacy)
	spec.set("skills", skills)
	spec.set("manifest", p.Manifest)
	if _, ok := spec.vals["options"]; !ok {
		spec.set("options", newOrdered())
	}
	spec.set("adapter", p.Adapter)
	spec.set("project", project)
	spec.set("prompt", prompt)
	spec.set("prompt_path", promptPath)
	return spec, nil
}

// orderedTable converts a decoded TOML table to ordered objects, nested
// tables included, using the key order the decoder saw.
func orderedTable(table map[string]interface{}, keys []toml.Key, prefix []string) (*ordered, error) {
	// keys is the decoder's full key list; keep the descendants of prefix.
	var under []toml.Key
	for _, k := range keys {
		if len(k) > len(prefix) && hasPrefix(k, prefix) {
			under = append(under, k)
		}
	}
	return orderTable(table, under, len(prefix))
}

// orderTable orders one table from the keys under it, in the decoder's order
// (depth is the table's own path length). Each child gets the keys beneath
// it, so nested tables and arrays of tables keep their order too.
func orderTable(table map[string]interface{}, under []toml.Key, depth int) (*ordered, error) {
	o := newOrdered()
	for _, k := range under {
		if len(k) != depth+1 {
			continue
		}
		name := k[depth]
		if _, seen := o.vals[name]; seen {
			continue // an array of tables names its key once per element
		}
		v, ok := table[name]
		if !ok {
			continue
		}
		var child []toml.Key
		for _, c := range under {
			if len(c) > depth+1 && c[depth] == name {
				child = append(child, c)
			}
		}
		ov, err := orderValue(v, child, depth+1)
		if err != nil {
			return nil, err
		}
		o.set(name, ov)
	}
	if len(o.keys) != len(table) {
		return nil, fmt.Errorf("cannot recover the key order of a profile table")
	}
	return o, nil
}

// orderValue orders a value from the keys strictly beneath it. The decoder
// lists the keys of an array's tables one element after another, so element
// i takes the next keys until it has as many direct keys as it has fields.
func orderValue(v interface{}, under []toml.Key, depth int) (interface{}, error) {
	switch x := v.(type) {
	case map[string]interface{}:
		return orderTable(x, under, depth)
	case []map[string]interface{}:
		items := make([]interface{}, len(x))
		for i, m := range x {
			items[i] = m
		}
		return orderArray(items, under, depth)
	case []interface{}:
		return orderArray(x, under, depth)
	}
	return v, nil
}

func orderArray(items []interface{}, under []toml.Key, depth int) (interface{}, error) {
	out := make([]interface{}, len(items))
	pos := 0
	for i, item := range items {
		m, isTable := item.(map[string]interface{})
		if !isTable {
			out[i] = item
			continue
		}
		start, direct := pos, 0
		for pos < len(under) {
			if len(under[pos]) == depth+1 {
				if direct == len(m) {
					break // the next element's first key
				}
				direct++
			}
			pos++
		}
		ov, err := orderTable(m, under[start:pos], depth)
		if err != nil {
			return nil, err
		}
		out[i] = ov
	}
	return out, nil
}
func hasPrefix(k toml.Key, prefix []string) bool {
	for i, p := range prefix {
		if k[i] != p {
			return false
		}
	}
	return true
}

// pyJSON is Python's json.dumps with its defaults: ", " and ": " separators,
// ensure_ascii (\uXXXX, surrogate pairs above the BMP), float repr.
func pyJSON(v interface{}) (string, error) {
	var b strings.Builder
	if err := writePyJSON(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

func writePyJSON(b *strings.Builder, v interface{}) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		writePyString(b, x)
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case int:
		b.WriteString(strconv.Itoa(x))
	case float64:
		b.WriteString(pyFloat(x))
	case []interface{}:
		b.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			if err := writePyJSON(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case []string:
		items := make([]interface{}, len(x))
		for i, s := range x {
			items[i] = s
		}
		return writePyJSON(b, items)
	case *ordered:
		b.WriteByte('{')
		for i, k := range x.keys {
			if i > 0 {
				b.WriteString(", ")
			}
			writePyString(b, k)
			b.WriteString(": ")
			if err := writePyJSON(b, x.vals[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("Object of type %T is not JSON serializable", v)
	}
	return nil
}

func writePyString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r >= 0x20 && r < 0x7f:
				b.WriteRune(r)
			case r > 0xffff:
				r -= 0x10000
				fmt.Fprintf(b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			default:
				fmt.Fprintf(b, `\u%04x`, r)
			}
		}
	}
	b.WriteByte('"')
}

// pyFloat is Python's float repr: fixed notation for exponents in [-4, 16),
// always with a fractional part, else 1e+16 style.
func pyFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case math.IsNaN(f):
		return "NaN"
	}
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mant, expStr, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expStr)
	if exp >= -4 && exp < 16 {
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	sign := "+"
	if exp < 0 {
		sign, exp = "-", -exp
	}
	return fmt.Sprintf("%se%s%02d", mant, sign, exp)
}

// elevated names the builtin options that take a client out of its safe
// defaults ("" when none): Claude without permission prompts, Codex with full
// disk access or no approvals, Kimi in yolo mode.
func elevated(p Profile, rules ToolRules) string {
	var parts []string
	switch p.Client {
	case "claude-code":
		if perm := p.Options["permission"]; perm == "bypassPermissions" || perm == "dontAsk" {
			parts = append(parts, "permission="+perm)
		}
		if n := len(rules.Allow); n > 0 { // commands that skip the prompt and the classifier
			parts = append(parts, fmt.Sprintf("allow=%d rules", n))
		}
	case "codex":
		if p.Options["sandbox"] == "danger-full-access" {
			parts = append(parts, "sandbox=danger-full-access")
		}
		if p.Options["approval"] == "never" {
			parts = append(parts, "approval=never")
		}
	case "kimi-cli":
		if p.Options["permission"] == "yolo" {
			parts = append(parts, "permission=yolo")
		}
	}
	return strings.Join(parts, " ")
}

// ClientExecutable is the program a resolved profile runs: the builtin
// client's binary, or the adapter's executable (v0.15 §6.1: a swap checks it
// is there before it closes anything).
func ClientExecutable(p Profile) string {
	if bin, ok := builtinBinary[p.Client]; ok {
		return bin
	}
	return p.Adapter
}

// ResolveTier is the model a tier stands for at provider in the machine's
// model table (agentbus models). Tests replace it.
var ResolveTier = func(provider, tier string) (string, error) {
	t, err := models.Load(models.CachePath())
	if err != nil {
		return "", fmt.Errorf("model table: %w", err)
	}
	r, ok := t.Resolve(provider, tier, time.Now())
	if !ok {
		return "", fmt.Errorf("no %s model known for %s in the model table: run agentbus models --refresh, or set model in the profile", tier, provider)
	}
	return r.ID, nil
}

// ShellSplit is the inverse of ShellJoin: it splits a POSIX-quoted command
// line into words (single quotes, double quotes and backslashes). It runs
// nothing and expands nothing; an unbalanced quote is an error.
func ShellSplit(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		case c == '\'':
			inWord = true
			j := i + 1
			for j < len(rs) && rs[j] != '\'' {
				cur.WriteRune(rs[j])
				j++
			}
			if j == len(rs) {
				return nil, errors.New("unbalanced single quote")
			}
			i = j
		case c == '"':
			inWord = true
			j := i + 1
			for ; j < len(rs) && rs[j] != '"'; j++ {
				if rs[j] == '\\' && j+1 < len(rs) && strings.ContainsRune("\"\\$`", rs[j+1]) {
					j++
				}
				cur.WriteRune(rs[j])
			}
			if j == len(rs) {
				return nil, errors.New("unbalanced double quote")
			}
			i = j
		case c == '\\':
			if i+1 == len(rs) {
				return nil, errors.New("trailing backslash")
			}
			inWord = true
			i++
			cur.WriteRune(rs[i])
		default:
			inWord = true
			cur.WriteRune(c)
		}
	}
	flush()
	return words, nil
}
