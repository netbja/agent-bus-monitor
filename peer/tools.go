package peer

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
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/netbja/agent-bus-monitor/bus"
)

// ToolSpec is one OpenAI function tool.
type ToolSpec struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is a tool's name, description and JSON-schema parameters.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

func tool(name, desc string, props map[string]any, required ...string) ToolSpec {
	params := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		params["required"] = required
	}
	return ToolSpec{Type: "function", Function: ToolFunction{Name: name, Description: desc, Parameters: params}}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }

// ToolSpecs are the peer's tools, all read-only: no shell, no write, no URL.
func ToolSpecs() []ToolSpec {
	return []ToolSpec{
		tool("read_file", "Read a text file under the project root (64 KiB at most).",
			map[string]any{"path": str("Path relative to the project root"), "offset": num("First byte to read"), "limit": num("Bytes to read")}, "path"),
		tool("list_dir", "List the names in a directory under the project root.",
			map[string]any{"path": str("Directory relative to the project root; empty for the root")}),
		tool("grep", "Search files under the project root for a Go regular expression.",
			map[string]any{"pattern": str("Go regexp"), "path": str("File or directory relative to the root; empty for the root")}, "pattern"),
		tool("bus_thread", "Read a bus thread's entries (the last 20).",
			map[string]any{"id": str("Thread id: a message id or a ref")}, "id"),
		tool("bus_agents", "List the agents on the bus and their states.", map[string]any{}),
	}
}

// Tool limits (spec, "Tools, read-only").
const (
	readCap     = 64 << 10
	listCap     = 500
	grepMatches = 200
	grepBudget  = 1 << 20
	threadCap   = 20
)

// grepEntries bounds the entries one grep walks (SEC-LLM-5): the byte budget
// alone let a tree of many small or skipped files run past turn_timeout.
var grepEntries = 20000

// Tools runs the peer's read-only tools. Root is the project root with its
// symlinks resolved; Deny the role's effective Read denies; Thread and Agents
// read the bus.
type Tools struct {
	Root   string
	Deny   DenyMatcher
	Thread func(ctx context.Context, id string) ([]bus.Event, error)
	Agents func(ctx context.Context) (map[string]bus.AgentSnapshot, error)
}

// Run runs one tool call and returns its result for the model: text, or a
// line starting "refused:" (the root or a deny rule) or "error:".
func (t Tools) Run(ctx context.Context, name, args string) string {
	var a struct {
		Path    string `json:"path"`
		Offset  int64  `json:"offset"`
		Limit   int64  `json:"limit"`
		Pattern string `json:"pattern"`
		ID      string `json:"id"`
	}
	if strings.TrimSpace(args) != "" {
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "error: the arguments are not a JSON object: " + err.Error()
		}
	}
	switch name {
	case "read_file":
		return t.readFile(a.Path, a.Offset, a.Limit)
	case "list_dir":
		return t.listDir(a.Path)
	case "grep":
		return t.grep(ctx, a.Pattern, a.Path)
	case "bus_thread":
		return t.busThread(ctx, a.ID)
	case "bus_agents":
		return t.busAgents(ctx)
	}
	return "error: no tool named " + strconv.Quote(name) + " (read_file, list_dir, grep, bus_thread, bus_agents)"
}

// resolve keeps rel under the root: no absolute path, no "..", symlinks
// resolved and checked again, and neither the asked nor the resolved path
// denied.
func (t Tools) resolve(rel string) (string, string) {
	if filepath.IsAbs(rel) {
		return "", "refused: an absolute path; give a path relative to the project root"
	}
	for _, s := range strings.Split(filepath.ToSlash(rel), "/") {
		if s == ".." {
			return "", "refused: \"..\" leaves the project root"
		}
	}
	asked := filepath.Join(t.Root, rel)
	real, err := filepath.EvalSymlinks(asked)
	if err != nil {
		return "", "error: no such file or directory" // never a link's target
	}
	if real != t.Root && !strings.HasPrefix(real, t.Root+string(filepath.Separator)) {
		return "", "refused: the path leads outside the project root"
	}
	if t.Deny.Denied(asked) || t.Deny.Denied(real) {
		return "", "refused: a Read deny rule of this role matches it"
	}
	if inGit(t.Root, asked) || inGit(t.Root, real) {
		return "", "refused: the repository's .git is not read"
	}
	return real, ""
}

