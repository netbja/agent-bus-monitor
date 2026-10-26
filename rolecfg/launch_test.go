package rolecfg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/netbja/agent-bus-monitor/clientfp"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellJoinMatchesShlex(t *testing.T) {
	// Expected values captured from Python 3 shlex.quote.
	for in, want := range map[string]string{
		"": "''", "abc": "abc", "a b": "'a b'", "it's": `'it'"'"'s'`, "--model": "--model",
		`x="y"`: `'x="y"'`, "$(touch X)": "'$(touch X)'", "é": "'é'",
		"a/b.c:d,e@f%g+h=i_-": "a/b.c:d,e@f%g+h=i_-",
	} {
		if got := ShellJoin([]string{in}); got != want {
			t.Errorf("ShellJoin(%q) = %s, want %s", in, got, want)
		}
	}
	if got := ShellJoin([]string{"env", "A=1", "x y"}); got != "env A=1 'x y'" {
		t.Errorf("join = %s", got)
	}
}

// launchFixture is the Python suite's setup: a manifest, a role prompt with a
// shell trap, one skill, and fake clients on PATH that answer --help.
type launchFixture struct {
	t                           *testing.T
	root, manifest, skills, bin string
	env                         []string
	execed                      []string
	execEnv                     []string
}

const fakeHelp = "--rules --settings --model --config --sandbox --ask-for-approval --append-system-prompt --name --effort --permission-mode --fallback-model --agent-file --yolo --auto --plan --resume"

