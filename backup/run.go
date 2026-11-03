package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Source is one file a run archives: its member path and where it is read.
// An absent source is left out silently unless Required.
type Source struct {
	Member   string
	Path     string
	Required bool
}

// Plan is everything one run archives (spec "What an archive holds").
type Plan struct {
	Projects     []string // the run's scope, each covered even with nothing to save
	Host         string
	Now          time.Time
	Version      string // agentbus's
	RedisVersion string
	// Dump reads a project's keys at one instant (bus.DumpProject).
	Dump    func(ctx context.Context, project string) (bus.ProjectDump, error)
	Files   []Source
	LeftOut []LeftOut
	Notes   []string
	Secrets []byte // SealSecrets' ciphertext, the secrets.age member; nil = none

	beforeRead func(path string) // tests: runs between a file's stat and its read
}

// Result is a complete archive's name and what its run measured.
type Result struct {
	Name, Path string
	Projects   []string
	Keys       map[string]int           // keys archived per project
	Exec       map[string]time.Duration // how long each project's MULTI held the server
}

const archivePrefix = "agentbus-backup-"

var archiveRE = regexp.MustCompile(`^agentbus-backup-(.+)-([0-9]{8}T[0-9]{6}Z)\.tar\.zst$`)

const stampLayout = "20060102T150405Z"

// ArchiveName is agentbus-backup-<host>-<UTC yyyymmddThhmmssZ>.tar.zst; any
// character of host but a letter, a digit, '.' or '-' reads '-'.
func ArchiveName(host string, at time.Time) string {
	h := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			return r
		}
		return '-'
	}, host)
	if h == "" {
		h = "host"
	}
	return archivePrefix + h + "-" + at.UTC().Format(stampLayout) + ".tar.zst"
}

// parseArchiveName reads a complete archive's name: its host and time. A
// temporary name never matches.
func parseArchiveName(name string) (host string, at time.Time, ok bool) {
	m := archiveRE.FindStringSubmatch(name)
	if m == nil {
		return "", time.Time{}, false
	}
	at, err := time.Parse(stampLayout, m[2])
	if err != nil {
		return "", time.Time{}, false
	}
	return m[1], at, true
}

// redisLine is one key of redis/<project>.jsonl.
type redisLine struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Class string `json:"class"`
	Dump  []byte `json:"dump"` // base64 in the file
}

