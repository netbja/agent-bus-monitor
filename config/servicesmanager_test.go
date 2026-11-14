package config

import "testing"

// services.manager (herdr exit, spec 4.4b): the machine's service manager,
// auto (default), systemd, launchd or none. Its env variable is the one
// AGENTBUS_SERVICE_MANAGER has always been; another value is skipped.
func TestServicesManagerKey(t *testing.T) {
	k, ok := Lookup("services.manager")
	if !ok {
		t.Fatal("services.manager not registered")
	}
	if k.Env != "AGENTBUS_SERVICE_MANAGER" || k.Default != "auto" || !k.MachineOnly || k.Group() != GroupServices {
		t.Fatalf("%+v group %q", k, k.Group())
	}
	for _, v := range []string{"auto", "systemd", "launchd", "none"} {
		if got, r := k.env(v); r != envUse || got != v {
			t.Errorf("%s: %v %v", v, got, r)
		}
		if got, why := k.file(v); why != "" || got != v {
			t.Errorf("file %s: %v %q", v, got, why)
		}
	}
	if _, r := k.env("upstart"); r != envSkip {
		t.Error("upstart taken")
	}
	if _, why := k.file("upstart"); why == "" {
		t.Error("file upstart taken")
	}
}