// inGit reports whether p, under root, is in or is a .git: its config can
// hold a remote URL with a token (SEC-LLM-4).
func inGit(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return true
	}
	for _, s := range strings.Split(filepath.ToSlash(rel), "/") {
		if s == ".git" {
			return true
		}
	}
	return false
}

func (t Tools) readFile(rel string, offset, limit int64) string {
	if rel == "" {
		return "error: path is required"
	}
	p, msg := t.resolve(rel)
	if msg != "" {
		return msg
	}
	f, msg := openRegular(t.Root, p)
	if msg != "" {
		return msg
	}
	defer f.Close()
	if offset < 0 {
		offset = 0
	}
	capped := limit <= 0 || limit > readCap
	if capped {
		limit = readCap
	}
	buf := make([]byte, limit+1)
	n, err := f.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return "error: cannot read it"
	}
	if int64(n) > limit && capped {
		return string(buf[:limit]) + fmt.Sprintf("\n[truncated at %d bytes; read on with offset %d]", limit, offset+limit)
	}
	return string(buf[:min(int64(n), limit)])
}

func (t Tools) listDir(rel string) string {
	p, msg := t.resolve(rel)
	if msg != "" {
		return msg
	}
	if beforeOpen != nil {
		beforeOpen()
	}
	// The last component without O_DIRECTORY: a link there is ELOOP, where
	// O_DIRECTORY would make it ENOTDIR, like a plain file.
	d, err := openBeneath(t.Root, p, unix.O_NONBLOCK)
	switch {
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOTDIR):
		return refusedSwap
	case err != nil:
		return "error: not a directory"
	}
	defer d.Close()
	if fi, err := d.Stat(); err != nil || !fi.IsDir() {
		return "error: not a directory"
	}
	entries, err := d.ReadDir(-1)
	if err != nil {
		return "error: not a directory"
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var b strings.Builder
	for i, e := range entries {
		if i == listCap {
			fmt.Fprintf(&b, "[%d more not listed]\n", len(entries)-listCap)
			break
		}
		b.WriteString(e.Name())
		if e.IsDir() {
			b.WriteByte('/')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (t Tools) grep(ctx context.Context, pattern, rel string) string {
	if pattern == "" {
		return "error: pattern is required"
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "error: not a Go regexp: " + err.Error()
	}
	start, msg := t.resolve(rel)
	if msg != "" {
		return msg
	}
	var b strings.Builder
	matches, budget, entries := 0, int64(grepBudget), 0
	stop, tooMany := errors.New("stop"), errors.New("entries")
	walkErr := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err() // the turn's clock: the lease is held meanwhile
		}
		if entries++; entries > grepEntries {
			return tooMany
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		relp, _ := filepath.Rel(t.Root, p)
		real, msg := t.resolve(relp) // a symlink out of the root or onto a denied file is skipped
		if msg != "" {
			return nil
		}
		f, msg := openRegular(t.Root, real)
		if msg != "" {
			return nil // a pipe, a device, a socket: never opened
		}
		defer f.Close()
		data, _ := io.ReadAll(io.LimitReader(f, budget))
		budget -= int64(len(data))
		if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			return nil // binary
		}
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) {
				if matches == grepMatches {
					return stop
				}
				matches++
				fmt.Fprintf(&b, "%s:%d: %s\n", filepath.ToSlash(relp), i+1, cutLine(line))
			}
		}
		if budget <= 0 {
			return stop
		}
		return nil
	})
	switch {
	case ctx.Err() != nil:
		b.WriteString("[stopped: the turn ended]\n")
	case errors.Is(walkErr, tooMany):
		fmt.Fprintf(&b, "[stopped after %d entries]\n", grepEntries)
	case errors.Is(walkErr, stop) && matches == grepMatches:
		fmt.Fprintf(&b, "[stopped at %d matches]\n", grepMatches)
	case errors.Is(walkErr, stop):
		fmt.Fprintf(&b, "[stopped after reading %d bytes]\n", grepBudget)
	}
	if matches == 0 && b.Len() == 0 {
		return "no match"
	}
	return b.String()
}

