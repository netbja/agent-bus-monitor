package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func runAuditCmd(b *bus.Bus, args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	code := runAudit(context.Background(), b, args, time.Now, &out, &errw)
	return code, out.String(), errw.String()
}

// audit spec §4: agentbus audit lists the trail newest first, filtered by
// action, actor and age; --json gives the entries.
func TestAuditCmdLists(t *testing.T) {
	b := dialDispatchMain(t)
	ctx := context.Background()
	for _, e := range []bus.AuditEntry{
		{Action: "approve", Actor: "foureyes", Target: "pr 761", Ref: "1795-0", Outcome: bus.AuditOK, Detail: "head 9a37b101"},
		{Action: "merge", Actor: "master", Target: "pr 761", Outcome: bus.AuditOK},
		{Action: "push", Actor: "master", Target: "dev", Outcome: bus.AuditRefused, Detail: "not fast-forward"},
	} {
		if _, err := b.Audit(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errOut := runAuditCmd(b)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 4 || !strings.Contains(lines[0], "ACTION") || !strings.Contains(lines[1], "push") ||
		!strings.Contains(lines[1], "refused") || !strings.Contains(lines[3], "approve") || !strings.Contains(lines[3], "1795-0") {
		t.Fatalf("code %d\n%s\n%s", code, out, errOut)
	}
	if _, out, _ = runAuditCmd(b, "--actor", "master", "--action", "merge"); strings.Count(out, "\n") != 2 || !strings.Contains(out, "merge") {
		t.Fatalf("filtered:\n%s", out)
	}
	_, out, _ = runAuditCmd(b, "--json")
	var got []bus.AuditEntry
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 3 || got[0].Action != "push" || got[2].Hash == "" {
		t.Fatalf("json %v %v:\n%s", got, err, out)
	}
	if _, out, _ = runAuditCmd(b, "--since", "1h"); strings.Count(out, "\n") != 4 {
		t.Fatalf("since 1h:\n%s", out)
	}
	if code, _, errOut := runAuditCmd(b, "--action", "delete"); code != 2 || !strings.Contains(errOut, "unknown action") {
		t.Fatalf("an unknown action filter: %d %q", code, errOut)
	}
	if code, _, _ := runAuditCmd(b, "--since", "yesterdayish"); code != 2 {
		t.Fatal("a bad --since accepted")
	}
}

// §4: export writes JSON Lines, oldest first, from --since (required).
func TestAuditCmdExport(t *testing.T) {
	b := dialDispatchMain(t)
	ctx := context.Background()
	for _, a := range []string{"merge", "push"} {
		if _, err := b.Audit(ctx, bus.AuditEntry{Action: a, Actor: "master", Outcome: bus.AuditOK}); err != nil {
			t.Fatal(err)
		}
	}
	if code, _, errOut := runAuditCmd(b, "export"); code != 2 || !strings.Contains(errOut, "--since") {
		t.Fatalf("export without --since: %d %q", code, errOut)
	}
	code, out, _ := runAuditCmd(b, "export", "--since", "7d")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 2 {
		t.Fatalf("export %d:\n%s", code, out)
	}
	var first bus.AuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil || first.Action != "merge" {
		t.Fatalf("first line %q: %v", lines[0], err)
	}
}

// Q3 B: verify checks the chain, exit 0 whole, 3 broken naming the entry,
// and says plainly what it cannot see.
func TestAuditCmdVerify(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()) })
	var ids []string
	for _, a := range []string{"merge", "push", "deliver"} {
		id, err := b.Audit(ctx, bus.AuditEntry{Action: a, Actor: "master", Outcome: bus.AuditOK})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	code, out, _ := runAuditCmd(b, "verify")
	if code != 0 || !strings.Contains(out, "3 entries") || !strings.Contains(out, "a cut of the tail is not detected") {
		t.Fatalf("whole: %d\n%s", code, out)
	}
	if err := r.XDel(ctx, bus.AuditKey(b.Project()), ids[1]).Err(); err != nil { // a middle entry deleted
		t.Fatal(err)
	}
	code, out, _ = runAuditCmd(b, "verify")
	if code != 3 || !strings.Contains(out, ids[2]) || !strings.Contains(out, "broken") {
		t.Fatalf("broken: %d\n%s", code, out)
	}
}
