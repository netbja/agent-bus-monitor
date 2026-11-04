package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/config"
	guardian "github.com/netbja/agent-bus-monitor/health"
)

// The C page's Backup group (backup-page): below its keys, "Back up now",
// then one row per archive of this host in backup.dir, newest first, and the
// last run's outcome from backup-last.json. busmon never restores: Enter on
// an archive opens its coverage and the restore commands to copy. No secret
// is read or shown: an archive's coverage.json and the record hold none.

// backupArchiveRow is one archive as the page shows it.
type backupArchiveRow struct {
	Name, Path string
	At         time.Time
	Size       int64 // -1 = could not be read
	Projects   []string
	Secrets    bool
	Err        string // its coverage could not be read
}

// backupFacts is what the Backup group shows beyond its keys, read in a
// command with the rows.
type backupFacts struct {
	Dir       string
	Defaulted bool // backup.dir is not set: the default folder
	Archives  []backupArchiveRow
	Last      []string // the last runs' outcomes, one line each
	Err       string   // the folder or the record could not be read
	Secrets   bool     // backup.age_recipient is set: Back up now adds --secrets
	Log       string   // where Back up now's output goes ("" = no state folder)
}

// readBackupFacts reads backup.dir's archives of host and backup-last.json.
// With no HOME and no XDG folder it reads nothing (a test reads no file).
func readBackupFacts(getenv func(string) string, host string) backupFacts {
	cfg := config.Load(config.Path(getenv), "config.toml")
	r := config.Resolver{Getenv: getenv, Files: []*config.File{cfg}}
	var f backupFacts
	f.Secrets = r.String("backup.age_recipient") != ""
	dir, defaulted, err := config.BackupDir(r, getenv)
	if err != nil {
		f.Err = err.Error()
		return f
	}
	f.Dir, f.Defaulted = dir, defaulted
	list, err := backup.List(dir, host)
	if err != nil {
		f.Err = "the folder cannot be read: " + err.Error()
	}
	for _, a := range list {
		row := backupArchiveRow{Name: a.Name, Path: a.Path, At: a.At, Size: -1}
		if st, err := os.Stat(a.Path); err == nil {
			row.Size = st.Size()
		}
		if a.Err != nil {
			row.Err = a.Err.Error()
		} else {
			row.Projects, row.Secrets = a.Coverage.Projects, a.Coverage.Secrets
		}
		f.Archives = append(f.Archives, row)
	}
	if state := notifyStateDir(getenv); state != "" {
		f.Log = filepath.Join(state, "backup.log")
		l, err := backup.ReadLast(filepath.Join(state, "backup-last.json"))
		if err != nil {
			f.Last = []string{"last run unknown (backup-last.json cannot be read)"}
		} else {
			f.Last = backupLastLines(l)
		}
	}
	return f
}

// backupLastLines is the record in the page's words: the latest scheduled
// run, and the latest after_command run of any project.
func backupLastLines(l backup.Last) []string {
	var out []string
	if s := l.Scheduled; s != nil {
		line := "last scheduled run " + s.At.Local().Format("Jan 2 15:04") + ": "
		switch {
		case s.Error != "":
			line += "failed: " + guardian.Redact(s.Error)
		default:
			line += "ok, " + s.Archive
		}
		if s.SecretsError != "" {
			line += "; secrets left out: " + guardian.Redact(s.SecretsError)
		}
		out = append(out, line)
	}
	var latest *backup.AfterEntry
	var projects []string
	for p, e := range l.After {
		e := e
		switch {
		case latest == nil || e.At.After(latest.At):
			latest, projects = &e, []string{p}
		case e.At.Equal(latest.At):
			projects = append(projects, p)
		}
	}
	if latest != nil {
		sort.Strings(projects)
		out = append(out, "last command after a backup "+latest.At.Local().Format("Jan 2 15:04")+": "+latest.Outcome+" ("+strings.Join(projects, ", ")+")")
	}
	if len(out) == 0 {
		out = []string{"no scheduled run recorded"}
	}
	return out
}

// backupNowArgs is Back up now's command line: every project of the host,
// secrets encrypted when a recipient is set (as the schedule does).
func backupNowArgs(f backupFacts) []string {
	if f.Secrets {
		return []string{"backup", "--secrets"}
	}
	return []string{"backup"}
}

// backupItems is how many selectable lines the Backup group has after its
// keys: Back up now, then each archive.
func backupItems(f *backupFacts) int {
	if f == nil {
		return 0
	}
	return 1 + len(f.Archives)
}

// backupSizeText is an archive's size, ? when unread.
func backupSizeText(n int64) string {
	if n < 0 {
		return "?"
	}
	return humanBytes(n)
}

