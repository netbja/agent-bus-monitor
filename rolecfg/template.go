package rolecfg

import (
	"fmt"
	"strings"
)

// Template returns the herdr-plus project template produced by bootstrap.
func Template(m *Manifest, manifestPath, project, cwd, scriptsDir, repo string, dispatch, missions bool) string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "name = %s\ndescription = %s\nworking_dir = %s\n",
		templateJSONString(project), templateJSONString("Agent Bus team for "+project), templateJSONString(cwd))
	for _, role := range m.RolesByTier("boot") {
		command := templateShellJoin([]string{"env", "ROLES_TOML=" + manifestPath, scriptsDir + "/agent-launch", role, project})
		fmt.Fprintf(&b, "\n[[tabs]]\nname = %s\ncommand = %s\n", templateJSONString(role), templateJSONString(command))
	}
	command := templateShellJoin([]string{repo + "/busmon", "--project", project})
	fmt.Fprintf(&b, "\n[[tabs]]\nname = \"busmon\"\ncommand = %s\n", templateJSONString(command))
	if dispatch {
		command = templateShellJoin([]string{repo + "/agentbus", "--project", project, "dispatch"})
		fmt.Fprintf(&b, "\n[[tabs]]\nname = \"dispatch\"\ncommand = %s\n", templateJSONString(command))
	}
	if missions {
		command = templateShellJoin([]string{repo + "/agentbus", "--project", project, "missions", "--repo", cwd})
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

func templateShellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = templateShellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func templateShellQuote(value string) string {
	if value != "" {
		safe := true
		for i := 0; i < len(value); i++ {
			c := value[i]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("@%+=:,./_-", rune(c)) {
				continue
			}
			safe = false
			break
		}
		if safe {
			return value
		}
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
