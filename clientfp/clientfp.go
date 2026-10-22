// Package clientfp tells whether a client installed on disk has changed since
// an agent started it (spec 2026-10-21-client-reload-design.md). It reads file
// metadata, symlink targets and a package.json version field only: it never
// runs a client, and never reads a configuration, a transcript or a credential.
package clientfp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// FileFP identifies one file's content as the filesystem sees it.
type FileFP struct {
	Path    string `json:"path"`
	Dev     uint64 `json:"dev"`
	Ino     uint64 `json:"ino"`
	Size    int64  `json:"size"`
	MtimeNs int64  `json:"mtime_ns"`
}

// Fingerprint is what an agent's client was when the launcher started it:
// the path it resolved on PATH, the file that path led to and, for a script
// client (Node), the package.json beside it. Label is a version for display
// only, where a layout gives one.
type Fingerprint struct {
	Path   string  `json:"path"`
	Target FileFP  `json:"target"`
	Pkg    *FileFP `json:"pkg,omitempty"`
	Label  string  `json:"label,omitempty"`
}

// Take fingerprints the client the path leads to now.
func Take(path string) (Fingerprint, error) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Fingerprint{}, err
	}
	t, err := fileFP(target)
	if err != nil {
		return Fingerprint{}, err
	}
	fp := Fingerprint{Path: path, Target: t, Label: label(target)}
	if script(target) {
		if pkg := findPackage(target); pkg != "" {
			if p, err := fileFP(pkg); err == nil {
				fp.Pkg = &p
				if v := packageVersion(pkg); v != "" {
					fp.Label = v
				}
			}
		}
	}
	return fp, nil
}

// Changed re-fingerprints the same path: changed when what it leads to is not
// the file (or package) it led to at launch. known is false when the path
// cannot be read now: unknown is never "changed".
func (fp Fingerprint) Changed() (changed, known bool) {
	now, err := Take(fp.Path)
	if err != nil {
		return false, false
	}
	if now.Target != fp.Target {
		return true, true
	}
	if (now.Pkg == nil) != (fp.Pkg == nil) || now.Pkg != nil && *now.Pkg != *fp.Pkg {
		return true, true
	}
	return false, true
}

// Hash is a short digest of what the fingerprint identifies (its path, target
// and package): the episode key of one installed version.
func (fp Fingerprint) Hash() string {
	b, _ := json.Marshal(struct {
		P string
		T FileFP
		K *FileFP
	}{fp.Path, fp.Target, fp.Pkg})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// Encode is the fingerprint as one JSON string (AGENT_BUS_CLIENT_FP, the
// snapshot's client_fp).
func (fp Fingerprint) Encode() string {
	b, _ := json.Marshal(fp)
	return string(b)
}

// Decode reads an encoded fingerprint.
func Decode(s string) (Fingerprint, error) {
	var fp Fingerprint
	if s == "" {
		return fp, errors.New("no fingerprint")
	}
	if err := json.Unmarshal([]byte(s), &fp); err != nil {
		return fp, err
	}
	if fp.Path == "" {
		return fp, errors.New("fingerprint without a path")
	}
	return fp, nil
}

func fileFP(path string) (FileFP, error) {
	st, err := os.Stat(path)
	if err != nil {
		return FileFP{}, err
	}
	fp := FileFP{Path: path, Size: st.Size(), MtimeNs: st.ModTime().UnixNano()}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		fp.Dev, fp.Ino = uint64(sys.Dev), uint64(sys.Ino)
	}
	return fp, nil
}

// script: a .js file or one starting with "#!" (only the first two bytes are read).
func script(path string) bool {
	if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".mjs") {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 2)
	n, _ := f.Read(b)
	return n == 2 && string(b) == "#!"
}

// findPackage is the package.json of the script's package: in its directory
// or up to three above it (bin/codex.js → ../package.json).
func findPackage(target string) string {
	d := filepath.Dir(target)
	for i := 0; i < 4; i++ {
		p := filepath.Join(d, "package.json")
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p
		}
		d = filepath.Dir(d)
	}
	return ""
}

// packageVersion reads only the version field of a package.json.
func packageVersion(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var p struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &p) != nil {
		return ""
	}
	return p.Version
}

var (
	versionRE = regexp.MustCompile(`^\d+(\.\d+)+$`)
	grokRE    = regexp.MustCompile(`^grok-(\d+(?:\.\d+)+)-`)
)

// label is a version read from a known layout: Claude Code's
// .../versions/<v>, Grok's grok-<v>-<os>-<arch>; "" otherwise.
func label(target string) string {
	base := filepath.Base(target)
	if filepath.Base(filepath.Dir(target)) == "versions" && versionRE.MatchString(base) {
		return base
	}
	if m := grokRE.FindStringSubmatch(base); m != nil {
		return m[1]
	}
	return ""
}

// ClaudeLayout reads a running Claude Code's executable (/proc/<pid>/exe's
// target, " (deleted)" included): running is its version, installed the
// highest version in the same versions directory. ok is false for any other
// layout. A pinned install with a newer version left on disk reads outdated:
// acceptable for a proposal that only asks.
func ClaudeLayout(exe string) (running, installed string, outdated, ok bool) {
	exe = strings.TrimSuffix(exe, " (deleted)")
	dir, base := filepath.Dir(exe), filepath.Base(exe)
	if filepath.Base(dir) != "versions" || !versionRE.MatchString(base) {
		return "", "", false, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", false, false
	}
	installed = base
	for _, e := range entries {
		if versionRE.MatchString(e.Name()) && newer(e.Name(), installed) {
			installed = e.Name()
		}
	}
	return base, installed, newer(installed, base), true
}

// newer compares dotted numeric versions.
func newer(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(as), len(bs)); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}
