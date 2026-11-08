package notify

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
)

// notify.toml (spec "Targets and where their secrets live"): the person's
// file, 0600, never written by agentbus. One table per target,
// [ntfy.<name>], [discord.<name>] or [desktop.<name>]. A file readable or
// writable by anyone else is refused whole; a bad target is dropped with a
// warning naming the target, the key and the line, never a value.

// Path is notify.toml beside config.toml: $XDG_CONFIG_HOME/agentbus, else
// ~/.config/agentbus; "" when neither is an absolute path.
func Path(getenv func(string) string) string {
	if d := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "agentbus", "notify.toml")
	}
	if home := getenv("HOME"); filepath.IsAbs(home) {
		return filepath.Join(home, ".config", "agentbus", "notify.toml")
	}
	return ""
}

// DefaultServer is the ntfy server a target uses without its own (the
// person's answer 5: the public ntfy.sh).
const DefaultServer = "https://ntfy.sh"

// Target is one table of notify.toml that passed every rule. It holds
// secrets (the topic, the token, the webhook): its String and GoString name
// only its kind and name, so no format verb prints them.
type Target struct {
	Name       string
	Kind       string // ntfy, discord, desktop
	Server     string // ntfy: the server, no trailing slash
	Topic      string // ntfy
	Token      string // ntfy, optional
	SelfHosted bool   // ntfy: the person's own server
	Webhook    string // discord
	Line       int    // its table's header
}

func (t Target) String() string { return t.Kind + " target " + t.Name }

// Format makes every verb, %+v included, print String: the fields hold
// secrets.
func (t Target) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(t.String())) }

// URL is where a send goes: <server>/<topic> for ntfy, the webhook for
// Discord, "" for the desktop. A secret: never shown.
func (t Target) URL() string {
	switch t.Kind {
	case "ntfy":
		return t.Server + "/" + t.Topic
	case "discord":
		return t.Webhook
	}
	return ""
}

// Private is a target that may receive full text (the person's answer 5):
// the desktop, or a ntfy server the person declares their own. ntfy.sh and Discord are
// third parties: summary only.
func (t Target) Private() bool {
	return t.Kind == "desktop" || (t.Kind == "ntfy" && t.SelfHosted)
}

// Sender is the target's sender; run is the desktop's notify-send (nil =
// not on PATH), unused by the others.
func (t Target) Sender(run DesktopRun) Sender {
	switch t.Kind {
	case "ntfy":
		return &ntfy{name: t.Name, url: t.URL(), token: t.Token, c: newClient()}
	case "discord":
		return &discord{name: t.Name, url: t.URL(), c: newClient()}
	}
	return NewDesktop(t.Name, run)
}

// Warning is one problem with one target or key: never a value.
type Warning struct {
	Line int
	Key  string // kind.name or kind.name.key
	Msg  string
}

func (w Warning) String() string {
	return fmt.Sprintf("notify.toml:%d: %s: %s", w.Line, w.Key, w.Msg)
}

// File is what Load read.
type File struct {
	Exists   bool
	Refused  string // the whole file refused: why (never its content)
	Targets  []Target
	Warnings []Warning
}

// Target is the valid target called name.
func (f *File) Target(name string) (Target, bool) {
	for _, t := range f.Targets {
		if t.Name == name {
			return t, true
		}
	}
	return Target{}, false
}

// afterOpen is a test's hook between the open and the read.
var afterOpen = func() {}

// Load reads notify.toml at path. A missing file is an empty one. A symlink
// is resolved once; the link's folder and the file's folder must be the
// person's and writable by no one else, and the file is opened once
// (O_NOFOLLOW), judged by fstat on that descriptor and read from it, so the
// file judged is the file read (reviewer-codex).
func Load(path string) *File {
	f := &File{}
	if path == "" {
		return f
	}
	link, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f
	}
	f.Exists = true
	if err != nil {
		f.Refused = "cannot be read"
		return f
	}
	real := path
	if link.Mode()&fs.ModeSymlink != 0 {
		if why := folderRefusal(filepath.Dir(path)); why != "" {
			f.Refused = why
			return f
		}
		if real, err = filepath.EvalSymlinks(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				f.Exists = false
				return f
			}
			f.Refused = "cannot be read"
			return f
		}
	}
	if why := folderRefusal(filepath.Dir(real)); why != "" {
		f.Refused = why
		return f
	}
	fd, err := os.OpenFile(real, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		f.Refused = "cannot be read"
		return f
	}
	defer fd.Close()
	afterOpen()
	info, err := fd.Stat()
	if err != nil {
		f.Refused = "cannot be read"
		return f
	}
	if why := fileRefusal(info); why != "" {
		f.Refused = why
		return f
	}
	data, err := io.ReadAll(io.LimitReader(fd, maxFile+1))
	if err != nil {
		f.Refused = "cannot be read"
		return f
	}
	if len(data) > maxFile {
		f.Refused = "over 64 KiB"
		return f
	}
	parsed := parse(data)
	parsed.Exists = true
	return parsed
}

