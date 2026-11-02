package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// A folder's base name made a project name (agentbus-start spec, the
// deduction table): lower-cased, every run outside [a-z0-9_-] one -, a
// leading non-letter dropped, cut at 32; nothing valid left is "" (asked).
func TestStartProjectName(t *testing.T) {
	for in, want := range map[string]string{
		"My Shop": "my-shop", "shop": "shop", "_x": "x", "2048": "", "--": "", "Été 2024": "t-2024",
		"a.b.c": "a-b-c", strings.Repeat("abcdefghij", 4): strings.Repeat("abcdefghij", 4)[:32], "x__y": "x__y",
	} {
		if got := startProjectName(in); got != want {
			t.Errorf("startProjectName(%q) = %q, want %q", in, got, want)
		}
	}
}

// deduceFixture: a manifest with three boot roles (foureyes with a
// fallback), a projects dir, a config dir, and $USER.
func deduceFixture(t *testing.T) (*rolecfg.Manifest, string, string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	m, _ := rolecfg.Load(write(t, filepath.Join(dir, "roles.toml"), ownProfileManifest+"[roles.coder]\ntier = \"boot\"\nskills = [\"agent-bus\"]\n"))
	projects, xdg := filepath.Join(dir, "projects"), filepath.Join(dir, "xdg")
	for _, d := range []string{projects, filepath.Join(xdg, "agentbus")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"AGENTBUS_PROJECTS_DIR": projects, "XDG_CONFIG_HOME": xdg, "HOME": dir, "USER": "Root.Admin"}
	return m, projects, xdg, env
}

