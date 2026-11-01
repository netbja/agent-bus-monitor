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

// A spent restart budget fails like an inactive service, but only the person
// can restart it (sentinel's budget is the same one): the evaluator says so in
// the outcome, never in its detail, and the record carries it to busmon
// (reviewer-codex).
func TestSpentBudgetIsForThePerson(t *testing.T) {
	spent := EvaluateServices(ServiceEvidence{Known: true, Installed: true, Active: true, RecentFailures: 5})
	inactive := EvaluateServices(ServiceEvidence{Known: true, Installed: true, Active: false})
	if spent.Status != Fail || !spent.Person || inactive.Status != Fail || inactive.Person {
		t.Fatalf("spent %+v, inactive %+v", spent, inactive)
	}
	d, _ := ByID("services")
	if got := d.PlaceOf(Record{Status: Fail, Person: true}, Fail); got != PlaceForYou || d.Command == "" {
		t.Errorf("spent budget: place %q, command %q", got, d.Command)
	}
	if got := d.PlaceOf(Record{Status: Fail}, Fail); got != PlaceInHand {
		t.Errorf("inactive service: place %q", got)
	}
	// The flag never moves an ok or unknown check, whatever a record says.
	for _, s := range []Status{OK, Unknown} {
		if got := d.PlaceOf(Record{Status: s, Person: true}, s); got != d.Place(s) {
			t.Errorf("%s with person: place %q", s, got)
		}
	}
	// Only the person's outcomes carry it.
	for _, o := range []Outcome{EvaluateServices(ServiceEvidence{Known: true, Installed: true, Active: true, RecentFailures: 4}),
		EvaluateServices(ServiceEvidence{Known: true, Installed: true, Active: true, WaitingGit: true})} {
		if o.Person {
			t.Errorf("%+v carries person", o)
		}
	}
}
