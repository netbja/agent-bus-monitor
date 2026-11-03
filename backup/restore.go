package backup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// RestoreOptions are restore's flags (spec "Restore").
type RestoreOptions struct {
	Projects    []string // none = every project the archive covers (the host's settings too)
	RedisOnly   bool
	FilesOnly   bool
	WithRuntime bool // runtime and unknown keys too
	DryRun      bool
	Force       bool   // replace the keys present and differing files
	Identity    string // the person's age identity file: secrets.age is opened only with it
}

// Broker is what restore needs of the broker.
type Broker interface {
	Version(ctx context.Context) (string, error)
	Keys(ctx context.Context, project string) ([]string, error)
	Restore(ctx context.Context, project string, keys []bus.DumpedKey, del []string) (int, error)
}

// RestoreDeps are restore's reads and acts on this host.
type RestoreDeps struct {
	Broker Broker
	// Live says why a project is live ("" = not): a lease held, its services
	// active, its herdr workspace open, a registered agent's pid alive. An
	// error is not proof of death: the project is refused.
	Live func(ctx context.Context, project string) (string, error)
	// Lock takes the project's up lock (no up or start beside the restore).
	Lock func(project string) (unlock func(), err error)
	// Confirm asks the person before --force deletes keys.
	Confirm func(project string, keys int) bool
	Loc     Locations
	Age     string // the age binary, for --identity
}

// Report is what a restore did.
type Report struct {
	Restored []string // projects whose part succeeded
	Refused  int      // projects or members refused
}