// backupItemLines draws Back up now and the archives under the keys, from
// index first (the group's rows count before them); sel is the page's
// selection. It returns the lines and the selected one's offset (-1 = none).
func backupItemLines(s *settingsPage, f *backupFacts, first, sel, width int, running string) ([]Line, int) {
	var out []Line
	selAt := -1
	mark := func(i int) Line {
		switch {
		case i == sel && s.keys:
			return append(Line{S(RoleText, " ")}, planMark(true)...)
		case i == sel:
			return Line{S(RoleText, " "), S(RoleMuted, GlyphSelect), S(RoleText, " ")}
		}
		return append(Line{S(RoleText, " ")}, planMark(false)...)
	}
	out = append(out, Line{}, Line{S(RoleText, " "), Seg{Text: "Archives", Role: RoleText, Bold: true},
		S(RoleMuted, "  "+Preview(f.Dir)+backupDirNote(f))})
	now := mark(first)
	if first == sel {
		selAt = len(out)
	}
	label := "Back up now"
	what := "  agentbus " + strings.Join(backupNowArgs(*f), " ") + ": every project of this host"
	if !f.Secrets {
		what += ", secrets left out"
	}
	if running != "" {
		what = "  " + running
	}
	out = append(out, FitLine(append(now, S(RoleAccent, label), S(RoleMuted, Preview(what))), width))
	for i, a := range f.Archives {
		l := mark(first + 1 + i)
		if first+1+i == sel {
			selAt = len(out)
		}
		l = append(l, S(RoleText, a.At.Local().Format("2006-01-02 15:04")+"  "), S(RoleText, fmt.Sprintf("%8s  ", backupSizeText(a.Size))))
		switch {
		case a.Err != "":
			l = append(l, S(RoleAttention, "coverage unreadable"))
		default:
			sec := "no secrets"
			if a.Secrets {
				sec = "secrets"
			}
			l = append(l, S(RoleText, Preview(strings.Join(a.Projects, ", "))), S(RoleMuted, "  "+sec))
		}
		out = append(out, FitLine(l, width))
	}
	if len(f.Archives) == 0 && f.Err == "" {
		out = append(out, Line{S(RoleMuted, "    no archive of this host yet")})
	}
	if f.Err != "" {
		out = append(out, Line{S(RoleAttention, "    "+Preview(f.Err))})
	}
	for _, l := range f.Last {
		out = append(out, FitLine(Line{S(RoleMuted, "    "+Preview(l))}, width))
	}
	return out, selAt
}

func backupDirNote(f *backupFacts) string {
	if f.Defaulted && f.Dir != "" {
		return " (the default: backup.dir is not set)"
	}
	return ""
}

// backupArchivePage is an archive's page: its coverage, then the restore
// commands to copy (y copies them). busmon restores nothing itself.
func backupArchivePage(a backupArchiveRow) page {
	var b body
	field := func(k, v string) { b.seg(RoleMuted, " "+k+"  ").text(Preview(v)).text("\n") }
	field("File:    ", a.Name)
	field("Folder:  ", filepath.Dir(a.Path))
	field("Made:    ", a.At.Local().Format("2006-01-02 15:04:05 MST"))
	field("Size:    ", backupSizeText(a.Size))
	if a.Err != "" {
		b.seg(RoleAttention, " its coverage cannot be read: "+Preview(a.Err)).text("\n")
	} else {
		field("Projects:", strings.Join(a.Projects, ", "))
		sec := "not included"
		if a.Secrets {
			sec = "included, encrypted with age (restore needs --identity <file>)"
		}
		field("Secrets: ", sec)
	}
	cmds := backupRestoreCommands(a)
	b.text("\n").seg(RoleText, " Restore stays on the command line; check first with --dry-run:").text("\n")
	for _, c := range cmds {
		b.seg(RoleAccent, "   "+Preview(c)).text("\n")
	}
	b.text("\n").seg(RoleMuted, " A restore refuses a live project: agentbus suspend it first.").text("\n")
	return page{kind: ovMessage, title: "Archive " + GlyphNone + " " + a.At.Local().Format("2006-01-02 15:04"),
		body: b.Lines(), plain: strings.Join(cmds, "\n"), plainWhat: "commands", fixed: true}
}

// backupRestoreCommands are the dry runs to copy: the whole archive, then
// each project of it.
func backupRestoreCommands(a backupArchiveRow) []string {
	path := shellWord(a.Path)
	out := []string{"agentbus restore " + path + " --dry-run"}
	for _, p := range a.Projects {
		out = append(out, "agentbus restore "+path+" --project "+shellWord(p)+" --dry-run")
	}
	return out
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]+$`)

// shellWord is s as one word a shell reads back as s.
func shellWord(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// backupNowKey is Enter on Back up now: a question first, y runs it.
func (m *Model) backupNowKey(s *settingsPage) tea.Cmd {
	if r := m.ui.sleepRun; r != nil {
		m.ui.flash, m.ui.flashErr = r.verb+" already running (see "+r.log+")", true
		return nil
	}
	s.backupAsk = true
	return nil
}

// startBackup runs agentbus backup as Z runs suspend: a child in its own
// session, its output in backup.log, its last line the notice.
func (m *Model) startBackup(f *backupFacts) tea.Cmd {
	if f == nil || f.Log == "" {
		m.ui.flash, m.ui.flashErr = "no state folder for the backup's log: nothing done", true
		return nil
	}
	return m.startRun("backup", backupNowArgs(*f), f.Log)
}
