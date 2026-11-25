package bus

import (
	"context"
	"os"
	"slices"
	"testing"
)

// observer-role (the person, 2026-11-25): an observer's Redis user reads
// and writes nothing: read commands only, on every key.
func TestACLObserverRules(t *testing.T) {
	if !slices.Contains(ACLRoles, ACLObserver) {
		t.Fatalf("ACLRoles %v lack the observer", ACLRoles)
	}
	want := []string{"-@all", "+@read", "+@connection", "-@dangerous", "%R~*"}
	if got := ACLRules(ACLObserver); !slices.Equal(got, want) {
		t.Fatalf("observer rules %v, want %v", got, want)
	}
}

// An identity the manifest declares an observer connects as observer,
// whatever else its entry or ancestry would give it (the least rights); the
// admin entry stays refused to any identity.
func TestACLObserverUser(t *testing.T) {
	isObs := func(n string) bool { return n == "observer" }
	for _, c := range []struct {
		entry              ACLEntry
		identity, ancestor string
		want               string
		refused            bool
	}{
		{ACLEntryCLI, "observer", "", ACLObserver, false},
		{ACLEntryCLI, "observer", "coder", ACLObserver, false},  // under an agent: still only observer
		{ACLEntryCLI, "observer", "master", ACLObserver, false}, // under master: never master
		{ACLEntryBusmon, "observer", "", ACLObserver, false},
		{ACLEntryService, "observer", "", ACLObserver, false},
		{ACLEntryAdmin, "observer", "", "", true},
		// reviewer-codex: under a registered observer, whatever the identity says
		{ACLEntryCLI, "", "observer", ACLObserver, false},
		{ACLEntryCLI, "coder", "observer", ACLObserver, false},
		{ACLEntryCLI, "master", "observer", ACLObserver, false},
		{ACLEntryBusmon, "", "observer", ACLObserver, false},
		{ACLEntryService, "", "observer", ACLObserver, false},
		{ACLEntryAdmin, "", "observer", "", true},
		{ACLEntryCLI, "coder", "", ACLAgent, false}, // a launched role: unchanged
		{ACLEntryCLI, "", "", ACLBusmon, false},     // the person: unchanged
	} {
		got, err := aclUserOf(c.entry, c.identity, c.ancestor, isObs)
		if (err != nil) != c.refused || got != c.want {
			t.Errorf("%v %q under %q: %q %v, want %q refused %v", c.entry, c.identity, c.ancestor, got, err, c.want, c.refused)
		}
	}
	if got, _ := aclUserOf(ACLEntryCLI, "observer", "", nil); got != ACLAgent {
		t.Fatalf("no observer check: %q, want agent", got)
	}
}

// Connect's own choice: with SetObservers, an observer identity connects as
// observer, and so does its ancestry's inner read (never the agent user,
// which writes).
func TestConnectUserObserver(t *testing.T) {
	t.Setenv(brokerACLEnv(t), "on")
	t.Setenv("AGENT_BUS_AGENT", "observer")
	t.Cleanup(func() { SetConnectEntry(ACLEntryCLI, nil); SetObservers(nil) })
	SetObservers(func(n string) bool { return n == "observer" })
	var inner string
	SetConnectEntry(ACLEntryCLI, func(ctx context.Context) (string, error) {
		u, err := connectUser(ctx, os.Getenv)
		inner = u
		return "", err
	})
	u, err := connectUser(context.Background(), os.Getenv)
	if err != nil || u != ACLObserver || inner != ACLObserver {
		t.Fatalf("user %q, inner %q, err %v", u, inner, err)
	}
	// reviewer-codex: no identity, or another's, under a registered observer.
	SetConnectEntry(ACLEntryCLI, func(context.Context) (string, error) { return "observer", nil })
	for _, id := range []string{"", "coder", "master"} {
		t.Setenv("AGENT_BUS_AGENT", id)
		if u, err := connectUser(context.Background(), os.Getenv); err != nil || u != ACLObserver {
			t.Errorf("identity %q under the observer: %q %v", id, u, err)
		}
	}
	t.Setenv("AGENT_BUS_AGENT", "observer")
	SetConnectEntry(ACLEntryCLI, nil)
	SetObservers(nil) // no manifest known: the identity is an agent, as before
	if u, _ := connectUser(context.Background(), os.Getenv); u != ACLAgent {
		t.Fatalf("without observers: %q", u)
	}
}
