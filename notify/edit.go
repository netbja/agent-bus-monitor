package notify

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
)

// The one writer of notify.toml (notify-guided-setup §4), on the person's
// act only: its callers, busmon's setup and `agentbus notifications`, refuse
// an agent first (internal/agentcaller). It edits by table and keeps every
// other byte (the person's comments, order and other tables); every value is
// checked by Load's own rules and written by the TOML encoder; the file is
// replaced whole (0600, in a folder 0700 that is the person's), under a lock,
// and only when nobody changed it since it was read; then it is loaded
// again, and put back as it was when the target is not there.

// ErrChanged: the file changed between the read and the write (a hand edit,
// another setup): nothing was written.
var ErrChanged = errors.New("notify.toml changed while you were editing: open the setup again")

// Doc is notify.toml as its lines: what precedes the first table, then one
// part per table, from its header to the next header.
type Doc struct {
	head    []string
	tables  []docTable
	touched []string // the names an edit added or renamed: they must load
}

type docTable struct {
	kind, name string
	lines      []string
}

// tableHeader is a [kind.name] line, a trailing comment allowed.
var tableHeader = regexp.MustCompile(`^\s*\[\s*([A-Za-z0-9_-]+)\.([^\]\s]+)\s*\]\s*(#.*)?$`)

func parseDoc(data string) *Doc {
	d := &Doc{}
	if data == "" {
		return d
	}
	lines := strings.SplitAfter(data, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, l := range lines {
		if m := tableHeader.FindStringSubmatch(strings.TrimRight(l, "\r\n")); m != nil {
			d.tables = append(d.tables, docTable{kind: m[1], name: m[2], lines: []string{l}})
			continue
		}
		if len(d.tables) == 0 {
			d.head = append(d.head, l)
			continue
		}
		t := &d.tables[len(d.tables)-1]
		t.lines = append(t.lines, l)
	}
	return d
}

func (d *Doc) bytes() []byte {
	var b strings.Builder
	for _, l := range d.head {
		b.WriteString(l)
	}
	for _, t := range d.tables {
		for _, l := range t.lines {
			b.WriteString(l)
		}
	}
	return []byte(b.String())
}

// Names are the targets' names, in the file's order.
func (d *Doc) Names() []string {
	out := make([]string, 0, len(d.tables))
	for _, t := range d.tables {
		out = append(out, t.name)
	}
	return out
}

func (d *Doc) find(name string) int {
	for i, t := range d.tables {
		if t.name == name {
			return i
		}
	}
	return -1
}

// Add appends target t as a new table, after one blank line. Its name must
// be free and valid, and every value pass Load's rules.
func (d *Doc) Add(t Target) error {
	if !nameRule.MatchString(t.Name) {
		return fmt.Errorf("%q is not a valid target name (lowercase, a letter first)", t.Name)
	}
	if d.find(t.Name) >= 0 {
		return fmt.Errorf("a target is already called %s", t.Name)
	}
	text, err := tableText(t)
	if err != nil {
		return err
	}
	// One blank line between the file and the new table, kept with the part
	// before it, so a later remove gives the file back as it was.
	if b := string(d.bytes()); b != "" {
		if !strings.HasSuffix(b, "\n") {
			d.lastLines(func(ls []string) []string { ls[len(ls)-1] += "\n"; return ls })
			b += "\n"
		}
		if !strings.HasSuffix(b, "\n\n") {
			d.lastLines(func(ls []string) []string { return append(ls, "\n") })
		}
	}
	nt := parseDoc(text).tables[0]
	d.tables = append(d.tables, nt)
	d.touched = append(d.touched, t.Name)
	return nil
}

// lastLines edits the last part of the file (the last table, else the head).
func (d *Doc) lastLines(f func([]string) []string) {
	if n := len(d.tables); n > 0 {
		d.tables[n-1].lines = f(d.tables[n-1].lines)
		return
	}
	if len(d.head) > 0 {
		d.head = f(d.head)
	} else {
		d.head = f([]string{""})
	}
}

// Remove drops the target's table, its header to the next header.
func (d *Doc) Remove(name string) error {
	i := d.find(name)
	if i < 0 {
		return fmt.Errorf("no target called %s", name)
	}
	d.tables = append(d.tables[:i], d.tables[i+1:]...)
	return nil
}

// Rename changes the table's header only: its keys and comments stay.
func (d *Doc) Rename(old, name string) error {
	i := d.find(old)
	if i < 0 {
		return fmt.Errorf("no target called %s", old)
	}
	if !nameRule.MatchString(name) {
		return fmt.Errorf("%q is not a valid target name (lowercase, a letter first)", name)
	}
	if d.find(name) >= 0 {
		return fmt.Errorf("a target is already called %s", name)
	}
	t := &d.tables[i]
	h := t.lines[0]
	at := strings.Index(h, "."+old)
	t.lines[0] = h[:at] + "." + name + h[at+1+len(old):]
	t.name = name
	d.touched = append(d.touched, name)
	return nil
}

// tableText is t's table, every value encoded, after Load's own rules judged
// it (a refusal names the key and the rule, never the value).
func tableText(t Target) (string, error) {
	var keys []string
	val := func(k string, v any) {
		var b strings.Builder
		_ = toml.NewEncoder(&b).Encode(map[string]any{k: v})
		keys = append(keys, b.String())
	}
	switch t.Kind {
	case "ntfy":
		val("topic", t.Topic)
		if t.Server != "" && t.Server != DefaultServer {
			val("server", t.Server)
			if u, err := url.Parse(t.Server); err == nil && u.Scheme == "http" {
				val("insecure", true)
			}
		}
		if t.Token != "" {
			val("token", t.Token)
		}
		if t.SelfHosted {
			val("self_hosted", true)
		}
	case "discord":
		val("webhook", t.Webhook)
	case "desktop":
	default:
		return "", fmt.Errorf("unknown kind %q: want ntfy, discord or desktop", t.Kind)
	}
	text := "[" + t.Kind + "." + t.Name + "]\n" + strings.Join(keys, "")
	f := parse([]byte(text))
	if _, ok := f.Target(t.Name); !ok || f.Refused != "" || len(f.Warnings) > 0 {
		why := f.Refused
		if len(f.Warnings) > 0 {
			w := f.Warnings[0]
			why = w.Key + ": " + w.Msg
		}
		return "", fmt.Errorf("not written: %s", why)
	}
	return text, nil
}

// reload is Load after the write (a test replaces it).
var reload = Load

// Edit applies fn to notify.toml at path and writes the result, or nothing.
func Edit(path string, fn func(*Doc) error) error {
	if path == "" {
		return errors.New("no folder for notify.toml: neither XDG_CONFIG_HOME nor HOME is an absolute path")
	}
	real := path
	if link, err := os.Lstat(path); err == nil && link.Mode()&fs.ModeSymlink != 0 {
		if real, err = filepath.EvalSymlinks(path); err != nil {
			return errors.New("notify.toml is a link that cannot be followed")
		}
	}
	dir := filepath.Dir(real)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create the folder of notify.toml: %w", err)
	}
	if why := folderRefusal(dir); why != "" {
		return errors.New("notify.toml not changed: " + why)
	}
	unlock, err := lockFile(filepath.Join(dir, "notify.toml.lock"))
	if err != nil {
		return err
	}
	defer unlock()

	old, existed, stamp, err := readForEdit(real)
	if err != nil {
		return err
	}
	if existed {
		if f := Load(real); f.Refused != "" {
			if rest, ok := strings.CutPrefix(f.Refused, "does not parse"); ok {
				at := strings.Trim(strings.TrimSpace(rest), "()") // "line 12", or nothing
				if at != "" {
					at = " at " + at
				}
				return fmt.Errorf("notify.toml has an error%s: fix it, or move it away and start again", at)
			}
			return errors.New("notify.toml not changed: " + f.Refused)
		}
	}
	d := parseDoc(string(old))
	if err := fn(d); err != nil {
		return err
	}
	data := d.bytes()
	if f := parse(data); f.Refused != "" {
		return errors.New("notify.toml not changed: the result " + f.Refused)
	}
	if err := sameAsRead(real, existed, stamp); err != nil {
		return err
	}
	if err := replace(real, data); err != nil {
		return err
	}
	f := reload(path)
	for _, name := range d.touched {
		if _, ok := f.Target(name); !ok || f.Refused != "" {
			if existed {
				_ = replace(real, old)
			} else {
				_ = os.Remove(real)
				_ = syncDir(dir)
			}
			return fmt.Errorf("notify.toml was not changed: the target %s did not load after the write", name)
		}
	}
	return nil
}

