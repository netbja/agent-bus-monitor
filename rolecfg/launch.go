package rolecfg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// builtinBinary is the executable each builtin client runs.
var builtinBinary = map[string]string{"claude-code": "claude", "codex": "codex", "kimi-cli": "kimi"}

// LaunchConfig is what Launch reads from its surroundings, injected for tests.
// Repo is the agent-bus-monitor checkout (default skills and role prompts);
// Env is the launcher's environment (os.Environ() in production); Exec
// replaces the process (syscall.Exec in production, which keeps the pid).
type LaunchConfig struct {
	Repo           string
	Env            []string
	Stdout, Stderr io.Writer
	Exec           func(argv0 string, argv, env []string) error
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

func skillSources(names []string, repo string, e *envMap) ([]string, error) {
	roots := []string{
		e.or("REPO_SKILLS", filepath.Join(repo, "skills")),
		filepath.Join(e.or("POCOCK_SKILLS_ROOT", filepath.Join(homeDir(e), "Tools/herdr-plugins/skills/skills")), "engineering"),
	}
	sources := make([]string, 0, len(names))
	for _, name := range names {
		found := ""
		for _, root := range roots {
			if st, err := os.Stat(filepath.Join(root, name, "SKILL.md")); err == nil && st.Mode().IsRegular() {
				found = filepath.Join(root, name)
				break
			}
		}
		if found == "" {
			return nil, fmt.Errorf("skill not found in repo or Pocock collection: %s", name)
		}
		sources = append(sources, absResolved(found))
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
func promptFor(p Profile, repo string, e *envMap) (string, string, error) {
	file := filepath.Join(e.or("ROLES_DIR", filepath.Join(repo, "roles")), p.Role+".md")
	st, err := os.Stat(file)
	if err != nil || !st.Mode().IsRegular() {
		return "", "", fmt.Errorf("missing role prompt file: %s", file)
	}
	body, err := os.ReadFile(file)
	if err != nil {
		return "", "", err
	}
	path := absResolved(file)
	prompt := fmt.Sprintf("Role source: %s (resolve its relative links from this directory: %s).\n\n", path, filepath.Dir(path)) + string(body)
	if !p.Legacy {
		sources, err := skillSources(p.Skills, repo, e)
		if err != nil {
			return "", "", err
		}
		prompt += "\n\nRead and follow these role skills before work (paths to SKILL.md; resolve their relative links from their own directory):\n"
		lines := make([]string, len(sources))
		for i, s := range sources {
			lines[i] = filepath.Join(s, "SKILL.md")
		}
		prompt += strings.Join(lines, "\n")
		prompt += "\nUse native skill invocation when available, otherwise read the files directly.\n"
	}
	return path, prompt, nil
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
	sum := sha256.Sum256([]byte(text))
	target := filepath.Join(root, hex.EncodeToString(sum[:])+suffix)
	if _, err := os.Lstat(target); err == nil {
		return target, nil
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

func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// build is Python's build(): the client command line. dry replaces the
// prompt and the generated files with placeholders and writes nothing.
func build(p Profile, project, prompt, promptPath string, dry bool, env []string) ([]string, error) {
	model, opts := p.Model, p.Options
	switch p.Client {
	case "claude-code":
		cmd := []string{"claude", "--model", model}
		if opts["fallback"] != "" {
			cmd = append(cmd, "--fallback-model", opts["fallback"])
		}
		if perm := opts["permission"]; perm != "" && perm != "default" {
			cmd = append(cmd, "--permission-mode", perm)
		}
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
		spec := map[string]interface{}{}
		raw, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &spec); err != nil {
			return nil, err
		}
		spec["project"], spec["prompt"], spec["prompt_path"] = project, prompt, promptPath
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(spec); err != nil {
			return nil, err
		}
		if file, err = cacheFile(env, strings.TrimSuffix(b.String(), "\n"), ".json"); err != nil {
			return nil, err
		}
	}
	return []string{p.Adapter, "--spec-file", file}, nil
}

// preflight checks a builtin client's --help for the flags this launch will
// pass, before any cache file is written. --help never starts a session.
func preflight(p Profile, e *envMap) error {
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
		if p.Reasoning != "" {
			needed = append(needed, "--effort")
		}
		if p.Options["fallback"] != "" {
			needed = append(needed, "--fallback-model")
		}
		if perm != "" && perm != "default" {
			needed = append(needed, "--permission-mode")
		}
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
func Launch(m *Manifest, role, project string, cfg LaunchConfig) error {
	if !namePattern.MatchString(role) || !namePattern.MatchString(project) {
		return errors.New("invalid role or project name")
	}
	p, err := m.Resolve(role)
	if err != nil {
		return err
	}
	e := newEnv(cfg.Env)
	promptPath, prompt, err := promptFor(p, cfg.Repo, e)
	if err != nil {
		return err
	}
	dry := e.or("AGENT_LAUNCH_DRYRUN", "") == "1"
	if _, builtin := builtinBinary[p.Client]; !dry && !p.Legacy && builtin {
		if err := preflight(p, e); err != nil {
			return err
		}
	}
	cmd, err := build(p, project, prompt, promptPath, dry, cfg.Env)
	if err != nil {
		return err
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
		bind = []string{bin, "--project", project, "bind", role, "--pid", strconv.Itoa(os.Getpid())}
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
		"AGENT_BUS_CLIENT": p.Client, "AGENT_BUS_PROVIDER": p.Provider, "AGENT_BUS_MODEL": p.Model,
		"AGENT_BUS_PROFILE": p.Profile, "AGENT_BUS_REASONING": p.Reasoning, "ROLES_TOML": p.Manifest} {
		e.set(k, v)
	}
	// A child session must never publish the parent Claude session's transcript as its own.
	e.del("CLAUDE_CODE_SESSION_ID")
	for _, target := range sortedKeys(p.EnvRefs) {
		source := p.EnvRefs[target]
		v, ok := newEnv(cfg.Env).get(source)
		if !ok {
			return fmt.Errorf("missing environment source for %s", target)
		}
		e.set(target, v)
	}
	if p.Client == "claude-code" && e.or("AGENT_LAUNCH_KEEP_API_KEY", "") != "1" {
		e.del("ANTHROPIC_API_KEY")
	}
	if bind != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		c := exec.CommandContext(ctx, bind[0], bind[1:]...)
		c.Env, c.Stdout, c.Stderr = e.list(), cfg.Stderr, cfg.Stderr
		err := c.Run()
		cancel()
		if err != nil { // an agent the dispatcher cannot wake beats no agent
			fmt.Fprintf(cfg.Stderr, "warning: agentbus bind failed; the dispatcher will not wake %s until it is bound (agentbus --project %s bind %s --pane %s)\n",
				role, project, role, pane)
		}
	}
	path, err := lookPath(cmd[0], e.or("PATH", ""))
	if err != nil {
		return err
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
