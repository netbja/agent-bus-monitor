package rolecfg

import (
	"fmt"
	"slices"
	"strings"
)

// TeamRole is one role of a team and the profile it runs on: "" for its own
// in roles.toml, else one of its fallback_profiles (agentbus-start spec).
type TeamRole struct {
	Role, Profile string
}

// ParseTeam reads a team, "role[:profile],..." (agentbus start --roles,
// start.team): boot roles only, each once, a profile the role's own or one of
// its fallback_profiles. The team comes back in the manifest's order, a
// role's own profile read as none.
func (m *Manifest) ParseTeam(spec string) ([]TeamRole, error) {
	chosen := map[string]string{}
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("an empty role in %q", spec)
		}
		role, profile, _ := strings.Cut(item, ":")
		if strings.Contains(profile, ":") {
			return nil, fmt.Errorf("%s: want role or role:profile", item)
		}
		if !slices.Contains(m.RolesByTier("boot"), role) {
			return nil, fmt.Errorf("%s is not a boot role of roles.toml", role)
		}
		if _, twice := chosen[role]; twice {
			return nil, fmt.Errorf("%s twice", role)
		}
		own, err := m.Resolve(role)
		if err != nil {
			return nil, err
		}
		if profile == own.Profile {
			profile = ""
		}
		if profile != "" && !slices.Contains(m.Fallbacks(role), profile) {
			return nil, fmt.Errorf("%s is not %s's own profile nor one of its fallback_profiles", profile, role)
		}
		chosen[role] = profile
	}
	var team []TeamRole
	for _, role := range m.RolesByTier("boot") {
		if profile, ok := chosen[role]; ok {
			team = append(team, TeamRole{role, profile})
		}
	}
	return team, nil
}

// Template returns the project template bootstrap writes.
// busmon comes first: herdr focuses the first tab. as, when set, is the
// person busmon writes as (busmon --as).
func Template(m *Manifest, manifestPath, project, cwd, scriptsDir, repo, as string, dispatch, missions bool) string {
	return TemplateTeam(m, manifestPath, project, cwd, scriptsDir, repo, as, dispatch, missions, nil)
}

// TemplateTeam is Template for a team (ParseTeam): a tab for each of its
// roles, --profile for one off its own profile. A nil team is every boot
// role on its own profile, byte for byte Template's output.
func TemplateTeam(m *Manifest, manifestPath, project, cwd, scriptsDir, repo, as string, dispatch, missions bool, team []TeamRole) string {
	if m == nil {
		return ""
	}
	if team == nil {
		for _, role := range m.RolesByTier("boot") {
			team = append(team, TeamRole{Role: role})
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "name = %s\ndescription = %s\nworking_dir = %s\n",
		templateJSONString(project), templateJSONString("Agent Bus team for "+project), templateJSONString(cwd))
	busmon := []string{repo + "/busmon", "--project", project}
	if as != "" {
		busmon = append(busmon, "--as", as)
	}
	command := ShellJoin(busmon)
	fmt.Fprintf(&b, "\n[[tabs]]\nname = \"busmon\"\ncommand = %s\n", templateJSONString(command))
	for _, r := range team {
		words := []string{"env", "ROLES_TOML=" + manifestPath, scriptsDir + "/agent-launch", r.Role, project}
		if r.Profile != "" {
			words = append(words, "--profile", r.Profile)
		}
		command := ShellJoin(words)
		fmt.Fprintf(&b, "\n[[tabs]]\nname = %s\ncommand = %s\n", templateJSONString(r.Role), templateJSONString(command))
	}
	if dispatch {
		command = ShellJoin([]string{repo + "/agentbus", "--project", project, "dispatch"})
		fmt.Fprintf(&b, "\n[[tabs]]\nname = \"dispatch\"\ncommand = %s\n", templateJSONString(command))
	}
	if missions {
		command = ShellJoin([]string{repo + "/agentbus", "--project", project, "missions", "--repo", cwd})
		fmt.Fprintf(&b, "\n[[tabs]]\nname = \"missions\"\ncommand = %s\n", templateJSONString(command))
	}
	return b.String()
}

func templateJSONString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
