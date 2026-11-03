// Package backup writes and reads agent-bus's backup archives (spec
// docs/superpowers/specs/2026-11-01-backup-design.md): one .tar.zst whose
// first member is coverage.json and whose last is manifest.json, every other
// member hashed on the way in and verified before anything is restored.
package backup

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Format is the archive format version written in coverage.json and
// manifest.json.
const Format = 1

// The two fixed members: coverage.json first, manifest.json last.
const (
	CoverageName = "coverage.json"
	ManifestName = "manifest.json"
)

// Coverage is coverage.json: what a run covered, readable from the archive's
// first bytes (spec "Coverage"). It holds no hash, so it is written first.
type Coverage struct {
	Format    int       `json:"format"`
	CreatedAt time.Time `json:"created_at"`
	Host      string    `json:"host"`
	Projects  []string  `json:"projects"`
}

// Member is one archive member as the manifest lists it.
type Member struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}

// LeftOut is what a run did not archive, and why (a secret without
// --secrets), so a restore can say what it cannot bring back.
type LeftOut struct {
	Path string `json:"path"`
	Why  string `json:"why"`
}

// Manifest is manifest.json, the last member.
type Manifest struct {
	Format       int       `json:"format"`
	CreatedAt    time.Time `json:"created_at"`
	Host         string    `json:"host"`
	Version      string    `json:"version"`       // agentbus's
	RedisVersion string    `json:"redis_version"` // the broker's INFO redis_version
	Projects     []string  `json:"projects"`
	Members      []Member  `json:"members"`
	LeftOut      []LeftOut `json:"left_out,omitempty"`
	Notes        []string  `json:"notes,omitempty"`
}

// Writer streams members into a .tar.zst: zstd at its default level, one
// encoder goroutine, the frame's checksum on (spec "What an archive holds").
type Writer struct {
	zw      *zstd.Encoder
	tw      *tar.Writer
	cov     Coverage
	members []Member
	seen    map[string]bool
	closed  bool
	err     error // the first failure: the tar may be mid-member, so every later call fails
}

// NewWriter starts an archive on w and writes coverage.json as its first
// member; cov.Format is set to Format.
func NewWriter(w io.Writer, cov Coverage) (*Writer, error) {
	zw, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(true))
	if err != nil {
		return nil, err
	}
	cov.Format = Format
	a := &Writer{zw: zw, tw: tar.NewWriter(zw), cov: cov, seen: map[string]bool{}}
	b, err := json.MarshalIndent(cov, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := a.add(CoverageName, 0o600, int64(len(b)), strings.NewReader(string(b))); err != nil {
		return nil, err
	}
	return a, nil
}

// validMember: a clean relative slash path, never a fixed member's name.
func validMember(p string) error {
	switch {
	case p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\"):
		return fmt.Errorf("member %q: not a relative slash path", p)
	case path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../"):
		return fmt.Errorf("member %q: not a clean path inside the archive", p)
	case p == CoverageName || p == ManifestName:
		return fmt.Errorf("member %q: reserved", p)
	}
	return nil
}

// Add streams one member of exactly size bytes from r, hashing it on the
// way. A member shorter or longer than size is refused, and any failure
// abandons the archive: every later Add and Close returns it.
func (a *Writer) Add(p string, mode fs.FileMode, size int64, r io.Reader) error {
	if err := validMember(p); err != nil {
		return err
	}
	return a.add(p, mode, size, r)
}

func (a *Writer) add(p string, mode fs.FileMode, size int64, r io.Reader) error {
	if a.err != nil {
		return a.err
	}
	if a.closed {
		return errors.New("archive closed")
	}
	if err := a.write(p, mode, size, r); err != nil {
		a.err = fmt.Errorf("archive abandoned: %w", err)
		return err
	}
	return nil
}

