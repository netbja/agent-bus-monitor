package quota

import (
	"context"
	"errors"
	"testing"
)

// The local-llm peer's provider is a built-in unlimited reader (local-llm-peer
// step 1): no quota.toml, nothing found on the machine, and the dispatcher
// never holds or pauses it. Only a provider an agent uses is published, so
// it shows nowhere else.
func TestLocalIsABuiltinUnlimitedProvider(t *testing.T) {
	none := func(string) (string, error) { return "", errors.New("absent") }
	p, ok := Builtins(none)["local"]
	if !ok || p.Kind != "unlimited" {
		t.Fatalf("local = %+v %v, want a built-in unlimited reader", p, ok)
	}
	r, err := (UnlimitedReader{Name: "local"}).Read(context.Background())
	if err != nil || !r.Unlimited || r.Provider != "local" {
		t.Fatalf("reading = %+v %v", r, err)
	}
}
