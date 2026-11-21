package bus

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// aclUpdate is a recording (aclrecord.go) to rebuild the inventory from.
var aclUpdate = flag.String("acl-update", "", "rebuild testdata/acl-inventory.json from this -tags aclrecord recording")

const aclInventoryFile = "testdata/acl-inventory.json"

// aclEntry is one command shape and the users that send it.
type aclEntry struct {
	Roles   []string `json:"roles"`
	Args    []string `json:"args"`
	Callers []string `json:"callers"` // the innermost production frames that sent it, at most 3
	Via     []string `json:"via,omitempty"`
}

// aclInventory is the committed inventory: what the suite's production
// callers send, and the callers no frame gives a role.
type aclInventory struct {
	Entries      []aclEntry `json:"entries"`
	Unattributed []string   `json:"unattributed"` // chains no role package decides
}

// aclOpen are the inventory's refusals that wait for a decision (reported
// to master, not guessed): each is logged, not failed, while it is still
// refused; one no longer refused fails, so the list is kept true.
var aclOpen = []struct{ role, caller, key, question string }{
	{ACLAgent, "cmd/agentbus.runRequest", "run", "publishRequest's script declares {p}:run, {p}:plan and {p}:plan:facts (bus/requests.go): any agent sends a request, and a script's keys need read and write: grant them, or read the plan outside the script?"},
	{ACLAgent, "cmd/agentbus.runPlan", "plan", "plan note, link, ref, done, ignore and the plan's unload (DEL) write {p}:plan: master's only, or any agent's?"},
	{ACLAgent, "cmd/agentbus.runRetire", "dispatch", "retire --forget fences its writes with the dispatch lease, so it declares {p}:dispatch: master writes the dispatcher's lease key, or the retire moves to the dispatcher?"},
	{ACLAgent, "cmd/agentbus.planLoad", "plan", "plan load writes {p}:plan and runPlan's load branch checks no role (reviewer-codex): master's only (a guard), or any agent's (question b)?"},
	{ACLAgent, "cmd/agentbus.runMission", "missionops", "agentbus mission queues the conductor's operations in {p}:missionops: who runs it, master or any agent?"},
}

// aclPinned are shapes production callers send that the recording misses
// (their tests call the bus function directly; foureyes), each with the user
// that sends it and the file that holds the call, checked to be there.
var aclPinned = []struct {
	role, file, call string
	args             []string
}{
	{ACLDispatcher, "../internal/dispatchsvc/healthchecks.go", ".Persistence(", []string{"config", "get", "appendonly"}},
	{ACLDispatcher, "../internal/dispatchsvc/healthchecks.go", ".Persistence(", []string{"config", "get", "appendfsync"}},
	{ACLDispatcher, "../internal/dispatchsvc/healthchecks.go", ".Persistence(", []string{"config", "get", "save"}},
	{ACLDispatcher, "../internal/dispatchsvc/healthchecks.go", ".RedisInfo(", []string{"info", "server", "memory"}},
	{ACLBusmon, "../cmd/busmon/about.go", ".RedisInfo(", []string{"info", "server", "memory"}},
	{ACLBusmon, "../cmd/busmon/redis_page.go", ".RedisDetail(", []string{"info", "memory", "persistence", "clients"}},
	{ACLBusmon, "../cmd/busmon/projects_admin.go", "ProjectMemory(", []string{"memory", "usage", "{p}:board"}},
	{ACLAdmin, "../backup/host.go", ".RedisInfo(", []string{"info", "server", "memory"}},
	{ACLAdmin, "../cmd/agentbus/restorecmd.go", ".RedisInfo(", []string{"info", "server", "memory"}},
}