func (a *Writer) write(p string, mode fs.FileMode, size int64, r io.Reader) error {
	if a.seen[p] {
		return fmt.Errorf("member %q: already in the archive", p)
	}
	a.seen[p] = true
	if err := a.tw.WriteHeader(&tar.Header{Name: p, Mode: int64(mode.Perm()), Size: size,
		Typeflag: tar.TypeReg, ModTime: a.cov.CreatedAt}); err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(a.tw, h), io.LimitReader(r, size))
	if err != nil {
		return fmt.Errorf("member %q: %w", p, err)
	}
	if n != size {
		return fmt.Errorf("member %q: %d bytes read, %d announced", p, n, size)
	}
	if extra, _ := r.Read(make([]byte, 1)); extra > 0 {
		return fmt.Errorf("member %q: longer than the %d bytes announced", p, size)
	}
	a.members = append(a.members, Member{Path: p, Size: size, SHA256: hex.EncodeToString(h.Sum(nil)), Mode: uint32(mode.Perm())})
	return nil
}

// Close writes manifest.json, the last member, from m plus the coverage and
// every member's hash, then ends the tar and the zstd frame.
func (a *Writer) Close(m Manifest) error {
	if a.err != nil {
		return a.err
	}
	if a.closed {
		return errors.New("archive closed")
	}
	m.Format, m.CreatedAt, m.Host, m.Projects = Format, a.cov.CreatedAt, a.cov.Host, a.cov.Projects
	m.Members = a.members
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := a.add(ManifestName, 0o600, int64(len(b)), strings.NewReader(string(b))); err != nil {
		return err
	}
	a.closed = true
	if err := a.tw.Close(); err != nil {
		return err
	}
	return a.zw.Close()
}

// Verify is restore's first pass: it reads the archive to its end, takes the
// manifest (the last member) and checks every other member against it, size
// and SHA-256, coverage.json first, nothing missing or extra. Nothing may be
// written before it returns nil.
func Verify(r io.Reader) (Manifest, error) {
	zr, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return Manifest{}, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	var seen []Member
	var manifest []byte
	for i := 0; ; i++ {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("archive unreadable: %w", err)
		}
		if manifest != nil {
			return Manifest{}, fmt.Errorf("member %q after %s", hdr.Name, ManifestName)
		}
		if i == 0 && hdr.Name != CoverageName {
			return Manifest{}, fmt.Errorf("first member is %q, not %s", hdr.Name, CoverageName)
		}
		if hdr.Typeflag != tar.TypeReg {
			return Manifest{}, fmt.Errorf("member %q: not a regular file", hdr.Name)
		}
		if hdr.Name == ManifestName {
			if manifest, err = io.ReadAll(io.LimitReader(tr, 64<<20)); err != nil {
				return Manifest{}, fmt.Errorf("archive unreadable: %w", err)
			}
			continue
		}
		h := sha256.New()
		n, err := io.Copy(h, tr)
		if err != nil {
			return Manifest{}, fmt.Errorf("archive unreadable: %w", err)
		}
		seen = append(seen, Member{Path: hdr.Name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))})
	}
	if manifest == nil {
		return Manifest{}, fmt.Errorf("no %s: not a complete archive", ManifestName)
	}
	var m Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", ManifestName, err)
	}
	if m.Format != Format {
		return Manifest{}, fmt.Errorf("archive format %d, this agentbus reads %d", m.Format, Format)
	}
	if len(m.Members) != len(seen) {
		return Manifest{}, fmt.Errorf("the manifest lists %d members, the archive holds %d", len(m.Members), len(seen))
	}
	for i, s := range seen {
		w := m.Members[i]
		if w.Path != s.Path || w.Size != s.Size || w.SHA256 != s.SHA256 {
			return Manifest{}, fmt.Errorf("member %q does not match the manifest", s.Path)
		}
	}
	return m, nil
}

// ReadCoverage reads only the first member, which must be coverage.json: what
// the archive covers, without unpacking the rest. It proves nothing about the
// rest: a caller deciding coverage reads complete archives only (the final
// name), and restore verifies the whole with Verify.
func ReadCoverage(r io.Reader) (Coverage, error) {
	zr, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return Coverage{}, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	hdr, err := tr.Next()
	if err != nil {
		return Coverage{}, fmt.Errorf("archive unreadable: %w", err)
	}
	if hdr.Name != CoverageName {
		return Coverage{}, fmt.Errorf("first member is %q, not %s", hdr.Name, CoverageName)
	}
	var c Coverage
	if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&c); err != nil {
		return Coverage{}, fmt.Errorf("%s: %w", CoverageName, err)
	}
	if c.Format != Format {
		return Coverage{}, fmt.Errorf("archive format %d, this agentbus reads %d", c.Format, Format)
	}
	return c, nil
}
