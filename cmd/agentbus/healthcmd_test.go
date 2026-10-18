package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

func TestHealthCommandListDetailAndNote(t *testing.T) {
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "healthcmd"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	if err := b.MarkServicesInstalled(context.Background(), bus.ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := runHealth(context.Background(), b, "sentinel", []string{"list"}, &out, &errw); code != 0 {
		t.Fatalf("list code=%d err=%s", code, errw.String())
	}
	if !strings.Contains(out.String(), "services") || !strings.Contains(out.String(), "unknown") {
		t.Fatalf("list output=%q", out.String())
	}
	out.Reset()
	if code := runHealth(context.Background(), b, "sentinel", []string{"note", "services", "checked safely"}, &out, &errw); code != 0 {
		t.Fatalf("note code=%d err=%s", code, errw.String())
	}
	out.Reset()
	if code := runHealth(context.Background(), b, "sentinel", []string{"detail", "services"}, &out, &errw); code != 0 {
		t.Fatalf("detail code=%d err=%s", code, errw.String())
	}
	if !strings.Contains(out.String(), "remediation:") || !strings.Contains(out.String(), "checked safely") {
		t.Fatalf("detail output=%q", out.String())
	}
}