// Restore restores an archive (spec "Restore: never over a live project
// without --force"): first every member verified against the manifest
// (Verify), the broker's version checked, each project's lock taken; then,
// per project, the live checks and the Redis part in one script
// (RestoreProject); then the files of the projects whose Redis part
// succeeded, and the host's settings when no project is named. A refused
// project writes nothing. The error is for what stops everything (an archive
// refused, an older broker); a project refused is counted in the report.
func Restore(ctx context.Context, archive string, o RestoreOptions, d RestoreDeps, out io.Writer) (Report, error) {
	var rep Report
	f, err := os.Open(archive)
	if err != nil {
		return rep, err
	}
	defer f.Close()
	m, err := Verify(f)
	if err != nil {
		return rep, fmt.Errorf("archive refused, nothing written: %w", err)
	}
	scope := m.Projects
	if len(o.Projects) > 0 {
		for _, p := range o.Projects {
			if !slices.Contains(m.Projects, p) {
				return rep, fmt.Errorf("the archive does not cover %s (it covers %s)", p, strings.Join(m.Projects, ", "))
			}
		}
		scope = o.Projects
	}
	if !o.FilesOnly {
		v, err := d.Broker.Version(ctx)
		if err != nil {
			return rep, fmt.Errorf("the broker: %w", err)
		}
		if older(v, m.RedisVersion) {
			return rep, fmt.Errorf("the broker runs Redis %s, older than the archive's %s: a DUMP payload is refused by an older broker; upgrade the broker first", v, m.RedisVersion)
		}
	}
	// Pass 2: the Redis lines and the templates (for where roles go).
	redisLines := map[string][]redisLine{}
	templates := map[string][]byte{}
	if err := walkArchive(f, m, func(mem Member, body io.Reader) error {
		switch p, ok := redisMember(mem.Path); {
		case ok && slices.Contains(scope, p):
			lines, err := readRedis(body)
			if err != nil {
				return fmt.Errorf("%s: %w", mem.Path, err)
			}
			redisLines[p] = lines
		case strings.HasPrefix(mem.Path, "config/projects/") && strings.HasSuffix(mem.Path, ".toml"):
			b, err := io.ReadAll(io.LimitReader(body, 1<<20))
			if err != nil {
				return err
			}
			templates[strings.TrimSuffix(strings.TrimPrefix(mem.Path, "config/projects/"), ".toml")] = b
		}
		return nil
	}); err != nil {
		return rep, err
	}

	ok := map[string]bool{}
	refuse := func(p, why string) {
		fmt.Fprintf(out, "%s: refused (%s)\n", p, why)
		rep.Refused++
	}
	for _, p := range scope {
		unlock, err := d.Lock(p)
		if err != nil {
			refuse(p, "its up lock is held: "+err.Error())
			continue
		}
		defer unlock()
		why, err := d.Live(ctx, p)
		switch {
		case err != nil:
			refuse(p, "cannot tell whether it is live: "+err.Error())
			continue
		case why != "":
			refuse(p, "live: "+why)
			continue
		}
		if o.FilesOnly || restoreRedis(ctx, p, redisLines[p], o, d, out, refuse) {
			ok[p] = true
		}
	}

	// Pass 3: the files of the projects restored, the host's when unscoped.
	if !o.RedisOnly {
		if err := walkArchive(f, m, func(mem Member, body io.Reader) error {
			dest, project, host, why := destination(mem.Path, d.Loc, templates)
			switch {
			case strings.HasPrefix(mem.Path, "redis/") || mem.Path == CoverageName:
				return nil
			case mem.Path == SecretsName:
				rep.Refused += restoreSecrets(ctx, mem, body, o, d, ok, out)
				return nil
			case why != "":
				fmt.Fprintf(out, "file %s: skip (%s)\n", mem.Path, why)
				return nil
			case host && len(o.Projects) > 0:
				fmt.Fprintf(out, "file %s: skip (host setting: restored only without --project)\n", mem.Path)
				return nil
			case !host && !ok[project]:
				return nil // its project was refused: said above
			}
			action, err := restoreFile(dest, mem, body, func() error { return Verified(body) }, o)
			if err != nil {
				fmt.Fprintf(out, "file %s -> %s: refused (%v)\n", mem.Path, dest, err)
				rep.Refused++
				return nil
			}
			fmt.Fprintf(out, "file %s -> %s: %s\n", mem.Path, dest, action)
			return nil
		}); err != nil {
			return rep, err
		}
	}
	for _, p := range scope {
		if ok[p] {
			rep.Restored = append(rep.Restored, p)
		}
	}
	for _, l := range m.LeftOut {
		fmt.Fprintf(out, "not in the archive: %s (%s)\n", l.Path, l.Why)
	}
	if len(rep.Restored) > 0 && !o.DryRun {
		fmt.Fprintf(out, "next: agentbus up <project> for each project restored (%s); its services and agents write their runtime state again.\n",
			strings.Join(rep.Restored, ", "))
		for _, p := range rep.Restored {
			fmt.Fprintf(out, "  agentbus up %s\n", p)
		}
		if !o.FilesOnly {
			fmt.Fprintln(out, "the health records are the archive's: an old fail reads until the next health pass rewrites it.")
		}
	}
	return rep, nil
}

// walkArchive rewinds the archive and walks it against m.
func walkArchive(f *os.File, m Manifest, fn func(Member, io.Reader) error) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return Walk(f, m, fn)
}

