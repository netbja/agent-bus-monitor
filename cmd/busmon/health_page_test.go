package main

import (
	tea "charm.land/bubbletea/v2"
	"strconv"
	"strings"
	"testing"
	"time"

	guardian "github.com/netbja/agent-bus-monitor/health"
)

// healthFixture: two checks for the person, one in hand, one not measured,
// the rest fine.
func healthFixture(now time.Time) map[string]guardian.Record {
	recs := allOK(now)
	at := func(id string, s guardian.Status, detail string, ago time.Duration) {
		recs[id] = guardian.Record{ID: id, Status: s, Detail: detail, ObservedAt: now.UnixMilli(), Since: now.Add(-ago).UnixMilli()}
	}
	at("tracked-secrets", guardian.Fail, "tracked private-key path: deploy/id_ed25519", 3*time.Hour)
	at("service-env", guardian.Fail, "service environment file mode is not 0600", 2*time.Hour)
	at("context", guardian.Warn, "at or above 80%: coder 85%", 12*time.Minute)
	delete(recs, "model-drift")
	return recs
}

func healthPageText(body []Line) string {
	var b strings.Builder
	for _, l := range body {
		b.WriteString(l.text() + "\n")
	}
	return b.String()
}

// One line per check, in the order the person acts in: For you, In hand, Not
// measured, and the fine ones folded into one last line that names its key.
func TestHealthPageListsByPlace(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	title, body, items, _ := healthPage(healthFixture(now), nil, now, "demo", healthView{}, 136)
	n := len(guardian.Definitions())
	if want := "Health " + GlyphNone + " 2 to do " + GlyphNone + " 1 in hand " + GlyphNone + " " + strconv.Itoa(n-4) + " fine " + GlyphNone + " 1 not measured"; title != want {
		t.Errorf("title %q, want %q", title, want)
	}
	text := healthPageText(body)
	order := []string{"For you", "Tracked secret-like files", "Service environment", "In hand", "Context fill", "Not measured", "Model drift", "fine " + GlyphNone + " " + strconv.Itoa(n-4) + "  (a shows them)"}
	at := -1
	for _, w := range order {
		i := strings.Index(text, w)
		if i <= at {
			t.Fatalf("%q missing or out of order in:\n%s", w, text)
		}
		at = i
	}
	if strings.Contains(text, "Services") { // a fine check stays folded
		t.Errorf("a fine check is listed:\n%s", text)
	}
	if len(items) != 4 {
		t.Errorf("%d selectable rows, want 4", len(items))
	}
	for _, it := range items {
		if !strings.Contains(body[it.line].text(), titleOf(it.id)) {
			t.Errorf("item %s points at line %q", it.id, body[it.line].text())
		}
	}
	if line := body[items[0].line].text(); !strings.Contains(line, GlyphBlocked+" Tracked") || !strings.HasSuffix(line, " 3h") {
		t.Errorf("for-you row %q", line)
	}
}

func titleOf(id string) string {
	d, _ := guardian.ByID(id)
	return d.Title[:min(12, len(d.Title))]
}

// Wide, a For-you row names the person's command and an In-hand row who
// deals with it; at 80 the command goes on its own line under the row.
func TestHealthPageCommandWhereThePersonReadsIt(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	_, wide, items, _ := healthPage(healthFixture(now), nil, now, "demo", healthView{}, 136)
	if got := wide[items[0].line].text(); !strings.Contains(got, "git rm --cached <path>") {
		t.Errorf("wide for-you row lacks its command: %q", got)
	}
	if got := wide[items[1].line].text(); !strings.Contains(got, "agentbus up demo") { // the open project, not a placeholder
		t.Errorf("wide row lacks the project's command: %q", got)
	}
	if got := wide[items[2].line].text(); !strings.Contains(got, "(dispatch)") {
		t.Errorf("wide in-hand row lacks who deals with it: %q", got)
	}
	_, narrow, items, _ := healthPage(healthFixture(now), nil, now, "demo", healthView{}, 66)
	row, next := narrow[items[0].line].text(), narrow[items[0].line+1].text()
	if strings.Contains(row, "git rm") || strings.TrimSpace(next) != "git rm --cached <path>" {
		t.Errorf("narrow for-you row %q then %q", row, next)
	}
	for i, l := range narrow {
		if l.Width() > 66 {
			t.Errorf("line %d is %d cells: %q", i, l.Width(), l.text())
		}
	}
}

