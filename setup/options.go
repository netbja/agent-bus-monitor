package setup

import "fmt"

// PluginID is the herdr plugin id this checkout is linked as.
const PluginID = "netbja.agentbus"

// Options are setup.sh's flags.
type Options struct {
	Sub                         string // install, update, uninstall, status, rotate-redis-password
	BinDir                      string
	DryRun, Force, InstallHerdr bool
	StartDocker, InstallSkills  bool
	Yes, Purge                  bool
}

// SkillsHome and SkillsRef are where the external skills go, and their pin.
func SkillsHome(getenv func(string) string) string {
	base := getenv("XDG_DATA_HOME")
	if base == "" {
		base = getenv("HOME") + "/.local/share"
	}
	return base + "/agentbus/skills/mattpocock"
}

func SkillsRef(getenv func(string) string) string {
	if r := getenv("SKILLS_REF"); r != "" {
		return r
	}
	return "v1.3.1"
}

// ParseArgs reads setup.sh's command line. It returns -1 to go on, or the
// exit code after printing the help (0) or a refusal (1), as setup.sh does.
func ParseArgs(args []string, getenv func(string) string, o *Output) (Options, int) {
	opt := Options{Sub: "install", StartDocker: true, InstallSkills: true, BinDir: getenv("BIN_DIR")}
	if opt.BinDir == "" {
		opt.BinDir = getenv("HOME") + "/.local/bin"
	}
	if len(args) > 0 {
		switch args[0] {
		case "install", "update", "uninstall", "status", "rotate-redis-password":
			opt.Sub, args = args[0], args[1:]
		}
	}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--bin-dir":
			if i+1 >= len(args) {
				o.Fatal("--bin-dir requires a directory argument")
				return opt, 1
			}
			opt.BinDir = args[i+1]
			i++
		case "--dry-run":
			opt.DryRun = true
		case "--install-herdr":
			opt.InstallHerdr = true
		case "--force":
			opt.Force = true
		case "--no-docker":
			opt.StartDocker = false
		case "--no-skills":
			opt.InstallSkills = false
		case "--yes", "-y":
			opt.Yes = true
		case "--purge":
			opt.Purge = true
		case "-h", "--help":
			fmt.Fprint(o.out, Usage(getenv))
			return opt, 0
		default:
			o.Fatal("Unknown flag: " + a + " (see --help)")
			return opt, 1
		}
	}
	if opt.Sub == "rotate-redis-password" && (opt.InstallHerdr || opt.Purge || opt.Force || !opt.StartDocker || !opt.InstallSkills) {
		o.Fatal("rotate-redis-password takes only --dry-run and --yes (see --help)")
		return opt, 1
	}
	if opt.Sub == "uninstall" && opt.DryRun {
		o.Fatal("uninstall has no --dry-run: it asks before every step (run it without --yes to choose)")
		return opt, 1
	}
	if opt.Force && opt.Sub != "update" { // the switch's release warns, once (update hands --force to the install it runs); the next one drops the flag (spec: Settled in review, 2)
		o.Warn("--force has had no effect since v0.20 and is removed in the next release: drop it from your commands")
	}
	return opt, -1
}

// RotateHelp is the help's one line the bash fallback has no counterpart for
// (redis-password): scripts/setup-legacy.sh cannot rotate the password.
const RotateHelp = "  ./setup.sh rotate-redis-password [--dry-run] [--yes]  replace the broker's password\n"

// Usage is setup.sh's help text.
func Usage(getenv func(string) string) string {
	return `Agent Bus Monitor — setup

Usage:
  ./setup.sh install   [options]
  ./setup.sh update    [options]          git pull --ff-only, then install
  ./setup.sh uninstall [--yes] [--purge]  asks before every removal
  ./setup.sh status
` + RotateHelp + `
Install / update options:
  --bin-dir <dir>   Install directory for binaries (default: $HOME/.local/bin)
  --dry-run         Inspect the environment and show the actions, change nothing
  --install-herdr   Download herdr (herdr.dev installer) if missing
  --force           No effect since v0.20 (kept so older commands still run)
  --no-docker       Do not start the Redis broker container
  --no-skills       Do not fetch the external skills nor link role skills
  -h, --help        Show this help

Uninstall options:
  --yes             Accept the cautious default at every step (Redis is stopped, its data kept)
  --purge           Also remove the Redis container and its data without asking

agentbus and busmon are rebuilt on every install, into the repository (where the herdr tabs run
them) and into the bin dir, stamped with the release (agentbus version, busmon --version).
The repository is then linked as the herdr plugin ` + PluginID + ` (herdr plugin link), and
prefix+up (Ctrl+B then Up) bound to its Projects picker.

Environment variables:
  BIN_DIR           Binary installation directory
  SKILLS_REF        mattpocock/skills tag cloned into ` + SkillsHome(getenv) + ` (default: ` + SkillsRef(getenv) + `)
  NO_COLOR          Disables colorized terminal output
`
}