// fileStamp is what tells a later write that the file was changed.
type fileStamp struct {
	size  int64
	mtime int64
}

func readForEdit(path string) (data []byte, existed bool, st fileStamp, err error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, st, nil
	}
	if err != nil {
		return nil, false, st, errors.New("notify.toml cannot be read")
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, false, st, errors.New("notify.toml cannot be read")
	}
	return data, true, fileStamp{info.Size(), info.ModTime().UnixNano()}, nil
}

func sameAsRead(path string, existed bool, st fileStamp) error {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if existed {
			return ErrChanged
		}
		return nil
	case err != nil:
		return errors.New("notify.toml cannot be read")
	case !existed || info.Size() != st.size || info.ModTime().UnixNano() != st.mtime:
		return ErrChanged
	}
	return nil
}

// replace writes data to a new file in the folder (0600, never an existing
// file), syncs it, renames it over path and syncs the folder.
func replace(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".notify.toml.*")
	if err != nil {
		return fmt.Errorf("cannot write in the folder of notify.toml: %w", err)
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("notify.toml not changed: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("notify.toml not changed: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("notify.toml not changed: %w", err)
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// lockFile holds an exclusive flock on path (created 0600) until unlock.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot lock notify.toml: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("cannot lock notify.toml: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// SubscribeLink is where a phone subscribes to an ntfy target: its server,
// with its own scheme (http for a private server Load accepted with
// insecure), then the topic. A secret: shown only on the person's screen.
func SubscribeLink(t Target) string {
	server := t.Server
	if server == "" {
		server = DefaultServer
	}
	return strings.TrimRight(server, "/") + "/" + t.Topic
}