// parse is the file's content judged by the read rules, from its bytes (Load
// once the file passed its checks; Edit before it writes).
func parse(data []byte) *File {
	f := &File{}
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		f.Refused = "does not parse"
		var pe toml.ParseError
		if errors.As(err, &pe) {
			f.Refused = fmt.Sprintf("does not parse (line %d)", pe.Position.Line) // toml's message can quote the file
		}
		return f
	}
	f.read(raw, lineIndex(string(data)))
	return f
}

// maxFile is the most notify.toml may hold.
const maxFile = 64 << 10

// fileRefusal is why a file is refused whole ("" = read it): not a regular
// file, not the person's, or open to anyone else.
func fileRefusal(info fs.FileInfo) string {
	if !info.Mode().IsRegular() {
		return "not a regular file"
	}
	if !ownedByMe(info) {
		return "not owned by you"
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "readable or writable by others: run chmod 600 on notify.toml"
	}
	return ""
}

// folderRefusal is why the folder holding notify.toml (or its link) is
// refused: not the person's, or writable by anyone else, who could swap the
// file.
func folderRefusal(dir string) string {
	info, err := os.Stat(dir)
	if err != nil {
		return "its folder cannot be read"
	}
	if !ownedByMe(info) {
		return "its folder is not yours"
	}
	if info.Mode().Perm()&0o022 != 0 {
		return "its folder is writable by others: run chmod 700 on it"
	}
	return ""
}

func ownedByMe(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return !ok || int(st.Uid) == os.Getuid()
}

var (
	topicRule = regexp.MustCompile(`^[A-Za-z0-9_-]{12,64}$`)
	nameRule  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	kinds     = map[string]bool{"ntfy": true, "discord": true, "desktop": true}
	// secretNames are the key names an unknown key is called out for.
	secretNames = map[string]bool{"password": true, "passwd": true, "secret": true, "api_key": true,
		"apikey": true, "credential": true, "credentials": true, "key": true}
)

// read checks each table and keeps the good targets.
func (f *File) read(raw map[string]any, lines map[string]int) {
	warn := func(key, msg string) {
		f.Warnings = append(f.Warnings, Warning{Line: lines[key], Key: key, Msg: msg})
	}
	byName := map[string][]Target{}
	kindNames := make([]string, 0, len(raw))
	for k := range raw {
		kindNames = append(kindNames, k)
	}
	sort.Slice(kindNames, func(i, j int) bool { return lines[kindNames[i]] < lines[kindNames[j]] })
	for _, kind := range kindNames {
		tables, ok := raw[kind].(map[string]any)
		if !ok {
			warn(kind, "want a table: [ntfy.<name>], [discord.<name>] or [desktop.<name>]")
			continue
		}
		names := make([]string, 0, len(tables))
		for n := range tables {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			at := kind + "." + name
			if !kinds[kind] {
				warn(at, "unknown kind: want ntfy, discord or desktop; ignored")
				continue
			}
			keys, ok := tables[name].(map[string]any)
			if !ok {
				warn(at, "want a table: ["+kind+".<name>]")
				continue
			}
			if !nameRule.MatchString(name) {
				warn(at, "not a valid target name (lowercase, a letter first); ignored")
				continue
			}
			t, bad := readTarget(kind, name, keys, lines, warn)
			if bad {
				continue
			}
			t.Line = lines[at]
			byName[name] = append(byName[name], t)
		}
	}
	for name, ts := range byName {
		if len(ts) > 1 {
			for _, t := range ts {
				f.Warnings = append(f.Warnings, Warning{Line: t.Line, Key: t.Kind + "." + name,
					Msg: "the name " + name + " is used twice; both ignored"})
			}
			continue
		}
		f.Targets = append(f.Targets, ts[0])
	}
	sort.Slice(f.Targets, func(i, j int) bool { return f.Targets[i].Name < f.Targets[j].Name })
	sort.SliceStable(f.Warnings, func(i, j int) bool { return f.Warnings[i].Line < f.Warnings[j].Line })
}