func newLaunchFixture(t *testing.T) *launchFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "agent profiles")
	f := &launchFixture{t: t, root: root, manifest: filepath.Join(root, "roles.toml"),
		skills: filepath.Join(root, "skills"), bin: filepath.Join(root, "bin")}
	must(t, os.MkdirAll(filepath.Join(f.skills, "test-skill"), 0o755))
	must(t, os.WriteFile(filepath.Join(f.skills, "test-skill", "SKILL.md"), []byte("# Test skill\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "coder.md"), []byte("ROLE INSTRUCTIONS: review $(touch MUST_NOT_EXIST) `literal`\n"), 0o644))
	must(t, os.MkdirAll(f.bin, 0o755))
	for _, c := range []string{"claude", "codex", "kimi", "grok"} {
		f.fake(c, "#!/bin/sh\n[ \"$1\" = --help ] && echo '"+fakeHelp+"'\nexit 0\n")
	}
	f.env = []string{
		"PATH=" + f.bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + root, "ROLES_DIR=" + root, "REPO_SKILLS=" + f.skills,
		"POCOCK_SKILLS_ROOT=" + filepath.Join(root, "pocock"), "AGENT_LAUNCH_CACHE=" + filepath.Join(root, "cache"),
	}
	return f
}

func (f *launchFixture) fake(name, body string) {
	must(f.t, os.WriteFile(filepath.Join(f.bin, name), []byte(body), 0o755))
}

func (f *launchFixture) config(client, extra, roles, options string) *Manifest {
	f.t.Helper()
	options = claudePermission(client, options)
	body := "schema_version = 2\n[defaults]\nprofile = \"main\"\n[profiles.main]\nclient = \"" + client +
		"\"\nprovider = \"test-provider\"\nmodel = \"test-model\"\n" + extra + "\n" + options +
		"\n[roles.coder]\ntier = \"boot\"\nskills = [\"test-skill\"]\n" + roles + "\n"
	must(f.t, os.WriteFile(f.manifest, []byte(body), 0o644))
	m, err := Load(f.manifest)
	if err != nil {
		f.t.Fatalf("Load: %v", err)
	}
	return m
}

func (f *launchFixture) launch(m *Manifest, role string, extraEnv ...string) (string, string, error) {
	var out, errw bytes.Buffer
	f.execed, f.execEnv = nil, nil
	err := Launch(m, role, "demo", append(append([]string{}, f.env...), extraEnv...), &out, &errw,
		func(argv0 string, argv, env []string) error {
			f.execed, f.execEnv = append([]string{argv0}, argv...), env
			return nil
		})
	return out.String(), errw.String(), err
}

func envOf(env []string, key string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLaunchBuildsEachClient(t *testing.T) {
	f := newLaunchFixture(t)
	for _, c := range []struct{ client, bin string }{{"claude-code", "claude"}, {"codex", "codex"}, {"kimi-cli", "kimi"}} {
		m := f.config(c.client, "", "", "")
		if _, errOut, err := f.launch(m, "coder", "CLAUDE_CODE_SESSION_ID=parent-id"); err != nil {
			t.Fatalf("%s: %v %s", c.client, err, errOut)
		}
		if filepath.Base(f.execed[0]) != c.bin || f.execed[1] != c.bin || f.execed[3] != "test-model" {
			t.Fatalf("%s: exec %q", c.client, f.execed)
		}
		args := strings.Join(f.execed, " ")
		if strings.Contains(args, "--effort") || strings.Contains(args, "model_reasoning_effort") {
			t.Fatalf("%s: default must not set an effort: %s", c.client, args)
		}
		if _, ok := envOf(f.execEnv, "CLAUDE_CODE_SESSION_ID"); ok {
			t.Fatalf("%s: parent session id leaked", c.client)
		}
		if v, _ := envOf(f.execEnv, "AGENT_BUS_CLIENT"); v != c.client {
			t.Fatalf("%s: AGENT_BUS_CLIENT=%q", c.client, v)
		}
		if v, ok := envOf(f.execEnv, "AGENT_BUS_REASONING"); !ok || v != "" {
			t.Fatalf("%s: AGENT_BUS_REASONING=%q %v", c.client, v, ok)
		}
		payload := args
		if c.client == "kimi-cli" {
			b, err := os.ReadFile(f.execed[len(f.execed)-1])
			must(t, err)
			payload = string(b)
			if !strings.Contains(payload, "${base_prompt}") || !strings.HasPrefix(payload, "---\nname: bus-coder\n") {
				t.Fatalf("kimi agent file: %q", payload)
			}
		}
		if !strings.Contains(payload, "ROLE INSTRUCTIONS") || !strings.Contains(payload, filepath.Join(f.skills, "test-skill", "SKILL.md")) {
			t.Fatalf("%s: prompt missing role or skill: %s", c.client, payload)
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, "MUST_NOT_EXIST")); err == nil {
		t.Fatal("the role prompt was run by a shell")
	}
}

func TestLaunchCommandLines(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("claude-code", `reasoning = "xhigh"`+"\nreasoning_levels = [\"low\", \"xhigh\"]", "",
		"[profiles.main.options]\npermission = \"bypassPermissions\"\nfallback = \"fb-model\"")
	if _, e, err := f.launch(m, "coder"); err != nil {
		t.Fatal(err, e)
	}
	want := []string{"claude", "--model", "test-model", "--fallback-model", "fb-model", "--permission-mode", "bypassPermissions", "--settings", hookSettings(f), "--effort", "xhigh", "--name", "demo:coder", "--append-system-prompt"}
	if got := f.execed[1 : len(f.execed)-1]; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("claude argv = %q", got)
	}
	m = f.config("codex", `reasoning = "xhigh"`+"\nreasoning_levels = [\"xhigh\"]", "", "[profiles.main.options]\nsandbox = \"workspace-write\"\napproval = \"never\"")
	if _, e, err := f.launch(m, "coder"); err != nil {
		t.Fatal(err, e)
	}
	want = []string{"codex", "--model", "test-model", "-c", `model_provider="test-provider"`, "--sandbox", "workspace-write", "--ask-for-approval", "never", "-c", `model_reasoning_effort="xhigh"`}
	if got := f.execed[1 : len(f.execed)-1]; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("codex argv = %q", got)
	}
	m = f.config("kimi-cli", "", "", "[profiles.main.options]\npermission = \"yolo\"")
	if _, e, err := f.launch(m, "coder"); err != nil {
		t.Fatal(err, e)
	}
	if got := f.execed; got[2] != "--model" || got[4] != "--agent-file" || got[6] != "--yolo" || len(got) != 7 {
		t.Fatalf("kimi argv = %q", got)
	}
}

// Without [tool_rules] the command line is the old one plus the resume hook
// (v0.21.1): --settings carries no permissions.
func TestLaunchWithoutSharedToolRulesCarriesOnlyTheHook(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("claude-code", "", "", "[profiles.main.options]\npermission = \"bypassPermissions\"")
	rules, err := m.LaunchRules("coder")
	if err != nil || !rules.Empty() {
		t.Fatalf("LaunchRules without shared table = %+v, %v", rules, err)
	}
	if _, _, err := f.launch(m, "coder"); err != nil {
		t.Fatal(err)
	}
	want := []string{"claude", "--model", "test-model", "--permission-mode", "bypassPermissions", "--settings", hookSettings(f), "--name", "demo:coder", "--append-system-prompt"}
	if strings.Join(f.execed[1:len(f.execed)-1], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("launch argv changed without [tool_rules]: got %q want %q", f.execed, want)
	}
}

func TestLaunchResumeKeepsRoleSettingsAndRejectsOtherClients(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("claude-code", "", "", "[profiles.main.options]\npermission = \"bypassPermissions\"")
	if err := os.WriteFile(f.manifest, []byte(strings.Replace(string(mustRead(t, f.manifest)), "[roles.coder]", "[tool_rules]\ndeny = [\"Read(**/.env)\"]\n[roles.coder]", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(f.manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.launchWithResume(m, "coder", "session-123"); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(f.execed, " ")
	for _, want := range []string{"--resume session-123", "--permission-mode bypassPermissions", "--settings", "Read(**/.env)"} {
		if !strings.Contains(args, want) {
			t.Fatalf("resumed launch lacks %q: %s", want, args)
		}
	}

	codex := f.config("codex", "", "", "")
	if _, _, err := f.launchWithResume(codex, "coder", "session-123"); err == nil || !strings.Contains(err.Error(), "only for Claude Code") {
		t.Fatalf("Codex resume error = %v", err)
	}
}

// hookSettings is the --settings of a Claude launch without tool rules.
func hookSettings(f *launchFixture) string {
	s, err := settingsJSON(ToolRules{}, resumeHookCommand(newEnv(f.env)))
	must(f.t, err)
	return s
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *launchFixture) launchWithResume(m *Manifest, role, resumeID string, extraEnv ...string) (string, string, error) {
	var out, errw bytes.Buffer
	f.execed, f.execEnv = nil, nil
	err := LaunchResume(m, role, "demo", resumeID, append(append([]string{}, f.env...), extraEnv...), &out, &errw,
		func(argv0 string, argv, env []string) error {
			f.execed, f.execEnv = append([]string{argv0}, argv...), env
			return nil
		})
	return out.String(), errw.String(), err
}

func TestLaunchAdapterSpec(t *testing.T) {
	f := newLaunchFixture(t)
	f.fake("extra", "#!/bin/sh\nexit 0\n")
	body := "schema_version = 2\n[defaults]\nprofile = \"main\"\n[adapters.extra]\nexecutable = \"bin/extra\"\n[profiles.main]\nclient = \"extra\"\nprovider = \"p\"\nmodel = \"m\"\n[roles.coder]\ntier = \"boot\"\nskills = [\"test-skill\"]\n"
	must(t, os.WriteFile(f.manifest, []byte(body), 0o644))
	m, err := Load(f.manifest)
	must(t, err)
	if _, e, err := f.launch(m, "coder"); err != nil {
		t.Fatal(err, e)
	}
	if f.execed[0] != filepath.Join(f.bin, "extra") || f.execed[2] != "--spec-file" {
		t.Fatalf("adapter argv = %q", f.execed)
	}
	spec, err := os.ReadFile(f.execed[3])
	must(t, err)
	for _, want := range []string{`"project": "demo"`, `"role": "coder"`, `"prompt": "Role source:`, `"prompt_path": `, `"client": "extra"`} {
		if !strings.Contains(string(spec), want) {
			t.Fatalf("spec lacks %s: %s", want, spec)
		}
	}
}

func TestLaunchEnvEdits(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("claude-code", "env_refs = { TARGET_KEY = \"SOURCE_KEY\" }", "", "")
	if _, e, err := f.launch(m, "coder", "SOURCE_KEY=v1", "ANTHROPIC_API_KEY=secret"); err != nil {
		t.Fatal(err, e)
	}
	if v, _ := envOf(f.execEnv, "TARGET_KEY"); v != "v1" {
		t.Fatalf("env_refs: TARGET_KEY=%q", v)
	}
	if _, ok := envOf(f.execEnv, "ANTHROPIC_API_KEY"); ok {
		t.Fatal("ANTHROPIC_API_KEY kept for claude-code")
	}
	for k, want := range map[string]string{"AGENT_BUS_PROJECT": "demo", "AGENT_BUS_AGENT": "coder", "AGENT_BUS_PROVIDER": "test-provider",
		"AGENT_BUS_MODEL": "test-model", "AGENT_BUS_PROFILE": "main", "ROLES_TOML": m.manifestPath} {
		if v, _ := envOf(f.execEnv, k); v != want {
			t.Fatalf("%s=%q, want %q", k, v, want)
		}
	}
	if _, e, err := f.launch(m, "coder", "SOURCE_KEY=v1", "ANTHROPIC_API_KEY=keep", "AGENT_LAUNCH_KEEP_API_KEY=1"); err != nil {
		t.Fatal(err, e)
	}
	if v, _ := envOf(f.execEnv, "ANTHROPIC_API_KEY"); v != "keep" {
		t.Fatal("AGENT_LAUNCH_KEEP_API_KEY=1 must keep the key")
	}
	if _, _, err := f.launch(m, "coder"); err == nil || err.Error() != "missing environment source for TARGET_KEY" {
		t.Fatalf("missing source: %v", err)
	}
}

func TestLaunchDryRun(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("kimi-cli", "", "", "")
	out, _, err := f.launch(m, "coder", "AGENT_LAUNCH_DRYRUN=1", "ANTHROPIC_API_KEY=secret-value")
	must(t, err)
	want := "kimi --model test-model --agent-file '<generated agent.md>'\nprofile=main client=kimi-cli provider=test-provider reasoning=client-default; skills=test-skill\n"
	if out != want {
		t.Fatalf("dry run =\n%q\nwant\n%q", out, want)
	}
	if f.execed != nil {
		t.Fatal("dry run executed the client")
	}
	if _, err := os.Stat(filepath.Join(f.root, "cache")); err == nil {
		t.Fatal("dry run wrote the cache")
	}
	out, _, err = f.launch(m, "coder", "AGENT_LAUNCH_DRYRUN=1", "HERDR_ENV=1", "HERDR_PANE_ID=w9:p9", "AGENTBUS_BIN=/x/agentbus")
	must(t, err)
	if first := strings.SplitN(out, "\n", 2)[0]; !strings.HasPrefix(first, "/x/agentbus --project demo bind coder --pid ") {
		t.Fatalf("dry run inside herdr: first line %q", first)
	}
	cm := f.config("claude-code", "", "", "")
	out, _, err = f.launch(cm, "coder", "AGENT_LAUNCH_DRYRUN=1")
	must(t, err)
	if !strings.Contains(out, "--name demo:coder --append-system-prompt-file "+ShellJoin([]string{filepath.Join(f.root, "coder.md")})+"\n") {
		t.Fatalf("claude dry run = %q", out)
	}
}

func TestLaunchBindChild(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("codex", "", "", "")
	log := filepath.Join(f.root, "bind.log")
	stub := filepath.Join(f.root, "agentbus-stub")
	must(t, os.WriteFile(stub, []byte("#!/bin/sh\necho \"$@\" > \""+log+"\"\necho \"$AGENT_BUS_AGENT\" >> \""+log+"\"\nexit ${BIND_RC:-0}\n"), 0o755))
	herdr := []string{"HERDR_ENV=1", "HERDR_PANE_ID=w9:p9", "AGENTBUS_BIN=" + stub}
	_, errOut, err := f.launch(m, "coder", herdr...)
	must(t, err)
	b, _ := os.ReadFile(log)
	if lines := strings.Split(string(b), "\n"); !strings.HasPrefix(lines[0], "--project demo bind coder --pid ") || lines[1] != "coder" {
		t.Fatalf("bind child args/env: %q", b)
	}
	if errOut != "" || f.execed == nil {
		t.Fatalf("stderr %q, execed %v", errOut, f.execed)
	}
	_, errOut, err = f.launch(m, "coder", append(herdr, "BIND_RC=1")...)
	must(t, err)
	if !strings.Contains(errOut, "warning: agentbus bind failed; the dispatcher will not wake coder until it is bound (agentbus --project demo bind coder --pane w9:p9)") || f.execed == nil {
		t.Fatalf("failed bind: stderr %q, execed %v", errOut, f.execed)
	}
	os.Remove(log)
	if _, _, err := f.launch(m, "coder"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("bound outside herdr")
	}
}

func TestLaunchPreflightAndErrors(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("kimi-cli", "", "", "")
	f.fake("kimi", "#!/bin/sh\necho old-version\n")
	if _, _, err := f.launch(m, "coder"); err == nil || err.Error() != "kimi lacks required launch flags; inspect its version/help" {
		t.Fatalf("missing flag: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "cache")); err == nil {
		t.Fatal("cache written before the flag check")
	}
	f.fake("kimi", "#!/bin/sh\nexit 3\n")
	if _, _, err := f.launch(m, "coder"); err == nil || err.Error() != "kimi --help failed" {
		t.Fatalf("help failure: %v", err)
	}
	must(t, os.Remove(filepath.Join(f.skills, "test-skill", "SKILL.md")))
	if _, errOut, _ := f.launch(m, "coder"); !strings.Contains(errOut, "warning: skill test-skill not found") {
		t.Fatalf("missing skill must only warn: %q", errOut)
	}
	if _, _, err := f.launch(m, "nobody"); err == nil || err.Error() != "unknown role: nobody" {
		t.Fatalf("unknown role: %v", err)
	}
	if _, _, err := f.launch(m, "Bad"); err == nil || err.Error() != "invalid role or project name" {
		t.Fatalf("bad role: %v", err)
	}
	must(t, os.WriteFile(filepath.Join(f.skills, "test-skill", "SKILL.md"), []byte("x"), 0o644))
	must(t, os.Remove(filepath.Join(f.root, "coder.md")))
	if _, _, err := f.launch(m, "coder"); err == nil || !strings.HasPrefix(err.Error(), "missing role prompt file: ") {
		t.Fatalf("missing prompt: %v", err)
	}
	m = f.config("codex", "", "", "")
	must(t, os.WriteFile(filepath.Join(f.root, "coder.md"), []byte("x\n"), 0o644))
	must(t, os.Remove(filepath.Join(f.bin, "codex")))
	if _, _, err := f.launch(m, "coder", "PATH="+f.bin); err == nil || err.Error() != "client executable missing: codex" {
		t.Fatalf("missing client: %v", err)
	}
}

func TestCacheFileIsStable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "c")
	a, err := cacheFile([]string{"AGENT_LAUNCH_CACHE=" + dir}, "hello", ".md")
	must(t, err)
	b, err := cacheFile([]string{"AGENT_LAUNCH_CACHE=" + dir}, "hello", ".md")
	must(t, err)
	if a != b || filepath.Base(a) != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824.md" {
		t.Fatalf("cache paths %s %s", a, b)
	}
	st, err := os.Stat(dir)
	must(t, err)
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("cache dir mode %v", st.Mode().Perm())
	}
}

// TestAdapterSpecMatchesPython: the spec file an adapter reads is byte for
// byte what Python's json.dumps wrote (key order, ", "/": " separators,
// ASCII escapes), so its cache name is the same sha256 too. The golden was
// produced by role_config.py from the same fixture, with its root as @ROOT@.
func TestAdapterSpecMatchesPython(t *testing.T) {
	for manifest, golden := range map[string]string{"adapter.toml": "adapter-spec.golden", "adapter-arrays.toml": "adapter-arrays-spec.golden"} {
		t.Run(manifest, func(t *testing.T) { adapterSpecCase(t, manifest, golden) })
	}
}

func adapterSpecCase(t *testing.T, manifest, goldenFile string) {
	root := filepath.Join(t.TempDir(), "root dir")
	must(t, os.MkdirAll(filepath.Join(root, "skills", "test-skill"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "skills", "test-skill", "SKILL.md"), []byte("# s\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "bin", "extra"), []byte("#!/bin/sh\n"), 0o755))
	for src, dst := range map[string]string{manifest: "roles.toml", "coder.md": "coder.md"} {
		b, err := os.ReadFile(filepath.Join("testdata", "launch", src))
		must(t, err)
		must(t, os.WriteFile(filepath.Join(root, dst), b, 0o644))
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "launch", goldenFile))
	must(t, err)
	resolved, err := filepath.EvalSymlinks(root)
	must(t, err)
	want := strings.ReplaceAll(string(golden), "@ROOT@", resolved)
	m, err := Load(filepath.Join(root, "roles.toml"))
	must(t, err)
	var argv []string
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + root, "ROLES_DIR=" + root, "REPO_SKILLS=" + filepath.Join(root, "skills"),
		"POCOCK_SKILLS_ROOT=" + filepath.Join(root, "p"), "AGENT_LAUNCH_CACHE=" + filepath.Join(root, "cache"), "SRC_A=a", "SRC_B=b"}
	err = Launch(m, "coder", "demo", env, io.Discard, io.Discard, func(argv0 string, a, _ []string) error { argv = a; return nil })
	must(t, err)
	got, err := os.ReadFile(argv[2])
	must(t, err)
	if string(got) != want {
		t.Fatalf("spec differs from Python:\n got %s\nwant %s", got, want)
	}
	sum := sha256.Sum256([]byte(want))
	if filepath.Base(argv[2]) != hex.EncodeToString(sum[:])+".json" {
		t.Fatalf("cache name %s", argv[2])
	}
}

func TestPyJSONEscapes(t *testing.T) {
	for v, want := range map[interface{}]string{
		"é😀\"\\/\n\t\x7f\x01": `"\u00e9\ud83d\ude00\"\\/\n\t\u007f\u0001"`,
		int64(3):              "3", true: "true", 0.5: "0.5", 1234567.0: "1234567.0", 1e16: "1e+16", 1e-5: "1e-05", 100.0: "100.0",
	} {
		if got, err := pyJSON(v); err != nil || got != want {
			t.Errorf("pyJSON(%#v) = %s, %v; want %s", v, got, err, want)
		}
	}
}

func TestRepoFromEnv(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("codex", "", "", "")
	repo := filepath.Join(f.root, "repo")
	must(t, os.MkdirAll(filepath.Join(repo, "skills", "test-skill"), 0o755))
	must(t, os.WriteFile(filepath.Join(repo, "skills", "test-skill", "SKILL.md"), []byte("x"), 0o644))
	var env []string
	for _, kv := range f.env {
		if !strings.HasPrefix(kv, "REPO_SKILLS=") {
			env = append(env, kv)
		}
	}
	f.env = append(env, "AGENTBUS_REPO="+repo)
	if _, e, err := f.launch(m, "coder"); err != nil {
		t.Fatal(err, e)
	}
	if !strings.Contains(strings.Join(f.execed, " "), filepath.Join(repo, "skills", "test-skill", "SKILL.md")) {
		t.Fatalf("skills not taken from AGENTBUS_REPO: %q", f.execed)
	}
}

// The cache is trusted only for what it holds: a planted file or symlink with
// the right name but other content is replaced, never handed to a client.
func TestCacheFileReplacesTamperedEntries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "c")
	env := []string{"AGENT_LAUNCH_CACHE=" + dir}
	path, err := cacheFile(env, "hello", ".md")
	must(t, err)
	must(t, os.WriteFile(path, []byte("INJECTED"), 0o600))
	again, err := cacheFile(env, "hello", ".md")
	must(t, err)
	if b, _ := os.ReadFile(again); string(b) != "hello" {
		t.Fatalf("tampered cache entry reused: %q", b)
	}
	evil := filepath.Join(t.TempDir(), "evil")
	must(t, os.WriteFile(evil, []byte("INJECTED"), 0o600))
	must(t, os.Remove(path))
	must(t, os.Symlink(evil, path))
	again, err = cacheFile(env, "hello", ".md")
	must(t, err)
	if st, _ := os.Lstat(again); st.Mode()&os.ModeSymlink != 0 {
		t.Fatal("a symlink in the cache was kept")
	}
	if b, _ := os.ReadFile(again); string(b) != "hello" {
		t.Fatalf("symlinked cache entry reused: %q", b)
	}
	if b, _ := os.ReadFile(evil); string(b) != "INJECTED" {
		t.Fatal("the symlink target was written through")
	}
}

func TestCacheFileRefusesAWritableSharedDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared")
	must(t, os.MkdirAll(dir, 0o777))
	must(t, os.Chmod(dir, 0o777))
	if _, err := cacheFile([]string{"AGENT_LAUNCH_CACHE=" + dir}, "hello", ".md"); err == nil || !strings.Contains(err.Error(), "writable by others") {
		t.Fatalf("a group/world-writable cache dir was used: %v", err)
	}
}