func TestACLRoleOf(t *testing.T) {
	for _, c := range []struct {
		chain    []string
		identity string
		want     []string
	}{
		{[]string{"dispatch.(*loop).put", "dispatch.Run", "internal/dispatchsvc.Serve", "cmd/agentbus.main"}, "", []string{ACLDispatcher}},
		{[]string{"plan.Load", "cmd/agentbus.runReport", "cmd/agentbus.main"}, "coder", []string{ACLAgent}},
		{[]string{"plan.Load", "cmd/agentbus.planLoad", "cmd/agentbus.main"}, "coder", []string{ACLAgent}}, // unguarded: an agent may run it (reviewer-codex)
		{[]string{"cmd/agentbus.runReport"}, "master", []string{ACLAgent, ACLMaster}},
		{[]string{"cmd/busmon.(*model).refresh"}, "", []string{ACLBusmon}},
		{[]string{"backup.Run", "cmd/agentbus.runBackup", "cmd/agentbus.main"}, "coder", []string{ACLAdmin}},
		{[]string{"backup.Run", "cmd/agentbus.main"}, "coder", []string{ACLAgent}},
		{[]string{"backup.Run", "dispatch.(*loop).backupPass", "internal/dispatchsvc.Serve"}, "", []string{ACLDispatcher}},
		{[]string{"backup.Run"}, "", []string{ACLAdmin}},
		{[]string{"mergequeue.Next"}, "", nil},
		{[]string{"cmd/agentbus.popEnd", "cmd/agentbus.popEnd"}, "", []string{ACLDispatcher}},
		{[]string{"mission.Store.Change", "cmd/agentbus.missionStatus", "cmd/agentbus.runMission"}, "coder", []string{ACLAgent}},
		{[]string{"dispatch.(*loop).put", "dispatch.Run"}, "", []string{ACLDispatcher}},
		{[]string{"mission.Store.Changes", "cmd/agentbus.missionsActivity", "cmd/agentbus.serviceRun"}, "", []string{ACLDispatcher}},
		{[]string{"cmd/agentbus.planPriority"}, "", []string{ACLBusmon}},
		{[]string{"cmd/agentbus.runMergeDrop"}, "master", []string{ACLMaster}},
		{[]string{"cmd/agentbus.restoreBroker.Server", "backup.Restore", "cmd/agentbus.runRestore"}, "", []string{ACLAdmin}},
	} {
		if got := ACLRoleOf(c.chain, c.identity); !slices.Equal(got, c.want) {
			t.Errorf("%v as %q: %v, want %v", c.chain, c.identity, got, c.want)
		}
	}
}

// The table of spec step 4: master-only kinds only master writes, the
// services' leases only the dispatcher, everything else every data role;
// busmon and admin every key; ping nothing.
func TestACLRules(t *testing.T) {
	has := func(role, rule string) bool { return slices.Contains(ACLRules(role), rule) }
	for _, c := range []struct {
		role, rule string
		want       bool
	}{
		{ACLAgent, "~*:board", true}, {ACLAgent, "~*:gate:*", true}, {ACLAgent, "~*:pilot", false}, {ACLAgent, "~*:dispatch", false},
		{ACLMaster, "~*:pilot", true}, {ACLMaster, "~*:plan", true}, {ACLMaster, "~*:dispatch", false}, {ACLMaster, "+info", true},
		{ACLDispatcher, "~*:dispatch", true}, {ACLDispatcher, "~*:pilot", false}, {ACLDispatcher, "+config|get", true},
		{ACLAgent, "+info", false}, {ACLAgent, "-@dangerous", true},
		{ACLBusmon, "~*", true}, {ACLAdmin, "~*", true}, {ACLAdmin, "-flushall", true},
		{ACLPing, "+ping", true}, {ACLPing, "-@all", true},
	} {
		if got := has(c.role, c.rule); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.role, c.rule, got, c.want)
		}
	}
	for kind := range aclMasterOnly {
		if _, ok := keyKinds[kind]; !ok {
			t.Errorf("master-only %q is not a key kind", kind)
		}
	}
	for kind := range aclDispatcherOnly {
		if _, ok := keyKinds[kind]; !ok {
			t.Errorf("dispatcher-only %q is not a key kind", kind)
		}
	}
}

func TestShapeArgs(t *testing.T) {
	got := ShapeArgs([]any{"XADD", "demo:report", "MAXLEN", "~", 5000, "*", "msg", "a sentence: with spaces",
		"demo:gate:coder", "demo:nope", []byte("x/y:z"), "demo:swap:", "laptop-host", "7"})
	want := []string{"xadd", "{p}:report", "maxlen", "~", "1", "*", "x", "x", "{p}:gate:*", "{p}:?", "x", "{p}:swap:", "x", "7"}
	if !slices.Equal(got, want) {
		t.Fatalf("%q\nwant %q", got, want)
	}
	if got := ShapeArgs([]any{"client", "SETINFO", "lib-name", "go-redis"}); !slices.Equal(got, []string{"client", "setinfo", "x", "x"}) {
		t.Fatalf("a subcommand: %q", got)
	}
	again := make([]any, len(want))
	for i, w := range want {
		again[i] = w
	}
	if got := ShapeArgs(again); !slices.Equal(got, want) {
		t.Fatalf("not idempotent: %q", got)
	}
}

