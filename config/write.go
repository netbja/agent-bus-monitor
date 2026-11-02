package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// busmon's settings page writes config.toml (spec
// docs/superpowers/specs/2026-11-01-busmon-config-page-mockups.md, "Where and
// how a change is written"): one key at a time, the smallest edit, checked by
// the key's own file rule and read back before anything reaches the disk.

// RefusedError is a value the key's own file rule refuses: a reader would
// ignore it, so it is never written. Why is the rule's words.
type RefusedError struct {
	Key, Why string
}

func (e *RefusedError) Error() string { return e.Key + ": " + e.Why }

// NotParsingError is a config.toml that does not parse (a hand edit in
// progress): nothing is written to it. Line is 0 when unknown; the parser's
// message is never kept, it can quote the file.
type NotParsingError struct {
	Line int
}

func (e *NotParsingError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("config.toml does not parse (line %d): fix it by hand first", e.Line)
	}
	return "config.toml does not parse: fix it by hand first"
}

// errNotReadBack: the edit would not read back as exactly that change (a
// form the line edit cannot reach, such as an inline table).
var errNotReadBack = errors.New("this change cannot be made safely here: config.toml writes the key in a form the page cannot edit; edit the file by hand")

// Set writes key = v in the config.toml at path: the key's own line replaced,
// or the key added under its [table] (the table appended when missing), every
// other byte kept. v is the key's kind's Go value (a time.Duration for a
// duration). The value must pass the key's file rule, and the edited text
// must read back with that one change before it is written (a temporary file
// renamed over the old one, 0600 in a 0700 directory).
func Set(path, name string, v any) error {
	k, ok := Lookup(name)
	if !ok {
		return fmt.Errorf("%s: not a setting", name)
	}
	lit, err := literal(k, v)
	if err != nil {
		return err
	}
	want, why := k.file(decodeLiteral(lit))
	if why != "" {
		return &RefusedError{Key: name, Why: why}
	}
	text, old, err := readForEdit(path)
	if err != nil {
		return err
	}
	edited := setLine(text, name, lit)
	got, err := leavesOf(edited)
	if err != nil {
		return errNotReadBack
	}
	expect := cloneLeaves(old)
	expect[name] = nil // compared through the rule below
	if !sameExcept(expect, got, name) {
		return errNotReadBack
	}
	if v2, why := k.file(got[name]); why != "" || !reflect.DeepEqual(v2, want) {
		return errNotReadBack
	}
	return writeAtomic(path, edited)
}