// A missing skill warns and the agent still starts, without it in its prompt.
func TestLaunchWithAMissingSkillStarts(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("codex", "", "", "")
	must(t, os.Remove(filepath.Join(f.skills, "test-skill", "SKILL.md")))
	_, errOut, err := f.launch(m, "coder")
	if err != nil || f.execed == nil {
		t.Fatalf("launch with a missing skill: err %v, execed %v", err, f.execed)
	}
	if !strings.Contains(errOut, "warning: skill test-skill not found (run ./setup.sh install); coder starts without it") {
		t.Fatalf("stderr = %q", errOut)
	}
	if strings.Contains(strings.Join(f.execed, " "), "test-skill") {
		t.Fatal("the missing skill is still named in the prompt")
	}
}

// The external collection is looked for where setup.sh clones it, then at the
// historical ~/Tools path.
func TestExternalSkillsDefaultPaths(t *testing.T) {
	home := t.TempDir()
	for _, root := range []string{
		filepath.Join(home, ".local", "share", "agentbus", "skills", "mattpocock", "skills", "engineering"),
		filepath.Join(home, "Tools", "herdr-plugins", "skills", "skills", "engineering"),
	} {
		dir := filepath.Join(root, "wayfinder")
		must(t, os.MkdirAll(dir, 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("x"), 0o644))
		got, err := SkillSources([]string{"wayfinder"}, t.TempDir(), []string{"HOME=" + home})
		if err != nil || len(got) != 1 || got[0] != absResolved(dir) {
			t.Fatalf("SkillSources = %v, %v; want %s", got, err, dir)
		}
		must(t, os.RemoveAll(dir)) // the next root must be found on its own
	}
	if _, err := SkillSources([]string{"wayfinder"}, t.TempDir(), []string{"HOME=" + home, "XDG_DATA_HOME=" + filepath.Join(home, "x")}); err == nil {
		t.Fatal("found a skill that is nowhere")
	}
}

