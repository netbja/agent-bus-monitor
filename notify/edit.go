package notify

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
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

// Doc is notify.toml as its lines: what precedes the first header, then one
// part per header ([..] or [[..]]), from the comment block right above it to
// the next one. Only a [kind.name] part is a target the editor may touch;
// any other ([ntfy] with dotted keys, a nested table, an array of tables) is
// kept verbatim (foureyes).
type Doc struct {
	head   []string
	tables []docTable
	ops    []docOp // what the edit means to do: the safety net compares it
}

type docTable struct {
	kind, name string // "" for a part the editor does not edit
	lines      []string
	hdr        int // the header's line within lines (comments above it come first)
}

// docOp is one edit: add (name), remove (old), rename (old to name).
type docOp struct{ old, name string }

// anyHeader is any table header line; tableHeader is a [kind.name] one, a
// trailing comment allowed.
var (
	anyHeader   = regexp.MustCompile(`^\s*\[`)
	tableHeader = regexp.MustCompile(`^\s*\[\s*([A-Za-z0-9_-]+)\.([A-Za-z0-9_-]+)\s*\]\s*(#.*)?$`)
)

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
		line := strings.TrimRight(l, "\r\n")
		if !anyHeader.MatchString(line) {
			if n := len(d.tables); n > 0 {
				d.tables[n-1].lines = append(d.tables[n-1].lines, l)
			} else {
				d.head = append(d.head, l)
			}
			continue
		}
		// The comments right above a header are its own: take them from the
		// part before, leaving the blank lines that separate the two there.
		prev := &d.head
		if n := len(d.tables); n > 0 {
			prev = &d.tables[n-1].lines
		}
		cut := len(*prev)
		for cut > 0 && strings.HasPrefix(strings.TrimSpace((*prev)[cut-1]), "#") {
			cut--
		}
		above := append([]string(nil), (*prev)[cut:]...)
		*prev = (*prev)[:cut]
		t := docTable{lines: append(above, l), hdr: len(above)}
		if m := tableHeader.FindStringSubmatch(line); m != nil {
			t.kind, t.name = m[1], m[2]
		}
		d.tables = append(d.tables, t)
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
		if t.name != "" {
			out = append(out, t.name)
		}
	}
	return out
}

func (d *Doc) find(name string) int {
	for i, t := range d.tables {
		if t.name != "" && t.name == name {
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
	d.ops = append(d.ops, docOp{name: t.Name})
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
	d.ops = append(d.ops, docOp{old: name})
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
	h := t.lines[t.hdr]
	at := strings.Index(h, "."+old)
	t.lines[t.hdr] = h[:at] + "." + name + h[at+1+len(old):]
	t.name = name
	d.ops = append(d.ops, docOp{old: old, name: name})
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
		// Followed to write the file it names; whether it may be is Load(path)'s
		// judgement below, the link's own folder included (reviewer-codex).
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
		if f := Load(path); f.Refused != "" { // the path itself, as every reader loads it
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
	after := parse(data)
	if after.Refused != "" {
		return errors.New("notify.toml not changed: the result " + after.Refused)
	}
	// The safety net (foureyes): the targets after the edit are exactly those
	// before it, changed by the edit's own ops, or nothing is written.
	want := targetNames(parse(old))
	for _, op := range d.ops {
		delete(want, op.old)
		if op.name != "" {
			want[op.name] = true
		}
	}
	if got := targetNames(after); !maps.Equal(got, want) {
		return errors.New("notify.toml not changed: it is written in a form this editor cannot edit safely: edit it by hand")
	}
	if err := sameAsRead(real, existed, stamp); err != nil {
		return err
	}
	if err := replace(real, data); err != nil {
		return err
	}
	// Loaded again through the path: exactly the targets expected, a removal
	// included, or the old bytes come back (a new file goes).
	f := reload(path)
	if f.Refused != "" || !maps.Equal(targetNames(f), want) {
		if existed {
			_ = replace(real, old)
		} else {
			_ = os.Remove(real)
			_ = syncDir(dir)
		}
		return errors.New("notify.toml was not changed: the targets did not load as expected after the write")
	}
	return nil
}

func targetNames(f *File) map[string]bool {
	out := map[string]bool{}
	for _, t := range f.Targets {
		out[t.Name] = true
	}
	return out
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
