package mergequeue

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// withRollup is prJSON(n, head) with entries as its statusCheckRollup.
func withRollup(n int, head string, entries ...string) string {
	return strings.Replace(prJSON(n, head), `"mergeable":"MERGEABLE"`, `"mergeable":"MERGEABLE","statusCheckRollup":[`+strings.Join(entries, ",")+`]`, 1)
}

func checkRun(name, status, conclusion, completed string) string {
	return `{"__typename":"CheckRun","name":"` + name + `","status":"` + status + `","conclusion":"` + conclusion + `","detailsUrl":"https://ci/` + name + `","completedAt":"` + completed + `","workflowName":"CI"}`
}

// TestDeriveCIRed: the "CI red" mark (ci-loop §8 Q8) is read through a fake
// gh, from the rollup the pass already lists, red by §2's rule: a failed
// entry, each name's latest only, even while another check runs.
func TestDeriveCIRed(t *testing.T) {
	const hE = "ffffffffffffffffffffffffffffffffffffffff"
	const h6 = "6666666666666666666666666666666666666666"
	out := "[" + strings.Join([]string{
		withRollup(1, hA, checkRun("test", "COMPLETED", "FAILURE", "2026-11-20T10:00:00Z"), checkRun("build", "COMPLETED", "TIMED_OUT", "2026-11-20T10:00:00Z"),
			`{"__typename":"StatusContext","context":"jenkins","state":"SUCCESS","targetUrl":"https://j","createdAt":"2026-11-20T10:00:00Z"}`),
		withRollup(2, hB, checkRun("test", "COMPLETED", "FAILURE", "2026-11-20T10:00:00Z"), checkRun("test", "COMPLETED", "SUCCESS", "2026-11-20T11:00:00Z")), // re-run passed
		withRollup(3, hC, checkRun("lint", "COMPLETED", "FAILURE", "2026-11-20T10:00:00Z"), checkRun("e2e", "IN_PROGRESS", "", "")),                           // red while e2e runs
		withRollup(4, hD, checkRun("e2e", "IN_PROGRESS", "", ""), checkRun("ok", "COMPLETED", "CANCELLED", "2026-11-20T10:00:00Z")),                           // running, cancelled: not red
		withRollup(5, hE, `{"__typename":"StatusContext","context":"jenkins","state":"ERROR","targetUrl":"https://j","createdAt":"2026-11-20T10:00:00Z"}`),
		prJSON(6, h6), // no rollup at all
	}, ",") + "]"
	dir, env := fakeGH(t, out, 0)
	prs, err := ReadGitHub(context.Background(), dir, env, true)
	if err != nil {
		t.Fatal(err)
	}
	var ledger []bus.Verdict
	for i, h := range []string{hA, hB, hC, hD, hE, h6} {
		ledger = append(ledger, v(string(rune('1'+i))+"-0", "s"+string(rune('a'+i)), "foureyes", "approve", h))
	}
	got := Derive(Input{Verdicts: ledger, PRs: prs})
	want := map[int][]string{1: {"build", "test"}, 2: nil, 3: {"lint"}, 4: nil, 5: {"jenkins"}, 6: nil}
	if len(got.Items) != len(want) {
		t.Fatalf("items %+v", got.Items)
	}
	for _, it := range got.Items {
		if !slices.Equal(it.CIRed, want[it.Number]) {
			t.Errorf("#%d CIRed %q, want %q", it.Number, it.CIRed, want[it.Number])
		}
	}
}

// TestDeriveCIRedUnknown: no mark where the checks are not known: a cut
// rollup (ChecksCut, as the loop reads it), and a pass that did not ask for
// the rollup (ci.loop off: no new gh call is made for the mark).
func TestDeriveCIRedUnknown(t *testing.T) {
	failed := []Check{{Name: "test", Failed: true}}
	ledger := []bus.Verdict{v("1-0", "sa", "foureyes", "approve", hA), v("2-0", "sb", "foureyes", "approve", hB)}
	got := Derive(Input{Verdicts: ledger, PRs: []PR{
		{Number: 1, Head: hA, Checks: failed, ChecksCut: true},
		{Number: 2, Head: hB}, // read without the rollup
	}})
	for _, it := range got.Items {
		if it.CIRed != nil {
			t.Errorf("#%d CIRed %q, want none", it.Number, it.CIRed)
		}
	}
}