// v0.15.2 §10.3: a profile naming a tier launches the model the table gives
// for it now; with no such model the launch fails and says how to fix it.
func TestLaunchResolvesATier(t *testing.T) {
	f := newLaunchFixture(t)
	body := "schema_version = 2\n[defaults]\nprofile = \"main\"\n[profiles.main]\nclient = \"kimi-cli\"\nprovider = \"test-provider\"\ntier = \"frontier\"\n[roles.coder]\ntier = \"boot\"\nskills = [\"test-skill\"]\n"
	must(t, os.WriteFile(f.manifest, []byte(body), 0o644))
	m, err := Load(f.manifest)
	must(t, err)
	old := ResolveTier
	t.Cleanup(func() { ResolveTier = old })
	ResolveTier = func(provider, tier string) (string, error) {
		if provider == "test-provider" && tier == "frontier" {
			return "best-model", nil
		}
		return "", errors.New("no model")
	}
	out, _, err := f.launch(m, "coder", "AGENT_LAUNCH_DRYRUN=1")
	must(t, err)
	if !strings.HasPrefix(out, "kimi --model best-model ") {
		t.Fatalf("dry run = %q", out)
	}
	ResolveTier = func(string, string) (string, error) { return "", errors.New("no frontier model known") }
	if _, _, err := f.launch(m, "coder", "AGENT_LAUNCH_DRYRUN=1"); err == nil || !strings.Contains(err.Error(), "no frontier model") {
		t.Fatalf("missing tier model: %v", err)
	}
}