func TestDeduceStart(t *testing.T) {
	m, projects, xdg, env := deduceFixture(t)
	getenv := func(k string) string { return env[k] }
	base := t.TempDir()
	shop := filepath.Join(base, "My Shop")
	if err := os.MkdirAll(filepath.Join(shop, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", shop).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}

	// A new project: the Git root, the base name, $USER normalised and not
	// recorded, the boot roles in manifest order.
	p, err := deduceStart(filepath.Join(shop, "sub"), getenv, m)
	if err != nil {
		t.Fatal(err)
	}
	if p.Folder != shop || p.Project != "my-shop" || p.Existing || p.Name != "root-admin" || p.NameRecorded {
		t.Fatalf("new project: %+v", p)
	}
	if got := teamNames(p.Team); got != "master foureyes coder" {
		t.Fatalf("team = %q", got)
	}

	// The recorded name wins; start.team is the default team, a stale item
	// dropped and said, master added back.
	write(t, filepath.Join(xdg, "agentbus", "config.toml"), "[identity]\nas = \"charlie\"\n[start]\nteam = \"coder,foureyes:review,ghost\"\n")
	p, _ = deduceStart(shop, getenv, m)
	if p.Name != "charlie" || !p.NameRecorded {
		t.Fatalf("recorded name: %+v", p)
	}
	if got := teamNames(p.Team); got != "master foureyes:review coder" {
		t.Fatalf("start.team = %q", got)
	}
	if !hasNote(p.Notes, "ghost") || !hasNote(p.Notes, "master") {
		t.Fatalf("notes = %q", p.Notes)
	}

	// An existing template on this folder: its name and its tabs' team.
	write(t, filepath.Join(projects, "shop.toml"), "name = \"shop\"\nworking_dir = \""+shop+"\"\n"+
		"\n[[tabs]]\nname = \"busmon\"\ncommand = \"/r/busmon --project shop\"\n"+
		"\n[[tabs]]\nname = \"master\"\ncommand = \"/r/scripts/agent-launch master shop\"\n"+
		"\n[[tabs]]\nname = \"foureyes\"\ncommand = \"/r/scripts/agent-launch foureyes shop --profile review\"\n")
	p, _ = deduceStart(shop, getenv, m)
	if p.Project != "shop" || !p.Existing || teamNames(p.Team) != "master foureyes:review" {
		t.Fatalf("existing: %+v team %q", p, teamNames(p.Team))
	}

	// A template of another folder holds the base name: -2.
	other := filepath.Join(base, "shop")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	p, _ = deduceStart(other, getenv, m)
	if p.Project != "shop-2" || p.Existing || !hasNote(p.Notes, "shop") {
		t.Fatalf("taken name: %+v", p)
	}

	// Two templates on one folder: asked, both named.
	write(t, filepath.Join(projects, "shop-b.toml"), "name = \"shop-b\"\nworking_dir = \""+shop+"\"\n")
	p, _ = deduceStart(shop, getenv, m)
	if p.Project != "" || !strings.Contains(p.ProjectAsk, "shop") || !strings.Contains(p.ProjectAsk, "shop-b") {
		t.Fatalf("two templates: %+v", p)
	}

	// No valid name from the folder: asked.
	digits := filepath.Join(base, "2048")
	if err := os.MkdirAll(digits, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, _ = deduceStart(digits, getenv, m); p.Project != "" || p.ProjectAsk == "" {
		t.Fatalf("2048: %+v", p)
	}

	// No folder: an error.
	if _, err := deduceStart(filepath.Join(base, "nowhere"), getenv, m); err == nil {
		t.Fatal("a missing folder deduced")
	}
}

// start.team read item by item: a duplicate kept once, a role named like
// master is not master (master added back).
func TestDefaultTeam(t *testing.T) {
	m, _, _, _ := deduceFixture(t)
	team, notes := defaultTeam(m, "coder,coder,foureyes", nil)
	if teamNames(team) != "master foureyes coder" || !hasNote(notes, "master added back") {
		t.Fatalf("team %q notes %q", teamNames(team), notes)
	}
	team, notes = defaultTeam(m, "master,coder", nil)
	if teamNames(team) != "master coder" || len(notes) != 0 {
		t.Fatalf("team %q notes %q", teamNames(team), notes)
	}
}

// A linked worktree resolves to its main checkout: a team works in one
// checkout and makes its own worktrees.
func TestDeduceStartWorktree(t *testing.T) {
	m, _, _, env := deduceFixture(t)
	main := filepath.Join(t.TempDir(), "proj")
	for _, args := range [][]string{{"init", "-q", main}, {"-C", main, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "x"}, {"-C", main, "worktree", "add", "-q", filepath.Join(main, ".worktrees", "w")}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	p, err := deduceStart(filepath.Join(main, ".worktrees", "w"), func(k string) string { return env[k] }, m)
	if err != nil || p.Folder != main || p.Project != "proj" {
		t.Fatalf("worktree: %+v %v", p, err)
	}
}

func teamNames(team []rolecfg.TeamRole) string {
	var out []string
	for _, r := range team {
		if r.Profile != "" {
			out = append(out, r.Role+":"+r.Profile)
		} else {
			out = append(out, r.Role)
		}
	}
	return strings.Join(out, " ")
}

func hasNote(notes []string, word string) bool {
	return slices.ContainsFunc(notes, func(n string) bool { return strings.Contains(n, word) })
}

// A template whose working_dir reaches the folder through a symlink (bootstrap
// records cd ... && pwd, the logical path) is that folder's project, not a
// second one (foureyes, reviewer-codex).
func TestDeduceStartThroughASymlink(t *testing.T) {
	m, projects, _, env := deduceFixture(t)
	real := filepath.Join(t.TempDir(), "shop")
	if out, err := exec.Command("git", "init", "-q", real).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	links := t.TempDir()
	if err := os.Symlink(filepath.Dir(real), filepath.Join(links, "src")); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(links, "src", "shop")
	write(t, filepath.Join(projects, "shop.toml"), "name = \"shop\"\nworking_dir = \""+alias+"\"\n\n[[tabs]]\nname = \"master\"\ncommand = \"/r/scripts/agent-launch master shop\"\n")
	for _, from := range []string{alias, real} {
		p, err := deduceStart(from, func(k string) string { return env[k] }, m)
		if err != nil || p.Project != "shop" || !p.Existing {
			t.Fatalf("from %s: %+v %v", from, p, err)
		}
	}
	// --name on that alias is this folder's project too.
	p, _ := deduceStart(real, func(k string) string { return env[k] }, m)
	if err := p.setProject("shop", "--name", func(k string) string { return env[k] }, m); err != nil || !p.Existing {
		t.Fatalf("--name through the alias: %v %+v", err, p)
	}
}

// An existing template that cannot be read is an error, never an empty team;
// a tab whose --profile roles.toml no longer allows is asked again (foureyes).
func TestDeduceStartChecksTheTemplatesTeam(t *testing.T) {
	m, projects, _, env := deduceFixture(t)
	folder := filepath.Join(t.TempDir(), "shop")
	if out, err := exec.Command("git", "init", "-q", folder).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	folder, _ = filepath.EvalSymlinks(folder)
	head := "name = \"shop\"\nworking_dir = \"" + folder + "\"\n"
	write(t, filepath.Join(projects, "shop.toml"), head+"\n[[tabs]]\nname = \"foureyes\"\ncommand = \"/r/scripts/agent-launch foureyes shop --profile other\"\n")
	p, err := deduceStart(folder, func(k string) string { return env[k] }, m)
	if err != nil || !strings.Contains(p.TeamAsk, "foureyes") || !strings.Contains(p.TeamAsk, "other") {
		t.Fatalf("a profile no longer allowed: %+v %v", p, err)
	}
	write(t, filepath.Join(projects, "shop.toml"), head+"\n[[tabs]\nbroken")
	if _, err := deduceStart(folder, func(k string) string { return env[k] }, m); err == nil {
		t.Fatal("an unreadable template deduced")
	}
}