// readTarget checks one table; bad = refused (each problem warned once).
func readTarget(kind, name string, keys map[string]any, lines map[string]int, warn func(key, msg string)) (Target, bool) {
	at := kind + "." + name
	t := Target{Name: name, Kind: kind}
	bad := false
	refuse := func(key, msg string) {
		warn(key, msg+"; target ignored")
		bad = true
	}
	str := func(k string) (string, bool) {
		s, ok := keys[k].(string)
		if !ok || s == "" || strings.ContainsAny(s, "\r\n") {
			refuse(at+"."+k, "want a one-line string")
			return "", false
		}
		return s, true
	}
	boolean := func(k string) bool {
		b, ok := keys[k].(bool)
		if !ok {
			refuse(at+"."+k, "want true or false")
		}
		return b
	}
	allowed := map[string][]string{
		"ntfy":    {"topic", "server", "token", "self_hosted", "insecure"},
		"discord": {"webhook"},
		"desktop": nil,
	}[kind]
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool { return lines[at+"."+ordered[i]] < lines[at+"."+ordered[j]] })
	for _, k := range ordered {
		known := false
		for _, a := range allowed {
			known = known || a == k
		}
		if !known {
			msg := "unknown key"
			if secretNames[strings.ToLower(k)] {
				msg += "; a secret here has no use"
			}
			refuse(at+"."+k, msg)
		}
	}
	if bad {
		return t, true
	}
	switch kind {
	case "ntfy":
		insecure := false
		if _, ok := keys["insecure"]; ok {
			insecure = boolean("insecure")
		}
		if _, ok := keys["self_hosted"]; ok {
			t.SelfHosted = boolean("self_hosted")
		}
		if _, ok := keys["token"]; ok {
			t.Token, _ = str("token")
		}
		if _, ok := keys["topic"]; !ok {
			refuse(at, "no topic")
		} else if s, ok := str("topic"); ok {
			if !topicRule.MatchString(s) {
				refuse(at+".topic", "want 12 to 64 letters, digits, - or _ (on a public server its name is its only lock)")
			}
			t.Topic = s
		}
		t.Server = DefaultServer
		if _, ok := keys["server"]; ok {
			if s, ok := str("server"); ok {
				u, why := serverURL(s, insecure)
				if why != "" {
					refuse(at+".server", why)
				}
				t.Server = u
			}
		}
		if t.SelfHosted && !bad && publicNtfy(t.Server) {
			refuse(at+".self_hosted", "ntfy.sh is a public server, not your own")
		}
	case "discord":
		if _, ok := keys["webhook"]; !ok {
			refuse(at, "no webhook")
		} else if s, ok := str("webhook"); ok {
			if why := webhookURL(s); why != "" {
				refuse(at+".webhook", why)
			}
			t.Webhook = s
		}
	}
	return t, bad
}

// serverURL checks a ntfy server and returns it without a trailing slash,
// or why it is refused (never quoting it).
func serverURL(raw string, insecure bool) (string, string) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", "want an https URL"
	}
	if u.User != nil {
		return "", "user info in a URL is a secret: use token"
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", "want a URL without a query or fragment"
	}
	if u.Scheme == "http" {
		if !insecure {
			return "", "want an https URL (http only with insecure = true on a private host)"
		}
		if !privateHost(u.Hostname()) {
			return "", "http only on a loopback, .local or private address"
		}
	}
	return strings.TrimRight(raw, "/"), ""
}

// privateHost: loopback, link-local, a private address or a .local name.
func privateHost(h string) bool {
	h = strings.ToLower(h)
	if h == "localhost" || strings.HasSuffix(h, ".local") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// publicNtfy is the public ntfy.sh (any case, any subdomain).
func publicNtfy(server string) bool {
	u, err := url.Parse(server)
	if err != nil {
		return false
	}
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	return h == "ntfy.sh" || strings.HasSuffix(h, ".ntfy.sh")
}

// webhookURL is why a Discord webhook is refused ("" = taken).
func webhookURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" {
		return "want an https URL"
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(raw, "#") {
		return "want a webhook URL without user info, query or fragment"
	}
	if h := strings.ToLower(u.Host); h != "discord.com" && h != "discordapp.com" {
		return "want a webhook on discord.com or discordapp.com"
	}
	if !strings.HasPrefix(u.Path, "/api/webhooks/") {
		return "want a webhook URL: https://discord.com/api/webhooks/..."
	}
	return ""
}

// lineIndex finds the line of each [table] header (as kind.name, and its
// kind alone at its first table) and each key under it (kind.name.key);
// good enough for a warning.
func lineIndex(text string) map[string]int {
	out := map[string]int{}
	table := ""
	for i, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "[") && !strings.HasPrefix(s, "[[") {
			if end := strings.Index(s, "]"); end > 0 {
				table = strings.TrimSpace(s[1:end])
				if out[table] == 0 {
					out[table] = i + 1
				}
				if kind, _, ok := strings.Cut(table, "."); ok && out[kind] == 0 {
					out[kind] = i + 1
				}
			}
			continue
		}
		if k, _, ok := strings.Cut(s, "="); ok && !strings.HasPrefix(s, "#") {
			k = strings.TrimSpace(k)
			full := k
			if table != "" {
				full = table + "." + k
			}
			if out[full] == 0 {
				out[full] = i + 1
			}
		}
	}
	return out
}