func (t Tools) busThread(ctx context.Context, id string) string {
	if id == "" {
		return "error: id is required"
	}
	evs, err := t.Thread(ctx, id)
	if err != nil {
		return "error: the thread cannot be read"
	}
	return ThreadText(evs)
}

// ThreadText renders a thread's last entries, one per line, capped at 8 KiB:
// what a turn shows the model and what bus_thread returns.
func ThreadText(evs []bus.Event) string {
	if len(evs) > threadCap {
		evs = evs[len(evs)-threadCap:]
	}
	var b strings.Builder
	for _, e := range evs {
		fmt.Fprintf(&b, "%s %s -> %s [%s]: %s\n", e.ID, e.From, e.Target, e.Type, e.Message)
	}
	s := b.String()
	if len(s) > 8<<10 {
		s = "[older lines cut]\n" + s[len(s)-8<<10:]
	}
	if s == "" {
		return "no entry"
	}
	return s
}

func (t Tools) busAgents(ctx context.Context) string {
	snaps, err := t.Agents(ctx)
	if err != nil {
		return "error: the agents cannot be read"
	}
	names := make([]string, 0, len(snaps))
	for n := range snaps {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		s := snaps[n]
		msg := s.Message
		if len(msg) > 120 {
			msg = msg[:120]
		}
		fmt.Fprintf(&b, "%s %s %s\n", n, s.State, msg)
	}
	return b.String()
}

// beforeOpen runs between a path's checks and its open: a test's hook to
// swap the path for a link there (R1).
var beforeOpen func()

// refusedSwap is a path that became a link after it was checked.
const refusedSwap = "refused: the path changed after it was checked (a link appeared)"

// openBeneath opens p, a path under root that resolve has checked, one
// component at a time from root, following no link (O_NOFOLLOW on each):
// a component swapped for a link after the checks fails with ELOOP instead
// of leading outside the root or onto a denied file (R1). It holds on Linux
// and macOS alike, needing no openat2.
func openBeneath(root, p string, flags int) (*os.File, error) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, fmt.Errorf("not under the root")
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	parts := split(filepath.ToSlash(rel))
	if rel == "." {
		parts = nil
	}
	for i, part := range parts {
		fl := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_DIRECTORY
		if i == len(parts)-1 {
			fl = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | flags
		}
		next, err := unix.Openat(fd, part, fl, 0)
		unix.Close(fd)
		if err != nil {
			return nil, err
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), p), nil
}

// openRegular opens a regular file only, beneath root (openBeneath). A named
// pipe would block the open until a writer comes, past the turn's clock and
// with the receiver lease held: the open never blocks (O_NONBLOCK) and the
// open file's type is checked.
func openRegular(root, p string) (*os.File, string) {
	if beforeOpen != nil {
		beforeOpen()
	}
	f, err := openBeneath(root, p, unix.O_NONBLOCK)
	switch {
	case errors.Is(err, unix.ELOOP):
		return nil, refusedSwap
	case errors.Is(err, unix.ENOTDIR):
		return nil, refusedSwap // a directory on the path is no longer one
	case err != nil:
		return nil, "error: cannot open it"
	}
	fi, err := f.Stat()
	switch {
	case err != nil:
		f.Close()
		return nil, "error: not a regular file"
	case fi.IsDir():
		f.Close()
		return nil, "error: a directory (use list_dir)"
	case !fi.Mode().IsRegular():
		f.Close()
		return nil, "error: not a regular file"
	}
	return f, ""
}

// lineCap bounds one printed grep line: a minified file's one line never
// fills a result.
const lineCap = 300

func cutLine(s string) string {
	if len(s) <= lineCap {
		return s
	}
	cut := lineCap
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + " [line cut]"
}