// TestACLInventory: every command shape the suite's production callers send
// passes ACL DRYRUN as each user that sends it, on the disposable broker. A
// refusal is either a correction of the table (bus/acl.go) or a caller that
// connects as another user. Rebuild the inventory with -acl-update (see
// aclrecord.go).
func TestACLInventory(t *testing.T) {
	if *aclUpdate != "" {
		writeACLInventory(t, *aclUpdate)
	}
	inv := readACLInventory(t)
	testredis.Configure(t)
	c, err := Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	user := func(role string) string { return "acltest-" + role }
	for _, role := range ACLRoles {
		args := []any{"ACL", "SETUSER", user(role), "reset", "on", "nopass"}
		for _, r := range ACLRules(role) {
			args = append(args, r)
		}
		if err := c.Do(ctx, args...).Err(); err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		t.Cleanup(func() { c.Do(ctx, "ACL", "DELUSER", user(role)) })
	}
	dry := func(role string, args []string) error {
		a := []any{"ACL", "DRYRUN", user(role)}
		for _, s := range args {
			a = append(a, s)
		}
		// A refusal is a plain reply naming the denied command or key, not an
		// error: only "OK" passes.
		got, err := c.Do(ctx, a...).Text()
		if err == nil && got != "OK" {
			err = errors.New(got)
		}
		return err
	}
	// What every client sends before any command: go-redis's handshake.
	for _, role := range []string{ACLAdmin, ACLMaster, ACLDispatcher, ACLAgent, ACLBusmon} {
		for _, args := range [][]string{{"hello", "3"}, {"client", "setinfo", "lib-name", "go-redis"}, {"ping"}} {
			if err := dry(role, args); err != nil {
				t.Errorf("%s: %v: %v", role, args, err)
			}
		}
	}
	if err := dry(ACLPing, []string{"ping"}); err != nil {
		t.Errorf("ping: %v", err)
	}
	if err := dry(ACLPing, []string{"get", "{p}:board"}); err == nil {
		t.Error("ping may GET")
	}
	var refused, unknown []string
	stillOpen := map[int]bool{}
	for _, e := range inv.Entries {
		for _, role := range e.Roles {
			err := dry(role, e.Args)
			if err != nil && strings.Contains(err.Error(), "ERR Command") && strings.Contains(err.Error(), "not found") {
				// a command this broker lacks (go-redis's maint_notifications
				// handshake on Valkey 7.2): nothing to check here
				unknown = append(unknown, strings.Join(e.Args[:min(2, len(e.Args))], " "))
				continue
			}
			if err != nil && openFinding(role, e.Callers, err) >= 0 {
				stillOpen[openFinding(role, e.Callers, err)] = true
				continue
			}
			if err != nil {
				refused = append(refused, fmt.Sprintf("%s: %s (%s): %v", role, strings.Join(e.Args, " "), strings.Join(e.Callers, ", "), err))
			}
		}
	}
	for _, p := range aclPinned {
		src, err := os.ReadFile(p.file)
		if err != nil || !strings.Contains(string(src), p.call) {
			t.Errorf("pinned: %s no longer calls %s: move or drop the pin", p.file, p.call)
		}
		if err := dry(p.role, p.args); err != nil {
			refused = append(refused, fmt.Sprintf("%s: %s (pinned, %s): %v", p.role, strings.Join(p.args, " "), p.file, err))
		}
	}
	if len(refused) > 0 {
		t.Errorf("%d refusal(s):\n%s", len(refused), strings.Join(refused, "\n"))
	}
	for i, o := range aclOpen {
		if !stillOpen[i] {
			t.Errorf("open finding no longer refused, drop it from aclOpen: %s %s %s", o.role, o.caller, o.key)
			continue
		}
		t.Logf("open: %s from %s on {p}:%s: %s", o.role, o.caller, o.key, o.question)
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		t.Logf("not checked, unknown to this broker: %s", strings.Join(slices.Compact(unknown), "; "))
	}
	if len(inv.Unattributed) > 0 {
		t.Logf("not checked, no role decides (%d): %s", len(inv.Unattributed), strings.Join(inv.Unattributed, "; "))
	}
	// The fenced scripts' own commands: ACL checks each at run time, DRYRUN
	// cannot. None may be one a data role lacks.
	for name, files := range scriptCommands(t) {
		info, err := c.Do(ctx, "COMMAND", "INFO", name).Slice()
		if err != nil || len(info) == 0 || info[0] == nil {
			t.Errorf("script command %q: %v", name, err)
			continue
		}
		if cats := fmt.Sprint(info[0]); strings.Contains(cats, "@dangerous") && !aclAdminOnly(files) {
			t.Errorf("a fenced script runs %q, which -@dangerous refuses to agent and master", name)
		}
	}
}