func TestTierValidation(t *testing.T) {
	head := "schema_version = 2\n[defaults]\nprofile = \"main\"\n[profiles.main]\nclient = \"kimi-cli\"\nprovider = \"p\"\n"
	for body, want := range map[string]string{
		head + "tier = \"best\"\n[roles.a]\ntier = \"boot\"\n":                "tier must be one of frontier, workhorse, fast, legacy",
		head + "tier = \"fast\"\nmodel = \"m\"\n[roles.a]\ntier = \"boot\"\n": "profile main: set model or tier, not both",
	} {
		if _, err := Load(writeConfig(t, body)); err == nil || err.Error() != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
	if _, err := Load(writeConfig(t, head+"tier = \"fast\"\n[roles.a]\ntier = \"boot\"\n")); err != nil {
		t.Fatalf("a tier profile: %v", err)
	}
}

const popsRoles = "pops = true\n[roles.coder.tool_rules]\ndeny = [\"WebFetch\"]\n[roles.deploy]\ntier = \"pop\"\napproval = \"person\"\n[roles.architect]\ntier = \"pop\"\n"

const popsSettings = `{"permissions": {"allow": ["Bash(agent-spawn architect *)"], "ask": ["Bash(agent-spawn deploy *)"], "deny": ["WebFetch"]}}`

func TestLaunchSettingsFlag(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("claude-code", "", popsRoles, "[profiles.main.options]\npermission = \"acceptEdits\"")
	if _, e, err := f.launch(m, "coder"); err != nil {
		t.Fatal(err, e)
	}
	settings := strings.TrimSuffix(popsSettings, "}") + ", " + strings.TrimPrefix(hookSettings(f), "{")
	want := []string{"claude", "--model", "test-model", "--permission-mode", "acceptEdits", "--settings", settings, "--name", "demo:coder", "--append-system-prompt"}
	if got := f.execed[1 : len(f.execed)-1]; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("claude argv = %q", got)
	}
	out, _, err := f.launch(m, "coder", "AGENT_LAUNCH_DRYRUN=1")
	must(t, err)
	if !strings.Contains(out, "--settings "+ShellJoin([]string{settings})+" --name") {
		t.Fatalf("dry run = %q", out)
	}
}