// a lists the fine checks too; Enter opens a row in place with its facts;
// y copies the selected For-you row's command, and nothing on other rows.
func TestHealthPageAllExpandAndCopy(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	recs := healthFixture(now)
	notes := map[string]guardian.Note{"tracked-secrets": {ID: "tracked-secrets", Author: "sentinel", Text: "escalated to master"}}
	_, body, items, _ := healthPage(recs, notes, now, "demo", healthView{all: true}, 136)
	text := healthPageText(body)
	if !strings.Contains(text, "Fine") || !strings.Contains(text, GlyphDone+" Services") || strings.Contains(text, "shows them") {
		t.Errorf("a: fine checks not listed:\n%s", text)
	}
	if len(items) != len(guardian.Definitions()) {
		t.Errorf("a: %d rows, want every check", len(items))
	}
	_, body, items, plain := healthPage(recs, notes, now, "demo", healthView{open: map[string]bool{"tracked-secrets": true}}, 136)
	text = healthPageText(body)
	for _, w := range []string{"Since:", "Detail:", "tracked private-key path: deploy/id_ed25519", "What to do:", "Remove the path from version control", "Note:", "escalated to master"} {
		if !strings.Contains(text, w) {
			t.Errorf("expanded row lacks %q:\n%s", w, text)
		}
	}
	if plain != "git rm --cached <path>" {
		t.Errorf("y on a for-you row copies %q", plain)
	}
	if body[items[1].line-1].text() == body[items[0].line].text() {
		t.Error("the expansion is not under its row")
	}
	// herdr-probe names a command for its fail; warning, it is in hand, and y copies nothing.
	recs["herdr-probe"] = guardian.Record{ID: "herdr-probe", Status: guardian.Warn, ObservedAt: now.UnixMilli(), Since: now.UnixMilli()}
	for sel := 2; sel <= 3; sel++ {
		if _, _, _, plain := healthPage(recs, notes, now, "demo", healthView{sel: sel}, 136); plain != "" {
			t.Errorf("y on in-hand row %d copies %q", sel, plain)
		}
	}
}

// Without any reading every check is not measured, never fine.
func TestHealthPageWithoutReadings(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	title, body, items, _ := healthPage(nil, nil, now, "demo", healthView{}, 80)
	if !strings.HasSuffix(title, strconv.Itoa(len(guardian.Definitions()))+" ?") || strings.Contains(title, "fine") {
		t.Errorf("title %q", title)
	}
	if len(items) != len(guardian.Definitions()) || !strings.Contains(body[items[0].line].text(), "? ") {
		t.Errorf("rows: %s", healthPageText(body))
	}
}

// A service whose restart budget is spent is for the person, with the
// command that restarts it; an inactive one stays in hand (sentinel).
func TestHealthPageSpentBudgetIsForYou(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	recs := allOK(now)
	recs["services"] = guardian.Record{ID: "services", Status: guardian.Fail, Reason: guardian.ReasonBudgetSpent, Detail: "service restart budget is spent", ObservedAt: now.UnixMilli(), Since: now.UnixMilli()}
	if s := summarizeGuardian(recs, now); s.ToDo != 1 || !s.ToDoFail {
		t.Fatalf("summary %+v", s)
	}
	_, body, items, plain := healthPage(recs, nil, now, "demo", healthView{}, 136)
	if !strings.HasPrefix(healthPageText(body), "  For you") || plain != "agentbus --project demo service restart" {
		t.Errorf("spent budget: plain %q\n%s", plain, healthPageText(body))
	}
	_ = items
	recs["services"] = guardian.Record{ID: "services", Status: guardian.Fail, Detail: "an installed service is inactive or failed", ObservedAt: now.UnixMilli(), Since: now.UnixMilli()}
	if s := summarizeGuardian(recs, now); s.ToDo != 0 || s.InHand != 1 {
		t.Fatalf("inactive: summary %+v", s)
	}
}

// The selection follows its check, not its index (foureyes): a poll that
// moves another check out of the list keeps the cursor, and y, on the same one.
func TestHealthPageSelectionFollowsItsCheck(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	recs := healthFixture(now)
	_, body, items, _ := healthPage(recs, nil, now, "demo", healthView{selID: "service-env"}, 136)
	if !strings.HasPrefix(body[items[1].line].text(), GlyphSelect) {
		t.Fatalf("service-env not selected:\n%s", healthPageText(body))
	}
	recs["tracked-secrets"] = guardian.Record{ID: "tracked-secrets", Status: guardian.OK, ObservedAt: now.UnixMilli()}
	_, body, items, plain := healthPage(recs, nil, now, "demo", healthView{sel: 1, selID: "service-env"}, 136)
	if !strings.HasPrefix(body[items[0].line].text(), GlyphSelect) || plain != "agentbus up demo" {
		t.Errorf("the cursor left service-env: plain %q\n%s", plain, healthPageText(body))
	}
}

// Through the shell: ↓ selects the next check, and a poll that drops the
// check above keeps the cursor on the one chosen.
func TestHealthPageCursorSurvivesAPoll(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 40)
	snap := shellFixture(shellNow)
	snap.guardianHealth, snap.guardianNow = healthFixture(shellNow), shellNow
	m = pollAgain(t, m, snap, shellNow)
	m = keys(t, keys(t, m, tea.Key{Code: tea.KeyF5}), kDown)
	if p := m.rd.top(); p == nil || p.hitems[p.sel].id != "service-env" {
		t.Fatalf("↓ did not select service-env: %+v", p)
	}
	snap = shellFixture(shellNow)
	snap.guardianHealth, snap.guardianNow = healthFixture(shellNow), shellNow
	snap.guardianHealth["tracked-secrets"] = guardian.Record{ID: "tracked-secrets", Status: guardian.OK, ObservedAt: shellNow.UnixMilli()}
	m = pollAgain(t, m, snap, shellNow.Add(time.Second))
	if p := m.rd.top(); p.hitems[p.sel].id != "service-env" || p.plain != "agentbus up agent-bus-monitor" {
		t.Fatalf("after the poll the cursor is on %s (plain %q)", p.hitems[p.sel].id, p.plain)
	}
}