// TestACLInventoryGaps lists what the inventory cannot check (the spec's two
// gaps, reported, not failed): the exported bus/ functions no production
// caller reached in the suite, and the script calls whose key is not one of
// KEYS (ACL checks it at run time; DRYRUN of EVALSHA checks only KEYS).
func TestACLInventoryGaps(t *testing.T) {
	inv := readACLInventory(t)
	seen := map[string]bool{}
	for _, e := range inv.Entries {
		for _, v := range e.Via {
			seen[v] = true
		}
	}
	var unseen []string
	for _, fn := range exportedBusFuncs(t) {
		if !seen[fn] {
			unseen = append(unseen, fn)
		}
	}
	t.Logf("exported bus functions no production caller outside bus/ called in the suite (%d; one reached only through another bus function counts here): %s", len(unseen), strings.Join(unseen, " "))
	t.Logf("script calls whose key is not one of KEYS: %s", strings.Join(scriptNonKeyCalls(t), "; "))
}

// openFinding is the aclOpen entry a refusal is, -1 when none.
func openFinding(role string, callers []string, err error) int {
	for i, o := range aclOpen {
		if o.role == role && slices.Contains(callers, o.caller) && strings.Contains(err.Error(), "'{p}:"+o.key+"'") {
			return i
		}
	}
	return -1
}

func readACLInventory(t *testing.T) aclInventory {
	t.Helper()
	data, err := os.ReadFile(aclInventoryFile)
	if err != nil {
		t.Fatalf("%v (rebuild it: see aclrecord.go)", err)
	}
	var inv aclInventory
	if err := json.Unmarshal(data, &inv); err != nil {
		t.Fatal(err)
	}
	return inv
}