// restoreRedis restores one project's keys; false when it was refused.
func restoreRedis(ctx context.Context, p string, lines []redisLine, o RestoreOptions, d RestoreDeps, out io.Writer, refuse func(p, why string)) bool {
	var keys []bus.DumpedKey
	for _, l := range lines {
		if l.Class != bus.KeyRecord.String() && !o.WithRuntime {
			fmt.Fprintf(out, "redis %s: skip (%s)\n", l.Key, l.Class)
			continue
		}
		keys = append(keys, bus.DumpedKey{Key: l.Key, Type: l.Type, Dump: l.Dump})
	}
	present, err := d.Broker.Keys(ctx, p)
	if err != nil {
		refuse(p, "cannot read its keys: "+err.Error())
		return false
	}
	var del []string
	if len(present) > 0 {
		if !o.Force {
			refuse(p, plural(len(present), "key")+" present; --force replaces them")
			return false
		}
		if !o.DryRun && !d.Confirm(p, len(present)) {
			refuse(p, "not confirmed")
			return false
		}
		del = present
	}
	for _, k := range keys {
		fmt.Fprintf(out, "redis %s: write\n", k.Key)
	}
	if o.DryRun {
		if len(del) > 0 {
			fmt.Fprintf(out, "%s: --force would delete %s first\n", p, plural(len(del), "key"))
		}
		return true
	}
	n, err := d.Broker.Restore(ctx, p, keys, del)
	if err != nil {
		refuse(p, err.Error())
		return false
	}
	fmt.Fprintf(out, "%s: %s restored\n", p, plural(n, "key"))
	// What appeared between the preflight and the script, neither restored
	// nor in the archive.
	if after, err := d.Broker.Keys(ctx, p); err == nil {
		restored := map[string]bool{}
		for _, l := range lines {
			restored[l.Key] = true
		}
		for _, k := range after {
			if !restored[k] {
				fmt.Fprintf(out, "%s: warning: %s is neither restored nor in the archive (written meanwhile)\n", p, k)
			}
		}
	}
	return true
}

func redisMember(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "redis/")
	if !ok || !strings.HasSuffix(rest, ".jsonl") {
		return "", false
	}
	p := strings.TrimSuffix(rest, ".jsonl")
	return p, bus.ValidName(p)
}

func readRedis(body io.Reader) ([]redisLine, error) {
	var out []redisLine
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1<<20), 512<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var l redisLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, sc.Err()
}

// destination maps a member to where it is restored: its project, or host
// for the person's settings; why says why it is not restored at all.
func destination(path string, loc Locations, templates map[string][]byte) (dest, project string, host bool, why string) {
	parts := strings.Split(path, "/")
	switch {
	case len(parts) == 2 && parts[0] == "config" && slices.Contains(configFiles, parts[1]):
		return filepath.Join(loc.ConfigDir, parts[1]), "", true, ""
	case len(parts) == 3 && parts[0] == "config" && parts[1] == "projects" && strings.HasSuffix(parts[2], ".toml"):
		p := strings.TrimSuffix(parts[2], ".toml")
		if !bus.ValidName(p) {
			return "", "", false, "not a project's template"
		}
		return filepath.Join(loc.ProjectsDir, parts[2]), p, false, ""
	case len(parts) >= 3 && parts[0] == "roles" && bus.ValidName(parts[1]):
		p := parts[1]
		ms := manifestsOf(templates[p])
		if len(ms) == 0 {
			return "", p, false, "the archive's template names no roles.toml"
		}
		// The path comes from the archive: only an absolute .../roles.toml,
		// so a crafted template cannot aim the write elsewhere.
		if !filepath.IsAbs(ms[0]) || filepath.Base(ms[0]) != "roles.toml" || filepath.Clean(ms[0]) != ms[0] {
			return "", p, false, "the template's ROLES_TOML is not an absolute .../roles.toml"
		}
		if len(parts) == 3 && parts[2] == "roles.toml" {
			return ms[0], p, false, ""
		}
		if len(parts) == 4 && parts[2] == "roles" && strings.HasSuffix(parts[3], ".md") {
			return filepath.Join(filepath.Dir(ms[0]), "roles", parts[3]), p, false, ""
		}
	case len(parts) >= 4 && parts[0] == "state" && bus.ValidName(parts[1]) && parts[2] == "history":
		return filepath.Join(append([]string{loc.StateDir(parts[1]), "history"}, parts[3:]...)...), parts[1], false, ""
	}
	return "", "", false, "not a member restore knows"
}

