package bus

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestServiceStateNeedsTheInstalledMarker(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, ServicesKey(b.project)) })

	// No marker: the project was never set up for services, or it was deleted.
	ok, err := b.PutServiceState(ctx, "dispatch", ServiceRecord{State: "ready"})
	if err != nil || ok {
		t.Fatalf("put without marker = %v, %v; want false, nil", ok, err)
	}
	if n, _ := b.r.Exists(ctx, ServicesKey(b.project)).Result(); n != 0 {
		t.Fatal("a state write recreated the services key")
	}
	if installed, _ := b.ServicesInstalled(ctx); installed {
		t.Fatal("installed without a marker")
	}

	if err := b.MarkServicesInstalled(ctx, ServicesInstall{Kinds: []string{"dispatch", "missions"}, Manager: "systemd", Host: "h"}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Minute).UnixMilli()
	ok, err = b.PutServiceState(ctx, "dispatch", ServiceRecord{State: "ready", Detail: "lease held", PID: 42, Host: "h", Log: "/l"})
	if err != nil || !ok {
		t.Fatalf("put = %v, %v", ok, err)
	}
	inst, recs, err := b.ServiceStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inst == nil || inst.Manager != "systemd" || strings.Join(inst.Kinds, ",") != "dispatch,missions" {
		t.Fatalf("install = %+v", inst)
	}
	r := recs["dispatch"]
	if r.State != "ready" || r.PID != 42 || r.Detail != "lease held" || r.At < before {
		t.Fatalf("record = %+v (at must come from Redis TIME)", r)
	}
	if _, ok := recs["installed"]; ok {
		t.Fatal("the marker is not a service record")
	}
	if err := b.ClearServices(ctx); err != nil {
		t.Fatal(err)
	}
	if installed, _ := b.ServicesInstalled(ctx); installed {
		t.Fatal("ClearServices left the marker")
	}
}

func TestServicesKeyNamesAProject(t *testing.T) {
	if p, ok := projectFromKey("demo:services"); !ok || p != "demo" {
		t.Fatalf("projectFromKey = %q, %v", p, ok)
	}
}
