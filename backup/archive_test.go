package backup

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

var t0 = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

// write builds an archive of members (path → content) for projects.
func write(t *testing.T, projects []string, members map[string]string, order ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, Coverage{CreatedAt: t0, Host: "h1", Projects: projects})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range order {
		c := members[p]
		if err := w.Add(p, 0o600, int64(len(c)), strings.NewReader(c)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(Manifest{Version: "dev", RedisVersion: "8.2.1",
		LeftOut: []LeftOut{{Path: "redis.env", Why: "secret (use --secrets)"}}}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// members lists an archive's tar members in order, with their contents.
func members(t *testing.T, b []byte) ([]string, map[string]string) {
	t.Helper()
	zr, err := zstd.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	var names []string
	got := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names, got
		}
		if err != nil {
			t.Fatal(err)
		}
		c, _ := io.ReadAll(tr)
		names = append(names, h.Name)
		got[h.Name] = string(c)
	}
}

func TestArchiveRoundTrip(t *testing.T) {
	in := map[string]string{"redis/demo.jsonl": `{"key":"demo:board"}` + "\n", "config/config.toml": "[backup]\n"}
	b := write(t, []string{"demo", "empty"}, in, "redis/demo.jsonl", "config/config.toml")
	names, got := members(t, b)
	if len(names) != 4 || names[0] != CoverageName || names[3] != ManifestName {
		t.Fatalf("members %q: coverage first, manifest last", names)
	}
	for p, c := range in {
		if got[p] != c {
			t.Errorf("%s = %q, want %q", p, got[p], c)
		}
	}
	m, err := Verify(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if m.Format != Format || m.Host != "h1" || !m.CreatedAt.Equal(t0) || strings.Join(m.Projects, ",") != "demo,empty" ||
		m.RedisVersion != "8.2.1" || len(m.LeftOut) != 1 {
		t.Fatalf("manifest %+v", m)
	}
	// Every member but the manifest is listed with its hash, coverage.json included.
	var listed []string
	for _, mem := range m.Members {
		listed = append(listed, mem.Path)
		if len(mem.SHA256) != 64 || mem.Mode != 0o600 {
			t.Errorf("member %+v", mem)
		}
	}
	if strings.Join(listed, ",") != CoverageName+",redis/demo.jsonl,config/config.toml" {
		t.Fatalf("listed %q", listed)
	}
	c, err := ReadCoverage(bytes.NewReader(b))
	if err != nil || strings.Join(c.Projects, ",") != "demo,empty" || c.Host != "h1" || !c.CreatedAt.Equal(t0) {
		t.Fatalf("ReadCoverage = %+v, %v", c, err)
	}
}

// The zstd frame carries its checksum: `zstd -t` accepts the file.
func TestArchiveIsAValidZstdFrame(t *testing.T) {
	zstdBin, err := exec.LookPath("zstd")
	if err != nil {
		t.Skip("no zstd on PATH")
	}
	path := filepath.Join(t.TempDir(), "a.tar.zst")
	if err := os.WriteFile(path, write(t, []string{"demo"}, map[string]string{"x": "y"}, "x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(zstdBin, "-t", path).CombinedOutput(); err != nil {
		t.Fatalf("zstd -t: %v %s", err, out)
	}
	// The checksum flag is in the frame header descriptor (bit 2).
	b, _ := os.ReadFile(path)
	if len(b) < 5 || b[4]&0x04 == 0 {
		t.Fatalf("frame header %x: no content checksum", b[:5])
	}
}

func TestVerifyRefuses(t *testing.T) {
	good := write(t, []string{"demo"}, map[string]string{"a": "alpha", "b": "beta"}, "a", "b")
	// retar rebuilds the archive, changing one member's content and keeping the manifest.
	retar := func(change func(name string, c []byte) []byte, drop string) []byte {
		names, got := members(t, good)
		var buf bytes.Buffer
		zw, _ := zstd.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		for _, n := range names {
			if n == drop {
				continue
			}
			c := change(n, []byte(got[n]))
			tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: int64(len(c)), Typeflag: tar.TypeReg})
			tw.Write(c)
		}
		tw.Close()
		zw.Close()
		return buf.Bytes()
	}
	same := func(_ string, c []byte) []byte { return c }
	for name, b := range map[string][]byte{
		"truncated": good[:len(good)/2],
		"corrupted": func() []byte { c := bytes.Clone(good); c[len(c)/2] ^= 0xff; return c }(),
		"a member changed": retar(func(n string, c []byte) []byte {
			if n == "a" {
				return []byte("ALPHA")
			}
			return c
		}, ""),
		"coverage changed": retar(func(n string, c []byte) []byte {
			if n == CoverageName {
				return bytes.Replace(c, []byte("demo"), []byte("demx"), 1)
			}
			return c
		}, ""),
		"a member missing": retar(same, "b"),
		"no manifest":      retar(same, ManifestName),
		"not zstd":         []byte("plain text"),
	} {
		if _, err := Verify(bytes.NewReader(b)); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
	// A member the manifest does not list is refused too.
	extra := retar(func(n string, c []byte) []byte { return c }, "")
	if _, err := Verify(bytes.NewReader(extra)); err != nil {
		t.Fatalf("an identical rebuild must verify: %v", err)
	}
}

func TestReadCoverageNeedsItFirst(t *testing.T) {
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, n := range []string{"x", CoverageName} {
		c := []byte(`{"format":1,"projects":["demo"]}`)
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: int64(len(c)), Typeflag: tar.TypeReg})
		tw.Write(c)
	}
	tw.Close()
	zw.Close()
	if _, err := ReadCoverage(bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("a coverage.json that is not the first member was read")
	}
}

func TestAddRefusesBadMembers(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, Coverage{CreatedAt: t0, Host: "h", Projects: []string{"demo"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", "/abs", "../up", "a/../../b", CoverageName, ManifestName, "a\\b"} {
		if err := w.Add(p, 0o600, 1, strings.NewReader("x")); err == nil {
			t.Errorf("Add(%q) accepted", p)
		}
	}
	if err := w.Add("dup", 0o600, 1, strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if err := w.Add("dup", 0o600, 1, strings.NewReader("x")); err == nil {
		t.Error("a duplicate member was accepted")
	}
	// A member shorter or longer than announced abandons the archive.
	for _, c := range []struct{ body string }{{"abc"}, {"abcdefg"}} {
		w, _ := NewWriter(&buf, Coverage{CreatedAt: t0, Host: "h", Projects: []string{"demo"}})
		if err := w.Add("m", 0o600, 5, strings.NewReader(c.body)); err == nil {
			t.Errorf("%q announced as 5 bytes was accepted", c.body)
		}
		if err := w.Add("n", 0o600, 1, strings.NewReader("x")); err == nil {
			t.Error("an Add after a failure was accepted")
		}
		if err := w.Close(Manifest{}); err == nil {
			t.Error("a Close after a failure was accepted")
		}
	}
}
