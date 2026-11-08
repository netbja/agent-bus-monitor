package main

import (
	"context"
	"errors"
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

	if n, kinds, err := deleteProjectNow(ctx, c, project, func(string) ([]string, error) {
		return nil, errors.New("systemctl refused")
	}); err == nil || !strings.Contains(err.Error(), "nothing deleted") || n != 0 || kinds != nil {
		t.Fatalf("failed removal: %d %v %v", n, kinds, err)
	}
	if keys, _ := bus.ProjectKeys(ctx, c, project); len(keys) == 0 {
		t.Fatal("keys deleted although the services were not removed")
	}
	var at int
	n, kinds, err := deleteProjectNow(ctx, c, project, func(p string) ([]string, error) {
		keys, _ := bus.ProjectKeys(ctx, c, p)
		at = len(keys)
		return []string{"dispatch", "missions"}, nil
	})
	if err != nil || n == 0 || !slices.Equal(kinds, []string{"dispatch", "missions"}) || at == 0 {
		t.Fatalf("delete: %d %v %v (keys at removal %d)", n, kinds, err, at)
	}
	if keys, _ := bus.ProjectKeys(ctx, c, project); len(keys) != 0 {
		t.Fatalf("left %v", keys)
	}
	if keys, _ := bus.ProjectKeys(ctx, c, sibling); len(keys) == 0 {
		t.Fatal("the sibling project lost its keys")
	}
	if _, _, err := deleteProjectNow(ctx, c, "Bad*", nil); err == nil {
		t.Fatal("an invalid name was accepted")
	}
}