// writeACLInventory folds a recording into the inventory: one entry per
// (users, shape), its first three callers and the bus functions it went
// through, sorted so a rebuild diffs.
func writeACLInventory(t *testing.T, recording string) {
	t.Helper()
	f, err := os.Open(recording)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	byKey := map[string]*aclEntry{}
	unattributed := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var r ACLRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("%s: %v", recording, err)
		}
		shaped := make([]any, len(r.Args))
		for i, a := range r.Args {
			shaped[i] = a
		}
		r.Args = ShapeArgs(shaped) // the recording's shape under today's rules
		roles := ACLRoleOf(r.Chain, r.Identity)
		if roles == nil {
			unattributed[strings.Join(r.Chain, " < ")] = true
			continue
		}
		k := strings.Join(roles, ",") + "\x00" + strings.Join(r.Args, "\x00")
		e := byKey[k]
		if e == nil {
			e = &aclEntry{Roles: roles, Args: r.Args}
			byKey[k] = e
		}
		if len(e.Callers) < 3 && !slices.Contains(e.Callers, r.Chain[0]) {
			e.Callers = append(e.Callers, r.Chain[0])
		}
		if r.Via != "" && !slices.Contains(e.Via, r.Via) {
			e.Via = append(e.Via, r.Via)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	var inv aclInventory
	for _, e := range byKey {
		sort.Strings(e.Callers)
		sort.Strings(e.Via)
		inv.Entries = append(inv.Entries, *e)
	}
	sort.Slice(inv.Entries, func(i, j int) bool {
		a, b := inv.Entries[i], inv.Entries[j]
		if x, y := strings.Join(a.Args, " "), strings.Join(b.Args, " "); x != y {
			return x < y
		}
		return strings.Join(a.Roles, ",") < strings.Join(b.Roles, ",")
	})
	for u := range unattributed {
		inv.Unattributed = append(inv.Unattributed, u)
	}
	sort.Strings(inv.Unattributed)
	// One entry per line, so a rebuild's diff is one line per shape.
	var buf strings.Builder
	buf.WriteString("{\"entries\": [\n")
	for i, e := range inv.Entries {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(line)
		if i < len(inv.Entries)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
	}
	un, err := json.Marshal(inv.Unattributed)
	if err != nil {
		t.Fatal(err)
	}
	buf.WriteString("],\n\"unattributed\": " + string(un) + "}\n")
	if err := os.WriteFile(aclInventoryFile, []byte(buf.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// exportedBusFuncs is every exported function and method in bus/, as the
// recorder names them ("bus.(*Bus).Report", "bus.Connect").
func exportedBusFuncs(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, file := range busSources(t) {
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			name := "bus." + fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) == 1 {
				switch rt := fn.Recv.List[0].Type.(type) {
				case *ast.StarExpr:
					if id, ok := rt.X.(*ast.Ident); ok {
						name = "bus.(*" + id.Name + ")." + fn.Name.Name
					}
				case *ast.Ident:
					name = "bus." + rt.Name + "." + fn.Name.Name
				}
			}
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func busSources(t *testing.T) []*ast.File {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var out []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	return out
}

// scripts is each redis.NewScript body in bus/, by "file:line".
func scripts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	fset := token.NewFileSet()
	paths, _ := filepath.Glob("*.go")
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "NewScript" {
				return true
			}
			if body, ok := stringConst(call.Args[0]); ok {
				out[fset.Position(call.Pos()).String()] = body
			} else {
				out[fset.Position(call.Pos()).String()] = "" // built at run time: no static check
			}
			return true
		})
	}
	return out
}

// stringConst is a literal or a concatenation of literals.
func stringConst(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			s, err := strconv.Unquote(v.Value)
			return s, err == nil
		}
	case *ast.BinaryExpr:
		if v.Op == token.ADD {
			a, ok1 := stringConst(v.X)
			b, ok2 := stringConst(v.Y)
			return a + b, ok1 && ok2
		}
	case *ast.ParenExpr:
		return stringConst(v.X)
	}
	return "", false
}

var luaCall = regexp.MustCompile(`redis\.p?call\(\s*['"]([A-Za-z]+)['"]\s*(?:,\s*([^,)]+))?`)

// keyless are the commands whose first argument is not a key.
var keyless = map[string]bool{"time": true, "ping": true, "info": true, "scan": true}

// scriptCommands is every command the scripts call, lower case, with the
// files of the scripts that call it.
func scriptCommands(t *testing.T) map[string][]string {
	out := map[string][]string{}
	for pos, body := range scripts(t) {
		file, _, _ := strings.Cut(filepath.Base(pos), ":")
		for _, m := range luaCall.FindAllStringSubmatch(body, -1) {
			name := strings.ToLower(m[1])
			if !slices.Contains(out[name], file) {
				out[name] = append(out[name], file)
			}
		}
	}
	return out
}

// aclAdminOnly: every file is one whose scripts only admin runs (the
// logical restore, backup.Restore).
func aclAdminOnly(files []string) bool {
	for _, f := range files {
		if f != "backuprestore.go" {
			return false
		}
	}
	return true
}

// scriptNonKeyCalls are the script calls whose first argument is not
// KEYS[n], and the scripts built at run time, by "file:line".
func scriptNonKeyCalls(t *testing.T) []string {
	var out []string
	for pos, body := range scripts(t) {
		if body == "" {
			out = append(out, filepath.Base(pos)+" (built at run time)")
			continue
		}
		for _, m := range luaCall.FindAllStringSubmatch(body, -1) {
			if keyless[strings.ToLower(m[1])] || strings.HasPrefix(strings.TrimSpace(m[2]), "KEYS[") {
				continue
			}
			out = append(out, fmt.Sprintf("%s %s %s", filepath.Base(pos), strings.ToLower(m[1]), strings.TrimSpace(m[2])))
		}
	}
	sort.Strings(out)
	return out
}
