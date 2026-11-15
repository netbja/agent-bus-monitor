package main

import "testing"

// herdr exit 6c3: busmon links holder, so with services.manager = none its
// manager is the supervisor (listed units, a delete that stops them), never
// "no manager". The name is written out: importing holder here would link
// it into the test binary and prove nothing about busmon's own imports.
func TestBusmonManagerUnderNoneIsTheSupervisor(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir(), "AGENTBUS_SERVICE_MANAGER": "none", "XDG_RUNTIME_DIR": t.TempDir()}
	m, err := localManager(func(k string) string { return env[k] })
	if err != nil || m == nil || m.Name() != "supervisor" {
		t.Fatalf("busmon's manager under none = %v, %v", m, err)
	}
}
