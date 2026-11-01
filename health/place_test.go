package health

import "testing"

// Every check has a place for every status, and the place says who acts:
// a For-you row names the person's command, an In-hand row who deals with it.
func TestEveryCheckHasAPlaceForEveryStatus(t *testing.T) {
	for _, d := range Definitions() {
		if got := d.Place(OK); got != PlaceFine {
			t.Errorf("%s ok: place %q", d.ID, got)
		}
		if got := d.Place(Unknown); got != PlaceNotMeasured {
			t.Errorf("%s unknown: place %q", d.ID, got)
		}
		if got := d.Place(Status("bogus")); got != PlaceNotMeasured {
			t.Errorf("%s bogus status: place %q, want not measured", d.ID, got)
		}
		for _, s := range []Status{Warn, Fail} {
			switch d.Place(s) {
			case PlaceForYou:
				if d.Command == "" {
					t.Errorf("%s %s is for the person but names no command", d.ID, s)
				}
			case PlaceInHand:
				if d.Handler == "" {
					t.Errorf("%s %s is in hand but names nobody", d.ID, s)
				}
			default:
				t.Errorf("%s %s: place %q", d.ID, s, d.Place(s))
			}
		}
	}
}

// The places the mock-ups name (busmon pages, Health): the person's own
// checks, and the ones something already deals with.
func TestPlacesOfTheNamedChecks(t *testing.T) {
	for id, want := range map[string]map[Status]Place{
		"tracked-secrets":    {Fail: PlaceForYou},
		"redis-password":     {Warn: PlaceForYou},
		"redis-version":      {Warn: PlaceForYou},
		"installed-versions": {Warn: PlaceForYou},
		"service-env":        {Fail: PlaceForYou},
		"services":           {Fail: PlaceInHand, Warn: PlaceInHand},
		"context":            {Warn: PlaceInHand},
		"client-updates":     {Warn: PlaceInHand},
		"runaway":            {Warn: PlaceInHand},
		"herdr-probe":        {Warn: PlaceInHand, Fail: PlaceForYou},
	} {
		d, ok := ByID(id)
		if !ok {
			t.Fatalf("no check %s", id)
		}
		for s, p := range want {
			if got := d.Place(s); got != p {
				t.Errorf("%s %s: place %q, want %q", id, s, got, p)
			}
		}
	}
}

// A spent restart budget fails like an inactive service, and a Git wait warns
// like a recent error, but only the person can fix the first of each: the
// evaluator says so with a fixed reason code, never in its detail, and the
// check names that code's command (reviewer-codex, foureyes).
func TestServicesReasonsAreForThePerson(t *testing.T) {
	d, _ := ByID("services")
	for _, c := range []struct {
		ev      ServiceEvidence
		status  Status
		place   Place
		command string
	}{
		{ServiceEvidence{Known: true, Installed: true, Active: true, RecentFailures: 5}, Fail, PlaceForYou, "agentbus --project <project> service restart"},
		{ServiceEvidence{Known: true, Installed: true, Active: true, WaitingGit: true}, Warn, PlaceForYou, "git init"},
		{ServiceEvidence{Known: true, Installed: true, Active: false}, Fail, PlaceInHand, ""},
		{ServiceEvidence{Known: true, Installed: true, Active: true, RecentFailures: 4}, Warn, PlaceInHand, ""},
		{ServiceEvidence{Known: true, Installed: true, Active: true, RecentError: true}, Warn, PlaceInHand, ""},
	} {
		o := EvaluateServices(c.ev)
		rec := Record{Status: o.Status, Reason: o.Reason}
		if o.Status != c.status || d.PlaceOf(rec, o.Status) != c.place || d.CommandOf(rec) != c.command {
			t.Errorf("%+v: %+v, place %q, command %q", c.ev, o, d.PlaceOf(rec, o.Status), d.CommandOf(rec))
		}
	}
	// A reason never moves an ok or unknown check, nor does a code the check
	// does not declare.
	for _, s := range []Status{OK, Unknown} {
		if got := d.PlaceOf(Record{Status: s, Reason: ReasonBudgetSpent}, s); got != d.Place(s) {
			t.Errorf("%s with a reason: place %q", s, got)
		}
	}
	if got := d.PlaceOf(Record{Status: Fail, Reason: "made-up"}, Fail); got != PlaceInHand {
		t.Errorf("an undeclared reason: place %q", got)
	}
}

// Every declared reason names its command.
func TestEveryReasonNamesItsCommand(t *testing.T) {
	for _, d := range Definitions() {
		for code, cmd := range d.Reasons {
			if code == "" || cmd == "" {
				t.Errorf("%s: reason %q command %q", d.ID, code, cmd)
			}
		}
	}
}