// Run writes one archive of p into dir (created 0700): a temporary file in
// dir, renamed to its final name only once complete, so a crash leaves no
// half archive under that name. Any failure removes the temporary file.
func Run(ctx context.Context, dir string, p Plan) (Result, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{}, err
	}
	name := ArchiveName(p.Host, p.Now)
	final := filepath.Join(dir, name)
	if _, err := os.Lstat(final); err == nil {
		return Result{}, fmt.Errorf("%s already exists", final)
	}
	tmp, err := os.CreateTemp(dir, "."+name+"-*.tmp")
	if err != nil {
		return Result{}, err
	}
	done := false
	defer func() {
		if !done {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return Result{}, err
	}
	res := Result{Name: name, Path: final, Projects: p.Projects, Keys: map[string]int{}, Exec: map[string]time.Duration{}}
	w, err := NewWriter(tmp, Coverage{CreatedAt: p.Now.UTC(), Host: p.Host, Projects: p.Projects})
	if err != nil {
		return Result{}, err
	}
	for _, project := range p.Projects {
		d, err := p.Dump(ctx, project)
		if err != nil {
			return Result{}, fmt.Errorf("redis %s: %w", project, err)
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		for _, k := range d.Keys {
			if err := enc.Encode(redisLine{Key: k.Key, Type: k.Type, Class: k.Class.String(), Dump: k.Dump}); err != nil {
				return Result{}, err
			}
		}
		if err := w.Add("redis/"+project+".jsonl", 0o600, int64(buf.Len()), &buf); err != nil {
			return Result{}, err
		}
		res.Keys[project], res.Exec[project] = len(d.Keys), d.Exec
	}
	for _, s := range p.Files {
		if err := addFile(w, s, p.beforeRead); err != nil {
			return Result{}, err
		}
	}
	if p.Secrets != nil {
		if err := w.Add(SecretsName, 0o600, int64(len(p.Secrets)), bytes.NewReader(p.Secrets)); err != nil {
			return Result{}, err
		}
	}
	if err := w.Close(Manifest{Version: p.Version, RedisVersion: p.RedisVersion, LeftOut: p.LeftOut, Notes: p.Notes}); err != nil {
		return Result{}, err
	}
	if err := tmp.Sync(); err != nil {
		return Result{}, err
	}
	if err := tmp.Close(); err != nil {
		return Result{}, err
	}
	// A link never replaces an existing name (an archive of the same second).
	if err := os.Link(tmp.Name(), final); err != nil {
		return Result{}, fmt.Errorf("archive %s: %w", final, err)
	}
	os.Remove(tmp.Name())
	done = true
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return res, nil
}

// addFile archives one regular file at the size it has when opened: a file
// growing meanwhile (a history day file) is cut there, never read past it.
func addFile(w *Writer, s Source, beforeRead func(string)) error {
	f, err := os.Open(s.Path)
	if errors.Is(err, fs.ErrNotExist) && !s.Required {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: %w", s.Member, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("%s: %w", s.Member, err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s: %s is not a regular file", s.Member, s.Path)
	}
	if beforeRead != nil {
		beforeRead(s.Path)
	}
	return w.Add(s.Member, st.Mode().Perm(), st.Size(), io.LimitReader(f, st.Size()))
}

// Archive is one complete archive of a host in a backup directory.
type Archive struct {
	Name, Path string
	Host       string
	At         time.Time
	Coverage   Coverage
	Err        error // its coverage could not be read: it covers nothing
}

// List returns host's complete archives in dir (the final-name pattern only),
// newest first, each with its coverage read from its first member.
func List(dir, host string) ([]Archive, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Archive
	for _, e := range entries {
		h, at, ok := parseArchiveName(e.Name())
		if !ok || !e.Type().IsRegular() || h != ArchiveHost(host) {
			continue
		}
		a := Archive{Name: e.Name(), Path: filepath.Join(dir, e.Name()), Host: h, At: at}
		if f, err := os.Open(a.Path); err != nil {
			a.Err = err
		} else {
			a.Coverage, a.Err = ReadCoverage(f)
			f.Close()
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// ArchiveHost is host as an archive name spells it.
func ArchiveHost(host string) string {
	h, _, _ := parseArchiveName(ArchiveName(host, time.Unix(0, 0)))
	return h
}

// NewestCovering is, per project, the newest archive of list (newest first)
// whose coverage names it. Freshness is per project: an archive of A says
// nothing about B.
func NewestCovering(list []Archive) map[string]Archive {
	out := map[string]Archive{}
	for _, a := range list {
		if a.Err != nil {
			continue
		}
		for _, p := range a.Coverage.Projects {
			if _, ok := out[p]; !ok {
				out[p] = a
			}
		}
	}
	return out
}

// Retain removes host's oldest archives in dir beyond keep (0 keeps every
// one), never one that is the newest covering some project, and never a file
// not named like an archive. It returns the names removed.
func Retain(dir, host string, keep int) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	list, err := List(dir, host)
	if err != nil {
		return nil, err
	}
	protected := map[string]bool{}
	for _, a := range NewestCovering(list) {
		protected[a.Name] = true
	}
	var removed []string
	for i, a := range list {
		if i < keep || protected[a.Name] {
			continue
		}
		if err := os.Remove(a.Path); err != nil {
			return removed, err
		}
		removed = append(removed, a.Name)
	}
	return removed, nil
}
