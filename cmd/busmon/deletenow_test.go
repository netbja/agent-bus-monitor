package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// projects-admin §5.3: deleteProjectNow is --delete's core with no terminal
// I/O: the services first, then every {p}:* key, never a sibling's; a removal
// that fails deletes nothing.
func TestDeleteProjectNow(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	project := "dn" + strconv.FormatInt(time.Now().UnixNano(), 36)
	sibling := project + "-2"
	for _, p := range []string{project, sibling} {
		b, _ := bus.Open(c, p)
		b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}})
	}
	t.Cleanup(func() { c.Del(ctx, bus.ServicesKey(project), bus.ServicesKey(sibling)) })

	if n, kinds, _, err := deleteProjectNow(ctx, c, project, func(string) ([]string, error) {
		return nil, errors.New("systemctl refused")
	}); err == nil || !strings.Contains(err.Error(), "nothing deleted") || n != 0 || kinds != nil {
		t.Fatalf("failed removal: %d %v %v", n, kinds, err)
	}
	if keys, _ := bus.ProjectKeys(ctx, c, project); len(keys) == 0 {
		t.Fatal("keys deleted although the services were not removed")
	}
	var at int
	n, kinds, left, err := deleteProjectNow(ctx, c, project, func(p string) ([]string, error) {
		keys, _ := bus.ProjectKeys(ctx, c, p)
		at = len(keys)
		return []string{"dispatch", "missions"}, nil
	})
	if err != nil || n == 0 || !slices.Equal(kinds, []string{"dispatch", "missions"}) || at == 0 || len(left) != 0 {
		t.Fatalf("delete: %d %v %v (keys at removal %d)", n, kinds, err, at)
	}
	if keys, _ := bus.ProjectKeys(ctx, c, project); len(keys) != 0 {
		t.Fatalf("left %v", keys)
	}
	if keys, _ := bus.ProjectKeys(ctx, c, sibling); len(keys) == 0 {
		t.Fatal("the sibling project lost its keys")
	}
	// reviewer-codex: an invalid name reaches no callback (the services' env
	// file is a path built from the name) and no key.
	for _, bad := range []string{"Bad*", "../../victim", "", "demo/x"} {
		called := false
		if _, _, _, err := deleteProjectNow(ctx, c, bad, func(string) ([]string, error) { called = true; return nil, nil }); err == nil || called {
			t.Fatalf("%q: err %v, removal called %v", bad, err, called)
		}
	}
}

// projects-admin §5.3: a lease refuses the delete (nothing deleted), on any
// host; busmon --delete reports keys that came back and fails.
func TestDeleteProjectNowRefusesALiveProject(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	project := "dl" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, _ := bus.Open(c, project)
	b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}})
	c.Set(ctx, bus.DispatchKey(project), "tok", time.Minute)
	t.Cleanup(func() { c.Del(ctx, bus.ServicesKey(project), bus.DispatchKey(project)) })
	var r *bus.DeleteRefusal
	if n, _, _, err := deleteProjectNow(ctx, c, project, nil); !errors.As(err, &r) || n != 0 {
		t.Fatalf("live: %d %v", n, err)
	}
	// foureyes: a refusal after the units were removed says so.
	_, kinds, _, err := deleteProjectNow(ctx, c, project, func(string) ([]string, error) { return []string{"dispatch"}, nil })
	if err == nil || !strings.Contains(err.Error(), "nothing deleted") || !strings.Contains(err.Error(), "its dispatch service here was removed") || len(kinds) != 1 {
		t.Fatalf("refusal after removal: %v %v", kinds, err)
	}
	if ok, _ := b.ServicesInstalled(ctx); !ok {
		t.Fatal("a refused delete deleted keys")
	}
	if err := deleteProject("", project, true, strings.NewReader(""), time.Now(), nil); err == nil || !strings.Contains(err.Error(), "dispatch holds its lease") {
		t.Fatalf("--delete on a live project: %v", err)
	}
}

// foureyes: each outcome in its own words; once the script deleted, never
// "nothing deleted".
func TestDeleteErr(t *testing.T) {
	gone := []string{"dispatch"}
	for _, c := range []struct {
		err       error
		n         int64
		has, hasn string
	}{
		{&bus.RescanError{Deleted: 12, Err: errors.New("i/o timeout")}, 12, "deleted 12 keys of demo, then could not scan again: i/o timeout", "nothing deleted"},
		{fmt.Errorf("%w: EOF", bus.ErrOutcomeUnknown), 0, "outcome is unknown: EOF; busmon --list shows what remains; its dispatch service here was removed", "nothing deleted"},
		{&bus.DeleteRefusal{What: "conductor"}, 0, "demo: a conductor holds its lease (nothing deleted; its dispatch service here was removed)", ""},
		{errors.New("connection refused"), 0, "demo: connection refused (nothing deleted; its dispatch service here was removed)", ""},
	} {
		got := deleteErr("demo", c.n, gone, c.err).Error()
		if !strings.Contains(got, c.has) || (c.hasn != "" && strings.Contains(got, c.hasn)) {
			t.Errorf("%v: %q", c.err, got)
		}
	}
	if deleteErr("demo", 3, nil, nil) != nil {
		t.Error("no error became one")
	}
}
