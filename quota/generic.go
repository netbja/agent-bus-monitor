package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Generic readers (spec §5.3, §5.4): a file with fields, an external command
// that prints a Reading, and a provider declared unlimited. They let any AI in
// without code in agentbus.

// FieldWindow says where a window's numbers are in a JSON record.
type FieldWindow struct {
	Name     string            `toml:"name"`      // fixed name, or
	NameFrom string            `toml:"name_from"` // a field whose value is mapped through Names
	Names    map[string]string `toml:"names"`     // value → canonical name; an unmapped value is unknown
	UsedPct  string            `toml:"used_pct"`
	Ratio    bool              `toml:"ratio"` // used_pct is 0-1
	ResetsAt string            `toml:"resets_at"`
	Minutes  int               `toml:"minutes"`
}

// FileReader reads the newest matching record of a JSON or JSONL file.
type FileReader struct {
	Name       string // provider
	Kind       string // "json" or "jsonl-last"
	Path       string
	Match      map[string]string // field → value every candidate line must carry
	ObservedAt string            // field holding the record's time (RFC 3339 or Unix s/ms)
	Windows    []FieldWindow
	Now        func() time.Time
}

func (f FileReader) Provider() string { return f.Name }

// tailMax bounds how far back a jsonl-last reader looks.
const tailMax = 8 << 20

func (f FileReader) Read(ctx context.Context) (Reading, error) {
	now := f.Now()
	src := "file " + f.Kind
	var lines [][]byte
	switch f.Kind {
	case "json":
		b, err := os.ReadFile(f.Path)
		if err != nil {
			return Unknown(f.Name, src, "file not readable"), nil
		}
		lines = [][]byte{b}
	case "jsonl-last":
		lines = tailLines(f.Path, tailMax)
	default:
		return Unknown(f.Name, src, "unknown kind "+f.Kind), nil
	}
	for i := len(lines) - 1; i >= 0; i-- {
		var rec map[string]any
		if json.Unmarshal(lines[i], &rec) != nil || !f.matches(rec) {
			continue
		}
		r := Reading{Provider: f.Name, Source: src, CollectedAt: now.UnixMilli()}
		if t, ok := timeField(rec, f.ObservedAt); ok {
			r.ObservedAt = t.UnixMilli()
		} else if st, err := os.Stat(f.Path); err == nil {
			r.ObservedAt = st.ModTime().UnixMilli()
		}
		for _, fw := range f.Windows {
			w, ok := fw.extract(rec)
			if !ok {
				return Unknown(f.Name, src, "the newest matching record lacks a window field"), nil
			}
			r.Windows = append(r.Windows, w)
		}
		if err := r.Validate(); err != nil {
			return Unknown(f.Name, src, err.Error()), nil
		}
		return r, nil
	}
	return Unknown(f.Name, src, "no complete matching record"), nil
}

func (f FileReader) matches(rec map[string]any) bool {
	for k, v := range f.Match {
		got, ok := field(rec, k)
		if !ok || fmt.Sprint(got) != v {
			return false
		}
	}
	return true
}

func (fw FieldWindow) extract(rec map[string]any) (Window, bool) {
	w := Window{Name: fw.Name, Minutes: fw.Minutes}
	if fw.NameFrom != "" {
		v, ok := field(rec, fw.NameFrom)
		if !ok {
			return w, false
		}
		name, ok := fw.Names[fmt.Sprint(v)]
		if !ok {
			return w, false
		}
		w.Name = name
	}
	pct, ok := number(rec, fw.UsedPct)
	if !ok {
		return w, false
	}
	if fw.Ratio {
		pct *= 100
	}
	w.UsedPct = pct
	t, ok := timeField(rec, fw.ResetsAt)
	if !ok {
		return w, false
	}
	w.ResetsAt = t.Unix()
	return w, w.Name != ""
}

// field follows a dotted path through nested objects.
func field(rec map[string]any, path string) (any, bool) {
	var cur any = rec
	for _, k := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[k]; !ok {
			return nil, false
		}
	}
	return cur, cur != nil
}

func number(rec map[string]any, path string) (float64, bool) {
	v, ok := field(rec, path)
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

// timeField reads RFC 3339, or Unix seconds / milliseconds.
func timeField(rec map[string]any, path string) (time.Time, bool) {
	if path == "" {
		return time.Time{}, false
	}
	v, ok := field(rec, path)
	if !ok {
		return time.Time{}, false
	}
	switch t := v.(type) {
	case string:
		if p, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return p, true
		}
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			return unixAny(n), true
		}
	case float64:
		return unixAny(int64(t)), true
	}
	return time.Time{}, false
}

func unixAny(n int64) time.Time {
	if n > 1e12 {
		return time.UnixMilli(n)
	}
	return time.Unix(n, 0)
}

// CommandReader runs an executable that prints one Reading as JSON (spec §5.4).
type CommandReader struct {
	Name string
	Argv []string
	Now  func() time.Time
}

func (c CommandReader) Provider() string { return c.Name }

func (c CommandReader) Read(ctx context.Context) (Reading, error) {
	now := c.Now()
	src := "command"
	if len(c.Argv) == 0 {
		return Unknown(c.Name, src, "no argv"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.Command(c.Argv[0], c.Argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = io.Discard
	var out bytes.Buffer
	cmd.Stdout = &limitWriter{w: &out, n: 1 << 20}
	if err := cmd.Start(); err != nil {
		return Unknown(c.Name, src, "cannot start the command"), nil
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return Unknown(c.Name, src, "the command failed"), nil
		}
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return Unknown(c.Name, src, "the command timed out"), nil
	}
	var r Reading
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		return Unknown(c.Name, src, "the command printed no reading"), nil
	}
	r.Provider, r.Source, r.CollectedAt, r.Note = c.Name, src, now.UnixMilli(), ""
	if r.ObservedAt == 0 {
		r.ObservedAt = now.UnixMilli()
	}
	if err := r.Validate(); err != nil {
		return Unknown(c.Name, src, err.Error()), nil
	}
	return r, nil
}

type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if len(p) > l.n {
		return 0, fmt.Errorf("output too large")
	}
	l.n -= len(p)
	return l.w.Write(p)
}

// UnlimitedReader is a provider with no limit (a local model).
type UnlimitedReader struct{ Name string }

func (u UnlimitedReader) Provider() string { return u.Name }
func (u UnlimitedReader) Read(context.Context) (Reading, error) {
	return Reading{Provider: u.Name, Source: "unlimited", Unlimited: true}, nil
}