// Unset removes key's line from the config.toml at path, every other byte
// kept; an absent key or file is no edit at all.
func Unset(path, name string) error {
	if _, ok := Lookup(name); !ok {
		return fmt.Errorf("%s: not a setting", name)
	}
	text, old, err := readForEdit(path)
	if err != nil {
		return err
	}
	if _, set := old[name]; !set {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	at, found := scanKeys(lines).key[name]
	if !found {
		return errNotReadBack
	}
	edited := strings.Join(append(lines[:at:at], lines[at+1:]...), "")
	got, err := leavesOf(edited)
	if err != nil {
		return errNotReadBack
	}
	expect := cloneLeaves(old)
	delete(expect, name)
	if !reflect.DeepEqual(expect, got) {
		return errNotReadBack
	}
	return writeAtomic(path, edited)
}

// literal is v as TOML writes it for k: a duration as its "5m" string (what
// fileDuration reads), anything else through the TOML encoder.
func literal(k *Key, v any) (string, error) {
	if k.Kind == Duration {
		d, ok := v.(time.Duration)
		if !ok {
			return "", &RefusedError{Key: k.Name, Why: "want a duration"}
		}
		v = Format(d)
	}
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(map[string]any{"v": v}); err != nil {
		return "", &RefusedError{Key: k.Name, Why: "not a " + k.Kind.String()}
	}
	lit := strings.TrimSpace(b.String())
	if !strings.HasPrefix(lit, "v = ") {
		return "", &RefusedError{Key: k.Name, Why: "not a " + k.Kind.String()}
	}
	return strings.TrimPrefix(lit, "v = "), nil
}

// decodeLiteral is lit as a reader decodes it (int64, float64, string, bool).
func decodeLiteral(lit string) any {
	var m map[string]any
	if _, err := toml.Decode("v = "+lit, &m); err != nil {
		return nil
	}
	return m["v"]
}

// readForEdit is the file's text and its decoded leaves; an absent file is
// empty, one that does not parse is refused.
func readForEdit(path string) (string, map[string]any, error) {
	if path == "" {
		return "", nil, errors.New("no config directory: neither XDG_CONFIG_HOME nor HOME is set")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", map[string]any{}, nil
	}
	if err != nil {
		return "", nil, errors.New("config.toml cannot be read")
	}
	leaves, err := leavesOf(string(data))
	if err != nil {
		var pe toml.ParseError
		line := 0
		if errors.As(err, &pe) {
			line = pe.Position.Line
		}
		return "", nil, &NotParsingError{Line: line}
	}
	return string(data), leaves, nil
}

func leavesOf(text string) (map[string]any, error) {
	var raw map[string]any
	if _, err := toml.Decode(text, &raw); err != nil {
		return nil, err
	}
	out := map[string]any{}
	flatten("", raw, out)
	return out, nil
}

func cloneLeaves(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// sameExcept reports whether got holds exactly expect's keys, with expect's
// values for all but name.
func sameExcept(expect, got map[string]any, name string) bool {
	if len(expect) != len(got) {
		return false
	}
	for k, v := range expect {
		g, ok := got[k]
		if !ok || k != name && !reflect.DeepEqual(v, g) {
			return false
		}
	}
	return true
}

// keyScan is where each `key = …` line and each [table] header is, as
// keyLines reads them, by index into the file's lines.
type keyScan struct {
	key     map[string]int // dotted key -> its line
	header  map[string]int // table -> its [table] line
	lastKey map[string]int // table -> its last key's line
}

func scanKeys(lines []string) keyScan {
	sc := keyScan{key: map[string]int{}, header: map[string]int{}, lastKey: map[string]int{}}
	table := ""
	for i, line := range lines {
		s := strings.TrimSpace(line)
		switch {
		case s == "" || strings.HasPrefix(s, "#"):
		case strings.HasPrefix(s, "[["):
			table = strings.TrimSpace(strings.Trim(s, "[]"))
		case strings.HasPrefix(s, "["):
			if end := strings.Index(s, "]"); end > 0 {
				table = strings.TrimSpace(s[1:end])
				if _, seen := sc.header[table]; !seen {
					sc.header[table] = i
				}
			}
		default:
			eq := strings.Index(s, "=")
			if eq <= 0 {
				continue
			}
			key := strings.Trim(strings.TrimSpace(s[:eq]), `"'`)
			if table != "" {
				key = table + "." + key
			}
			sc.key[key] = i
			sc.lastKey[table] = i
		}
	}
	return sc
}

// setLine is text with name's line set to lit: its own line replaced (its
// indentation and key kept), else a line added after its table's last key,
// else its table appended.
func setLine(text, name, lit string) string {
	var lines []string
	if text != "" {
		lines = strings.SplitAfter(text, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}
	sc := scanKeys(lines)
	if at, ok := sc.key[name]; ok {
		line := lines[at]
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		body := strings.TrimSpace(line)
		eq := strings.Index(body, "=")
		keyText := strings.TrimSpace(body[:eq])
		lines[at] = indent + keyText + " = " + lit + trailingComment(body[eq+1:]) + "\n"
		return strings.Join(lines, "")
	}
	dot := strings.LastIndex(name, ".")
	table, leaf := name[:dot], name[dot+1:]
	if hdr, ok := sc.header[table]; ok {
		at := hdr
		if last, ok := sc.lastKey[table]; ok && last > hdr {
			at = last
		}
		if !strings.HasSuffix(lines[at], "\n") {
			lines[at] += "\n"
		}
		out := append(append(append([]string{}, lines[:at+1]...), leaf+" = "+lit+"\n"), lines[at+1:]...)
		return strings.Join(out, "")
	}
	out := strings.Join(lines, "")
	if out != "" {
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += "\n"
	}
	return out + "[" + table + "]\n" + leaf + " = " + lit + "\n"
}

// trailingComment is the comment after a value, with the blanks before it
// ("" when none): the first '#' outside a basic or literal string.
func trailingComment(value string) string {
	var quote byte
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case quote == '"' && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			j := i
			for j > 0 && (value[j-1] == ' ' || value[j-1] == '\t') {
				j--
			}
			return strings.TrimRight(value[j:], "\r\n")
		}
	}
	return ""
}

// target is the file an edit of path writes: a symlinked config.toml
// (dotfiles) is followed so the link stays one; an absent file is path
// itself, and a dangling link is refused rather than replaced.
func target(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err == nil {
		return real, nil
	}
	if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("config.toml is a symlink to a missing file: fix the link by hand first")
	}
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	return "", errors.New("config.toml cannot be read")
}

// writeAtomic writes text to path (its symlink's target) through a temporary
// file in the same directory, renamed over it: a crash leaves the old file or
// the new one.
func writeAtomic(path, text string) error {
	path, err := target(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("the config directory cannot be created")
	}
	tmp, err := os.CreateTemp(dir, ".config.toml.*")
	if err != nil {
		return errors.New("config.toml cannot be written")
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op once renamed
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return errors.New("config.toml cannot be written")
	}
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return errors.New("config.toml cannot be written")
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return errors.New("config.toml cannot be written")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("config.toml cannot be written")
	}
	if err := os.Rename(name, path); err != nil {
		return errors.New("config.toml cannot be written")
	}
	return nil
}
