package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

func openGatesFixture(t *testing.T) *bus.Bus {
	t.Helper()
	b, _ := dialMain(t)
	ctx := context.Background()
	for _, g := range []struct{ agent, ref, meta string }{
		{"foureyes", "1793000000001-0", "master|[subject roles-impl] review roles"},
		{"foureyes", "1793000000002-0", "master|review the other one\n\nwith a second paragraph that runs well past sixty characters"},
		{"ui-kimi", "1793000000003-0", "master|[subject roles-impl] review roles"},
		{"ui-kimi", "1793000000004-0", "coder|please look"},
		{"foureyes", "1793000000005-0", "conductor|[subject roles-impl] mission review"},
	} {
		if err := b.OpenChallenge(ctx, g.agent, g.ref, g.meta); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func runGate(b *bus.Bus, self string, args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	code := runGateClose(context.Background(), b, self, args, &out, &errw)
	return code, out.String(), errw.String()
}

func TestGateCloseMineDryRunWritesNothing(t *testing.T) {
	b := openGatesFixture(t)
	code, out, errOut := runGate(b, "master", "--mine", "--dry-run")
	if code != 0 {
		t.Fatalf("code %d: %s", code, errOut)
	}
	want := "would close foureyes 1793000000001-0 (opened by master, subject roles-impl): review roles\n" +
		"would close foureyes 1793000000002-0 (opened by master): review the other one with a second paragraph that runs well …\n" +
		"would close ui-kimi 1793000000003-0 (opened by master, subject roles-impl): review roles\n" +
		"3 gate(s) would close; dry run, nothing written\n"
	if out != want {
		t.Fatalf("dry run:\n%s\nwant:\n%s", out, want)
	}
	all, _ := b.AllGates(context.Background())
	if len(all["foureyes"]) != 3 || len(all["ui-kimi"]) != 2 {
		t.Fatalf("dry run closed gates: %v", all)
	}
}

func TestGateCloseMineClosesOnlyTheChallengers(t *testing.T) {
	b := openGatesFixture(t)
	ctx := context.Background()
	if code, _, errOut := runGate(b, "master", "--mine"); code != 2 || !strings.Contains(errOut, "--note") {
		t.Fatalf("close without a note: %d %q", code, errOut)
	}
	code, out, errOut := runGate(b, "master", "--mine", "--note", "stale: batch merged")
	if code != 0 || !strings.HasSuffix(out, "3 gate(s) closed\n") {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	all, _ := b.AllGates(ctx)
	if len(all["foureyes"]) != 1 || all["foureyes"]["1793000000005-0"] == "" || len(all["ui-kimi"]) != 1 || all["ui-kimi"]["1793000000004-0"] == "" {
		t.Fatalf("left %v; want only coder's and the conductor's", all)
	}
	evs, _ := b.Thread(ctx, "1793000000001-0")
	if len(evs) != 1 || evs[0].From != "master" || !evs[0].NoWake || !strings.Contains(evs[0].Message, "stale: batch merged") {
		t.Fatalf("trace = %+v", evs)
	}
	if vs, _ := b.Verdicts(ctx, ""); len(vs) != 0 {
		t.Fatalf("a close wrote a verdict: %v", vs)
	}
}

func TestGateCloseOneRefusesSomeoneElsesGate(t *testing.T) {
	b := openGatesFixture(t)
	code, _, errOut := runGate(b, "coder", "foureyes", "1793000000001-0", "--note", "x")
	if code != 1 || !strings.Contains(errOut, "opened by master") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	code, out, _ := runGate(b, "coder", "ui-kimi", "1793000000004-0", "--note", "done in #135")
	if code != 0 || out != "closed ui-kimi 1793000000004-0 (opened by coder)\n" {
		t.Fatalf("own gate: %d %q", code, out)
	}
	if code, _, errOut := runGate(b, "coder", "ui-kimi", "nope", "--note", "x"); code != 1 || !strings.Contains(errOut, "no open gate") {
		t.Fatalf("unknown ref: %d %q", code, errOut)
	}
}

// A person (an explicit --as) may close any gate; the trace says so.
func TestGateCloseAsPerson(t *testing.T) {
	b := openGatesFixture(t)
	ctx := context.Background()
	code, out, errOut := runGate(b, "hermes", "--subject", "roles-impl", "--as", "Bernard", "--note", "reviewed in person")
	if code != 0 || !strings.HasSuffix(out, "2 gate(s) closed\n") {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	evs, _ := b.Thread(ctx, "1793000000003-0")
	if len(evs) != 1 || evs[0].From != "bernard" || !evs[0].Human {
		t.Fatalf("person trace = %+v", evs)
	}
	// Without --as, a subject sweep by a non-challenger closes nothing it did not open.
	b2 := openGatesFixture(t)
	code, out, _ = runGate(b2, "coder", "--subject", "roles-impl", "--note", "x")
	if code != 1 || !strings.Contains(out, "skipped foureyes 1793000000001-0: opened by master") {
		t.Fatalf("non-challenger sweep: %d %q", code, out)
	}
	all, _ := b2.AllGates(ctx)
	if len(all["foureyes"]) != 3 {
		t.Fatalf("closed someone else's gate: %v", all)
	}
	// --by lists another challenger's gates in a dry run.
	code, out, _ = runGate(b2, "coder", "--by", "master", "--dry-run")
	if code != 0 || !strings.Contains(out, "3 gate(s) would close") {
		t.Fatalf("--by dry run: %d %q", code, out)
	}
}

// The reviewer's verdict closes the gates opened on it for the subject or
// the ref, and reports them; a mission gate stays for the conductor.
func TestVerdictClosesTheReviewersGates(t *testing.T) {
	b := openGatesFixture(t)
	ctx := context.Background()
	lines := verdictGates(ctx, b, "coder", "foureyes", "roles-impl", "1793000000002-0")
	if len(lines) != 1 || lines[0] != "closed gate(s) on foureyes: 1793000000001-0 1793000000002-0" {
		t.Fatalf("lines = %q", lines)
	}
	open, _ := b.OpenChallenges(ctx, "foureyes")
	if len(open) != 1 || open["1793000000005-0"] == "" {
		t.Fatalf("foureyes keeps %v", open)
	}
	if open, _ := b.OpenChallenges(ctx, "ui-kimi"); len(open) != 2 {
		t.Fatalf("ui-kimi's gates moved on foureyes' verdict: %v", open)
	}
	// The mission review ref still gets the conductor notice.
	lines = verdictGates(ctx, b, "coder", "foureyes", "m", "1793000000005-0")
	if len(lines) != 1 || !strings.Contains(lines[0], "the conductor closes it") {
		t.Fatalf("mission lines = %q", lines)
	}
}
