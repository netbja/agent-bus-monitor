package mergequeue

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The rollup entries below have the fields gh 2.104 gives (read off real
// rollups at build time, spec ci-loop §2): a CheckRun carries workflowName
// (empty for a check run another app posts), a StatusContext carries
// startedAt, the graphql base read gives createdAt and the workflow through
// checkSuite.
func rollupOf(t *testing.T, js string) []Check {
	t.Helper()
	var raw []ghCheck
	if err := json.Unmarshal([]byte(js), &raw); err != nil {
		t.Fatal(err)
	}
	return parseChecks(raw)
}

func names(cs []Check) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func TestClassifyEachStatusAndConclusion(t *testing.T) {
	cases := []struct {
		entry           string
		failed, running bool
	}{
		{`{"__typename":"CheckRun","name":"x","status":"COMPLETED","conclusion":"FAILURE"}`, true, false},
		{`{"__typename":"CheckRun","name":"x","status":"COMPLETED","conclusion":"TIMED_OUT"}`, true, false},
		{`{"__typename":"CheckRun","name":"x","status":"COMPLETED","conclusion":"STARTUP_FAILURE"}`, true, false},
		{`{"__typename":"CheckRun","name":"x","status":"COMPLETED","conclusion":"SUCCESS"}`, false, false},
		{`{"__typename":"CheckRun","name":"x","status":"COMPLETED","conclusion":"NEUTRAL"}`, false, false},
		{`{"__typename":"CheckRun","name":"x","status":"COMPLETED","conclusion":"SKIPPED"}`, false, false},
		{`{"__typename":"CheckRun","name":"x","status":"COMPLETED","conclusion":"CANCELLED"}`, false, false},
		{`{"__typename":"CheckRun","name":"x","status":"COMPLETED","conclusion":"ACTION_REQUIRED"}`, false, false},
		{`{"__typename":"CheckRun","name":"x","status":"IN_PROGRESS","conclusion":""}`, false, true},
		{`{"__typename":"CheckRun","name":"x","status":"QUEUED","conclusion":""}`, false, true},
		{`{"__typename":"CheckRun","name":"x","status":"PENDING","conclusion":""}`, false, true},
		{`{"__typename":"StatusContext","context":"x","state":"FAILURE"}`, true, false},
		{`{"__typename":"StatusContext","context":"x","state":"ERROR"}`, true, false},
		{`{"__typename":"StatusContext","context":"x","state":"SUCCESS"}`, false, false},
		{`{"__typename":"StatusContext","context":"x","state":"PENDING"}`, false, true},
		{`{"__typename":"StatusContext","context":"x","state":"EXPECTED"}`, false, true},
	}
	for _, c := range cases {
		r := Classify(rollupOf(t, "["+c.entry+"]"))
		if got := len(r.Failing) == 1; got != c.failed || r.Running != c.running {
			t.Errorf("%s: failed %v running %v, want %v %v", c.entry, got, r.Running, c.failed, c.running)
		}
	}
}

func TestClassifyFailingSortedWithURLAndKind(t *testing.T) {
	r := Classify(rollupOf(t, `[
	 {"__typename":"StatusContext","context":"jenkins/pr","state":"FAILURE","targetUrl":"https://ci.example/job/1","startedAt":"2026-11-19T05:00:00Z"},
	 {"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://github.com/o/r/actions/runs/1/job/2","workflowName":"CI"},
	 {"__typename":"CheckRun","name":"cla","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://cla.example/about","workflowName":""},
	 {"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS","workflowName":"CI"}]`))
	if r.Running {
		t.Fatal("nothing runs")
	}
	want := []Check{
		{Name: "build", URL: "https://github.com/o/r/actions/runs/1/job/2", Actions: true, Failed: true},
		{Name: "cla", URL: "https://cla.example/about", Failed: true},
		{Name: "jenkins/pr", URL: "https://ci.example/job/1", Failed: true},
	}
	for i := range r.Failing {
		r.Failing[i].At = want[i].At
	}
	if !reflect.DeepEqual(r.Failing, want) {
		t.Fatalf("failing %+v, want %+v", r.Failing, want)
	}
}

// A pending check holds the PR even beside a failed one (§2): Running.
func TestClassifyRunningBesideFailed(t *testing.T) {
	r := Classify(rollupOf(t, `[
	 {"__typename":"CheckRun","name":"a","status":"COMPLETED","conclusion":"FAILURE"},
	 {"__typename":"CheckRun","name":"b","status":"IN_PROGRESS"}]`))
	if !r.Running || !reflect.DeepEqual(names(r.Failing), []string{"a"}) {
		t.Fatalf("%+v", r)
	}
}

// Duplicate names: the latest entry wins, by completion, else start (a
// check run), by its start or creation (a status).
func TestClassifyLatestOfTwoSameNamedWins(t *testing.T) {
	r := Classify(rollupOf(t, `[
	 {"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS","startedAt":"2026-11-19T06:00:00Z","completedAt":"2026-11-19T06:05:00Z"},
	 {"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE","startedAt":"2026-11-19T05:00:00Z","completedAt":"2026-11-19T05:05:00Z"},
	 {"__typename":"StatusContext","context":"j","state":"FAILURE","startedAt":"2026-11-19T05:00:00Z"},
	 {"__typename":"StatusContext","context":"j","state":"SUCCESS","startedAt":"2026-11-19T06:00:00Z"},
	 {"__typename":"StatusContext","context":"k","state":"SUCCESS","createdAt":"2026-11-19T05:00:00Z"},
	 {"__typename":"StatusContext","context":"k","state":"ERROR","createdAt":"2026-11-19T06:00:00Z"},
	 {"__typename":"CheckRun","name":"r","status":"IN_PROGRESS","startedAt":"2026-11-19T07:00:00Z"},
	 {"__typename":"CheckRun","name":"r","status":"COMPLETED","conclusion":"FAILURE","startedAt":"2026-11-19T06:00:00Z","completedAt":"2026-11-19T06:01:00Z"}]`))
	if !reflect.DeepEqual(names(r.Failing), []string{"k"}) || !r.Running {
		t.Fatalf("failing %v running %v, want [k] and the re-run of r running", names(r.Failing), r.Running)
	}
}

// The base's graphql entries name the workflow through checkSuite.
func TestClassifyGraphQLWorkflowIsActions(t *testing.T) {
	r := Classify(rollupOf(t, `[
	 {"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"FAILURE","checkSuite":{"workflowRun":{"workflow":{"name":"CI"}}}},
	 {"__typename":"CheckRun","name":"app","status":"COMPLETED","conclusion":"FAILURE","checkSuite":{"workflowRun":null}}]`))
	if len(r.Failing) != 2 || r.Failing[0].Name != "app" || r.Failing[0].Actions || !r.Failing[1].Actions {
		t.Fatalf("%+v", r.Failing)
	}
}

// Forge text is cleaned: no control characters, a secret-shaped name withheld.
func TestClassifyCleansNames(t *testing.T) {
	r := Classify(rollupOf(t, `[{"__typename":"CheckRun","name":"te\u0007st","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://x/\u001b[2J"}]`))
	if r.Failing[0].Name != "test" || r.Failing[0].URL != "https://x/[2J" {
		t.Fatalf("%+v", r.Failing[0])
	}
}

// An entry of an unknown kind is neither a failure nor running.
func TestClassifyUnknownKindIgnored(t *testing.T) {
	r := Classify(rollupOf(t, `[{"__typename":"Other","name":"x","status":"IN_PROGRESS","state":"FAILURE"}]`))
	if r.Running || len(r.Failing) != 0 {
		t.Fatalf("%+v", r)
	}
}

// The PR read asks for the rollup in the same call (§2) and keeps it per PR.
func TestReadGitHubKeepsTheRollup(t *testing.T) {
	pr := strings.Replace(prJSON(7, hA), `"mergeable":"MERGEABLE"`, `"mergeable":"MERGEABLE","statusCheckRollup":[{"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://u","workflowName":"CI"}]`, 1)
	dir, env := fakeGH(t, "["+pr+","+prJSON(8, hB)+"]", 0)
	prs, err := ReadGitHub(context.Background(), dir, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs[0].Checks) != 1 || prs[0].Checks[0].Name != "build" || !prs[0].Checks[0].Actions || len(prs[1].Checks) != 0 {
		t.Fatalf("%+v", prs)
	}
}

func baseJSON(oid, nodes string, next bool) string {
	return fmt.Sprintf(`{"data":{"repository":{"ref":{"target":{"oid":%q,"statusCheckRollup":{"contexts":{"pageInfo":{"hasNextPage":%v},"nodes":[%s]}}}}}}}`, oid, next, nodes)
}

// The base's rollup: one graphql call for the branch's latest commit.
func TestReadBaseRollup(t *testing.T) {
	dir, env := fakeGH(t, baseJSON(hA, `{"__typename":"CheckRun","name":"test-linux","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://u","checkSuite":{"workflowRun":{"workflow":{"name":"CI"}}}}`, false), 0)
	b, err := ReadBaseRollup(context.Background(), dir, env, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if b.Head != hA || len(b.Rollup.Failing) != 1 || b.Rollup.Failing[0].Name != "test-linux" || !b.Rollup.Failing[0].Actions {
		t.Fatalf("%+v", b)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if a := string(args); !strings.HasPrefix(a, "api graphql -f query=") || !strings.Contains(a, "-F owner={owner} -F name={repo} -f ref=refs/heads/dev") {
		t.Fatalf("args %q", a)
	}
	// A commit with no rollup at all (no CI on push): not red.
	dir, env = fakeGH(t, `{"data":{"repository":{"ref":{"target":{"oid":"`+hB+`","statusCheckRollup":null}}}}}`, 0)
	if b, err := ReadBaseRollup(context.Background(), dir, env, "dev"); err != nil || b.Rollup.Running || len(b.Rollup.Failing) != 0 || b.Head != hB {
		t.Fatalf("empty rollup: %+v %v", b, err)
	}
}

// Every way the base read can fail is an error, never a green base.
func TestReadBaseRollupFailures(t *testing.T) {
	for name, c := range map[string]struct {
		out  string
		code int
	}{
		"exit 1":       {"HTTP 504", 1},
		"not json":     {"<html>", 0},
		"no ref":       {`{"data":{"repository":{"ref":null}}}`, 0},
		"no repo":      {`{"data":{"repository":null}}`, 0},
		"errors":       {`{"errors":[{"message":"x"}],"data":null}`, 0},
		"bad head":     {baseJSON("nothex", "", false), 0},
		"a cut answer": {baseJSON(hA, "", true), 0},
	} {
		dir, env := fakeGH(t, c.out, c.code)
		if _, err := ReadBaseRollup(context.Background(), dir, env, "dev"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// A re-run still queued has no time yet: it is never beaten by an older
// finished run of its name, in either order, so the PR still runs (§2,
// foureyes on ci-loop-lib).
func TestClassifyQueuedReRunStillRuns(t *testing.T) {
	failedRun := `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE","startedAt":"2026-11-19T10:00:00Z","completedAt":"2026-11-19T10:05:00Z"}`
	queued := `{"__typename":"CheckRun","name":"test","status":"QUEUED"}`
	pending := `{"__typename":"StatusContext","context":"test","state":"PENDING"}`
	for _, js := range []string{"[" + failedRun + "," + queued + "]", "[" + queued + "," + failedRun + "]", "[" + failedRun + "," + pending + "]", "[" + pending + "," + failedRun + "]"} {
		if r := Classify(rollupOf(t, js)); !r.Running || len(r.Failing) != 0 {
			t.Errorf("%s: running %v failing %v, want running and nothing failing", js, r.Running, names(r.Failing))
		}
	}
}

// gh pr list gives at most 100 rollup entries per PR (gh 2.104: a PR of 128
// checks read 100, gh pr view 128): a rollup of 100 may be cut, so the PR's
// checks are unknown (foureyes on ci-loop-lib).
func TestReadGitHubRollupOf100IsCut(t *testing.T) {
	var e []string
	for i := 0; i < 100; i++ {
		e = append(e, fmt.Sprintf(`{"__typename":"CheckRun","name":"c%d","status":"COMPLETED","conclusion":"SUCCESS"}`, i))
	}
	pr := strings.Replace(prJSON(7, hA), `"mergeable":"MERGEABLE"`, `"mergeable":"MERGEABLE","statusCheckRollup":[`+strings.Join(e, ",")+`]`, 1)
	pr99 := strings.Replace(prJSON(8, hB), `"mergeable":"MERGEABLE"`, `"mergeable":"MERGEABLE","statusCheckRollup":[`+strings.Join(e[:99], ",")+`]`, 1)
	dir, env := fakeGH(t, "["+pr+","+pr99+"]", 0)
	prs, err := ReadGitHub(context.Background(), dir, env)
	if err != nil {
		t.Fatal(err)
	}
	if !prs[0].ChecksCut || prs[1].ChecksCut {
		t.Fatalf("cut %v %v", prs[0].ChecksCut, prs[1].ChecksCut)
	}
}
