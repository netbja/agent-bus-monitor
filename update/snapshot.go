package update

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/internal/files"
)

// The snapshot (spec §3.3): written before any change, under
// <state>/agentbus/updates/<id>/, its manifest written last; a snapshot
// without a manifest is incomplete and never restored.

// manifestFormat is the manifest's version. A rollback runs the binary that
// wrote the snapshot, so a newer binary never reads an older format.
const manifestFormat = 1

// Keep is how many snapshots stay after a successful update (the person's
// answer 5).
const Keep = 3

// Link is a symlink and its target.
type Link struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

// Service is a project service the update restarts, with the build it ran.
type Service struct {
	Project string `json:"project"`
	Kind    string `json:"kind"`
	Build   string `json:"build"`
}

// SkillsClone is the external skills clone before the update.
type SkillsClone struct {
	Home   string `json:"home"`
	Head   string `json:"head,omitempty"`
	Ours   bool   `json:"ours"`   // setup's marker was there
	Absent bool   `json:"absent"` // no clone yet: the rollback removes the one the update made
}

// Manifest is what a rollback needs.
type Manifest struct {
	Format      int         `json:"format"`
	From        string      `json:"from"`
	FromBuild   string      `json:"from_build"`
	To          string      `json:"to"`
	BinDir      string      `json:"bin_dir"`
	FromTree    string      `json:"from_tree"`
	FromHead    string      `json:"from_head"`
	AgentLaunch string      `json:"agent_launch,omitempty"` // the link's target, "" for no link
	Skills      SkillsClone `json:"skills"`
	SkillDirs   []string    `json:"skill_dirs"`
	SkillLinks  []Link      `json:"skill_links"`
	NewTree     string      `json:"new_tree"`
	Project     string      `json:"project,omitempty"`
	Services    []Service   `json:"services,omitempty"`
}

// Dir is <state>/agentbus/updates.
func Dir(state string) string { return filepath.Join(state, "agentbus", "updates") }

// NewID is the host's local time, then the from and to versions.
func NewID(now time.Time, from, to string) string {
	return now.Local().Format("20060102-150405") + "-" + from + "-" + to
}

// copyKeep copies src to dst keeping its mode and times (setup's backup, cp -p).
func copyKeep(src, dst string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, st.ModTime(), st.ModTime())
}

// WriteSnapshot copies the bin dir's two binaries into dir/bin, then writes
// the manifest last. A failure removes what it wrote: nothing changed yet.
func WriteSnapshot(dir string, m Manifest) (err error) {
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o700); err != nil {
		return err
	}
	for _, name := range []string{"agentbus", "busmon"} {
		if err := copyKeep(filepath.Join(m.BinDir, name), filepath.Join(dir, "bin", name)); err != nil {
			return fmt.Errorf("snapshot %s: %w", name, err)
		}
	}
	m.Format = manifestFormat
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return files.WriteAtomic(filepath.Join(dir, "manifest.json"), b)
}

// ReadManifest reads a complete snapshot's manifest; an incomplete one, or
// one of another format, is an error.
func ReadManifest(dir string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, fmt.Errorf("snapshot %s is incomplete (no manifest): it is never restored", filepath.Base(dir))
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("snapshot %s: %w", filepath.Base(dir), err)
	}
	if m.Format != manifestFormat {
		return m, fmt.Errorf("snapshot %s has format %d, this agentbus reads %d: run its own bin/agentbus", filepath.Base(dir), m.Format, manifestFormat)
	}
	return m, nil
}

// Snapshots are the complete snapshots' ids, oldest first.
func Snapshots(updates string) []string {
	ents, _ := os.ReadDir(updates)
	var ids []string
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(updates, e.Name(), "manifest.json")); err == nil {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

// HistoryLine is one line of <updates>/history.
type HistoryLine struct {
	Kind, From, To, At, Snapshot string
	OK                           bool
}

func (h HistoryLine) String() string {
	return fmt.Sprintf("%s from=%s to=%s at=%s ok=%t snapshot=%s", h.Kind, h.From, h.To, h.At, h.OK, h.Snapshot)
}

// AppendHistory adds one line to <updates>/history.
func AppendHistory(updates string, h HistoryLine) error {
	f, err := os.OpenFile(filepath.Join(updates, "history"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, h.String()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// History reads <updates>/history; a missing or unreadable file is empty.
func History(updates string) []HistoryLine {
	hs, _ := ReadHistory(updates)
	return hs
}

// ReadHistory is History that says when the file cannot be read: only a
// missing file is empty (an automatic pass then fails closed, reviewer-codex
// on quiet-update 4a).
func ReadHistory(updates string) ([]HistoryLine, error) {
	f, err := os.Open(filepath.Join(updates, "history"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []HistoryLine
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		h := HistoryLine{Kind: fields[0]}
		for _, kv := range fields[1:] {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "from":
				h.From = v
			case "to":
				h.To = v
			case "at":
				h.At = v
			case "ok":
				h.OK = v == "true"
			case "snapshot":
				h.Snapshot = v
			}
		}
		out = append(out, h)
	}
	return out, sc.Err()
}

// Interrupted is the last snapshot when no history line names it: an update
// stopped after its snapshot (killed), so the next one refuses and names
// --rollback (§3.4 step 0). "" when none.
func Interrupted(updates string) string {
	ids := Snapshots(updates)
	if len(ids) == 0 {
		return ""
	}
	last := ids[len(ids)-1]
	for _, h := range History(updates) {
		if h.Snapshot == last {
			return ""
		}
	}
	return last
}

// Live are the snapshots whose update succeeded and that no successful
// rollback has undone since, and the unfinished one (Interrupted: killed
// after its snapshot, which step 0 names for --rollback), oldest first: what
// --rollback undoes. A failed update rolled itself back already.
func Live(updates string) []string {
	live := map[string]bool{}
	if id := Interrupted(updates); id != "" {
		live[id] = true
	}
	for _, h := range History(updates) {
		switch {
		case h.Kind == "update" && h.OK:
			live[h.Snapshot] = true
		case h.Kind == "rollback" && h.OK:
			live[h.Snapshot] = false
		}
	}
	var out []string
	for _, id := range Snapshots(updates) {
		if live[id] {
			out = append(out, id)
		}
	}
	return out
}

// Prune keeps the last Keep complete snapshots and removes the older ones,
// after a successful update only (§3.3).
func Prune(updates string) error {
	ids := Snapshots(updates)
	var errs []error
	for len(ids) > Keep {
		errs = append(errs, os.RemoveAll(filepath.Join(updates, ids[0])))
		ids = ids[1:]
	}
	return errors.Join(errs...)
}

// ErrLocked: another update holds the lock.
var ErrLocked = errors.New("another agentbus update is running")

// Lock takes <updates>/lock without waiting (one update at a time); the
// returned func releases it. A killed process releases it with its file.
func Lock(updates string) (func(), error) {
	if err := os.MkdirAll(updates, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(updates, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
