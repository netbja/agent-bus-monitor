package rolecfg

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Drafting a role on demand (specialist roles spec, addendum E): master
// describes a task, the draft reuses the skills that best match it, and the
// person decides through an ask. A draft is inert: these functions only
// build text; nothing here writes a manifest.

// SkillInfo is a skill's name and description, from its SKILL.md front matter.
type SkillInfo struct {
	Name        string
	Description string
}

// ScoredSkill is a skill chosen for a draft and the task words it shares.
type ScoredSkill struct {
	Name  string   `json:"name"`
	Score int      `json:"score"`
	Words []string `json:"words,omitempty"`
}

// ListSkills reads every skill under roots (a directory holding <name>/SKILL.md),
// the first root winning a name, as findSkill does for the launcher. Only the
// front matter's name and description are read. Sorted by name.
func ListSkills(roots []string) []SkillInfo {
	seen := map[string]bool{}
	var out []SkillInfo
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if seen[e.Name()] {
				continue
			}
			desc, ok := skillDescription(filepath.Join(root, e.Name(), "SKILL.md"))
			if !ok {
				continue
			}
			seen[e.Name()] = true
			out = append(out, SkillInfo{Name: e.Name(), Description: desc})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AvailableSkills is ListSkills over the launcher's own roots: a drafted role
// names only skills its launch can resolve.
func AvailableSkills(env []string) []SkillInfo {
	e := newEnv(env)
	return ListSkills(skillRoots(repoRoot(e), e))
}

// skillDescription reads the description of a SKILL.md's front matter.
func skillDescription(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return "", false
	}
	desc := ""
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			return desc, true
		}
		if d, ok := strings.CutPrefix(line, "description:"); ok {
			desc = strings.Trim(strings.TrimSpace(d), `"'`)
		}
	}
	return "", false
}

var wordRE = regexp.MustCompile(`[a-z]+`)

// draftStop are words too common to say what a task is about.
var draftStop = map[string]bool{
	"that": true, "this": true, "with": true, "from": true, "have": true, "will": true, "when": true,
	"what": true, "your": true, "into": true, "about": true, "must": true, "should": true, "which": true,
	"their": true, "there": true, "then": true, "than": true, "also": true, "only": true, "each": true,
	"more": true, "some": true, "such": true, "make": true, "does": true, "done": true, "need": true,
	"needs": true, "work": true, "task": true, "role": true, "agent": true, "agents": true, "using": true,
	"used": true, "uses": true, "they": true, "them": true, "other": true, "every": true, "where": true,
}

func draftWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRE.FindAllString(strings.ToLower(s), -1) {
		if len(w) >= 4 && !draftStop[w] {
			out[w] = true
		}
	}
	return out
}

// DraftSkills picks a draft's skills: agent-bus always first, then the three
// skills sharing the most words with the task (at least one; ties by name).
func DraftSkills(task string, skills []SkillInfo) []ScoredSkill {
	words := draftWords(task)
	out := []ScoredSkill{{Name: "agent-bus"}}
	var scored []ScoredSkill
	for _, s := range skills {
		if s.Name == "agent-bus" {
			continue
		}
		var shared []string
		for w := range draftWords(s.Description) {
			if words[w] {
				shared = append(shared, w)
			}
		}
		if len(shared) > 0 {
			slices.Sort(shared)
			scored = append(scored, ScoredSkill{Name: s.Name, Score: len(shared), Words: shared})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Name < scored[j].Name
	})
	if len(scored) > 3 {
		scored = scored[:3]
	}
	return append(out, scored...)
}

// DraftRole renders a draft: the manifest entry, in the manifest's own schema
// and least privileged (permission manual, approval person, a pop role), and
// the prompt in the shape of roles/<role>.md. It refuses an invalid name or a
// role the manifest has.
func DraftRole(m *Manifest, role, task string, skills []ScoredSkill) (snippet, prompt string, err error) {
	if !namePattern.MatchString(role) {
		return "", "", fmt.Errorf("invalid role name %q", role)
	}
	if _, exists := m.Roles[role]; exists {
		return "", "", fmt.Errorf("role %s already exists in %s", role, m.manifestPath)
	}
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = fmt.Sprintf("%q", s.Name)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Drafted by master for: %s\n", oneLine(task))
	fmt.Fprintf(&sb, "[roles.%s]\n", role)
	if m.SchemaVersion == 2 {
		fmt.Fprintf(&sb, "profile = %q\n", draftProfile(m))
	} else {
		sb.WriteString("model = \"sonnet\"\npermission = \"manual\"\n")
	}
	fmt.Fprintf(&sb, "tier = \"pop\"\napproval = \"person\"\nskills = [%s]\n", strings.Join(names, ", "))
	var skillList []string
	for _, s := range skills {
		skillList = append(skillList, "`"+s.Name+"`")
	}
	return sb.String(), draftPrompt(role, strings.TrimSpace(task), strings.Join(skillList, ", ")), nil
}

// draftProfile is the least privileged profile of a schema-2 manifest: the
// first with permission manual, else the default profile.
func draftProfile(m *Manifest) string {
	for _, name := range m.profileOrder {
		p, _ := asTable(m.Profiles[name])
		if opts, ok := asTable(p["options"]); ok && opts["permission"] == "manual" {
			return name
		}
	}
	d, _ := m.Defaults["profile"].(string)
	return d
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func draftPrompt(role, task, skills string) string {
	return fmt.Sprintf(`# Role: %[1]s — drafted on demand

You are **%[1]s**, a role master drafted for one kind of task and the person adopted. Your
client, model, reasoning and permissions come from the launch profile in `+"`roles.toml`"+`.

Before the work itself, read [the shared memory index](../docs/memory/INDEX.md) and only
the notes your task touches. Check a note's scope, status and `+"`last_verified`"+` before acting
on it — a dated note records what was true then, not what is running now.

On boot, once:
1. Load your configured skills using native invocation or their supplied SKILL.md paths: %[3]s.
2. Publish presence: `+"`agentbus status %[1]s working \"%[1]s online\"`"+`.
3. Read what is waiting for you: `+"`agentbus request`"+` and `+"`agentbus board`"+`; read a request's
   body with `+"`agentbus thread <thread>`"+`, and accept it before you start
   (`+"`agentbus request accept <task>`"+`).
4. Arm **only if something is coming**: `+"`agentbus subscribe %[1]s`"+` as a background task
   (wake-on-exit; not a `+"`while`"+` loop). Nothing to wait for? Report your state and stop.

## Scope

%[2]s

## Never

- change a live host, service or database without the person's yes, asked by master;
- merge, tag, release or deploy;
- decide for the person: a decision goes to master, who asks the person with `+"`agentbus ask`"+`.

## Hand-back

Finish with `+"`agentbus request done <task> \"<summary>\"`"+`, then a report in the agreed shape (a
summary line, `+"`## Done`"+`, `+"`## What remains for you`"+`) with
`+"`agentbus report %[1]s --to master \"<report>\"`"+`, then `+"`agentbus status %[1]s idle \"<what you finished>\"`"+`
and stop. You do not retire yourself: master does, once your work is reviewed.
`, role, task, skills)
}