// restoreFile writes one member to dest (spec "Files"): absent written with
// the manifest's mode (folders 0700); identical skipped; differing kept, or
// with --force replaced through a temporary file renamed over it (over a
// symlink's target, so the link stays a link). The body is checked against
// the manifest before the rename: a member changed since Verify is never
// committed.
func restoreFile(dest string, mem Member, body io.Reader, verify func() error, o RestoreOptions) (string, error) {
	target := dest
	action := "write"
	if st, err := os.Lstat(dest); err == nil {
		if st.Mode()&fs.ModeSymlink != 0 {
			if target, err = filepath.EvalSymlinks(dest); err != nil {
				return "", err
			}
		}
		same, err := sameContent(target, mem)
		if err != nil {
			return "", err
		}
		switch {
		case same:
			return "skip (identical)", nil
		case !o.Force:
			return "keep (differs; --force replaces it)", nil
		}
		action = "replace"
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if o.DryRun {
		return action, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+"-restore-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, body); err != nil {
		tmp.Close()
		return "", err
	}
	if err := verify(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(fs.FileMode(mem.Mode).Perm()); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return action, os.Rename(tmp.Name(), target)
}

func sameContent(path string, mem Member) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return false, err
	}
	return n == mem.Size && hex.EncodeToString(h.Sum(nil)) == mem.SHA256, nil
}

// older reports whether version have is older than want in any part
// (major, minor, patch): a DUMP payload carries an RDB version that changes
// within a major. An unreadable version is older: fail closed.
func older(have, want string) bool {
	if want == "" {
		return false
	}
	h, w := versionParts(have), versionParts(want)
	if h == nil || w == nil {
		return true
	}
	for i := range 3 {
		if h[i] != w[i] {
			return h[i] < w[i]
		}
	}
	return false
}

func versionParts(v string) []int {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// restoreSecrets opens secrets.age with the person's identity (never without
// it) and restores each secret like a file, 0600: the password file only for
// an unscoped restore, a project's services env file only when its project
// was restored. The member is checked against the manifest before age reads
// it. It returns how many were refused.
func restoreSecrets(ctx context.Context, mem Member, body io.Reader, o RestoreOptions, d RestoreDeps, ok map[string]bool, out io.Writer) int {
	if o.Identity == "" {
		fmt.Fprintf(out, "file %s: skip (no --identity: the secrets stay encrypted)\n", SecretsName)
		return 0
	}
	sealed, err := io.ReadAll(io.LimitReader(body, 16<<20))
	if err == nil {
		err = Verified(body)
	}
	if err != nil {
		fmt.Fprintf(out, "file %s: refused (%v)\n", SecretsName, err)
		return 1
	}
	refused := 0
	err = OpenSecrets(ctx, d.Age, o.Identity, bytes.NewReader(sealed), func(name string, data []byte) error {
		var dest string
		switch p, isEnv := strings.CutPrefix(name, "services/"); {
		case name == "redis.env" && len(o.Projects) == 0:
			dest = d.Loc.PasswordFile
		case name == "redis.env":
			fmt.Fprintf(out, "secret %s: skip (host setting: restored only without --project)\n", name)
			return nil
		case isEnv && strings.HasSuffix(p, ".env") && bus.ValidName(strings.TrimSuffix(p, ".env")) && ok[strings.TrimSuffix(p, ".env")]:
			dest = d.Loc.EnvFile(strings.TrimSuffix(p, ".env"))
		default:
			fmt.Fprintf(out, "secret %s: skip (not a secret of a project restored)\n", name)
			return nil
		}
		if dest == "" {
			fmt.Fprintf(out, "secret %s: skip (no place for it here)\n", name)
			return nil
		}
		h := sha256.Sum256(data)
		m := Member{Path: name, Size: int64(len(data)), SHA256: hex.EncodeToString(h[:]), Mode: 0o600}
		action, err := restoreFile(dest, m, bytes.NewReader(data), func() error { return nil }, o)
		if err != nil {
			fmt.Fprintf(out, "secret %s -> %s: refused (%v)\n", name, dest, err)
			refused++
			return nil
		}
		fmt.Fprintf(out, "secret %s -> %s: %s\n", name, dest, action)
		return nil
	})
	if err != nil {
		fmt.Fprintf(out, "file %s: refused (%v)\n", SecretsName, err)
		refused++
	}
	return refused
}