// Every Claude Code launch passes --settings (the resume hook, v0.21.1), so a
// claude without it is refused with or without rules; Codex never needs it.
func TestPreflightNeedsSettingsForClaude(t *testing.T) {
	f := newLaunchFixture(t)
	f.fake("claude", "#!/bin/sh\necho '"+strings.Replace(fakeHelp, "--settings ", "", 1)+"'\n")
	f.fake("codex", "#!/bin/sh\necho '"+strings.Replace(fakeHelp, "--settings ", "", 1)+"'\n")
	for _, roles := range []string{"", popsRoles} {
		m := f.config("claude-code", "", roles, "")
		if _, _, err := f.launch(m, "coder"); err == nil || err.Error() != "claude lacks required launch flags; inspect its version/help" {
			t.Fatalf("claude without --settings (rules %q): %v", roles, err)
		}
	}
	if _, e, err := f.launch(f.config("codex", "", "", ""), "coder"); err != nil {
		t.Fatalf("codex: %v %s", err, e)
	}
}

func TestElevatedAnnouncesAllow(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("claude-code", "", popsRoles, "")
	_, errOut, err := f.launch(m, "coder")
	must(t, err)
	if !strings.Contains(errOut, "notice: coder runs claude-code with elevated allow=1 rules") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestRulesNotAppliedNote(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("codex", "", popsRoles, "")
	_, errOut, err := f.launch(m, "coder")
	must(t, err)
	if !strings.Contains(errOut, "note: tool_rules not applied for codex") {
		t.Fatalf("stderr = %q", errOut)
	}
	if strings.Contains(strings.Join(f.execed, " "), "--settings") {
		t.Fatalf("codex got --settings: %q", f.execed)
	}
}

func TestShellSplitRoundTrip(t *testing.T) {
	for _, x := range [][]string{
		{"/r/busmon", "--project", "demo", "--as", "Bernard J"},
		{"a", "it's", "", "x\"y", "$(z)"},
	} {
		got, err := ShellSplit(ShellJoin(x))
		if err != nil || strings.Join(got, "\x00") != strings.Join(x, "\x00") || len(got) != len(x) {
			t.Fatalf("ShellSplit(ShellJoin(%q)) = %q, %v", x, got, err)
		}
	}
}

func TestShellSplitRejectsUnbalancedQuote(t *testing.T) {
	for _, s := range []string{`a 'b`, `a "b`, `a\`} {
		if got, err := ShellSplit(s); err == nil {
			t.Fatalf("ShellSplit(%q) = %q, want an error", s, got)
		}
	}
}

// The launcher fingerprints the client it resolved and passes it on as
// AGENT_BUS_CLIENT_FP (client reload spec), for bind and the agent's status.
func TestLaunchExportsTheClientFingerprint(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("claude-code", "", "", "")
	if _, e, err := f.launch(m, "coder"); err != nil {
		t.Fatal(err, e)
	}
	v, ok := envOf(f.execEnv, "AGENT_BUS_CLIENT_FP")
	if !ok {
		t.Fatal("no AGENT_BUS_CLIENT_FP")
	}
	fp, err := clientfp.Decode(v)
	if err != nil || fp.Path != f.execed[0] && filepath.Base(fp.Path) != "claude" {
		t.Fatalf("fingerprint %+v (%v), exec %v", fp, err, f.execed)
	}
	if changed, known := fp.Changed(); changed || !known {
		t.Fatalf("a fresh fingerprint reads changed=%v known=%v", changed, known)
	}
}

// claudePermission names a mode for a Claude profile that does not (a
// manifest must: claudePermissionMissing). manual is the mode that adds the
// least: it asks for everything.
func claudePermission(client, options string) string {
	if client != "claude-code" || strings.Contains(options, "permission") {
		return options
	}
	if options == "" {
		options = "[profiles.main.options]"
	}
	return options + "\npermission = \"manual\""
}
