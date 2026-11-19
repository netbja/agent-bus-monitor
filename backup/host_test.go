package backup

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/service"
)

// HostProjects is every project of this host: the broker's and every
// template's, sorted, once each; a template whose name is not a project name
// is not one (dispatchsvc P5, moved from cmd/agentbus backupcmd.go).
func TestHostProjects(t *testing.T) {
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatalf("explicit test Redis unavailable: %v", err)
	}
	ctx := context.Background()
	name := "hp" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, err := bus.Open(r, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()) })
	if err := r.HSet(ctx, bus.BoardKey(name), "t1", `{"owner":"coder","state":"accepted"}`).Err(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, f := range []string{"zz-tpl.toml", name + ".toml", "Not_A_Name.toml", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := HostProjects(ctx, r, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.IsSorted(got) || len(slices.Compact(slices.Clone(got))) != len(got) {
		t.Fatalf("not sorted once each: %q", got)
	}
	for _, want := range []string{name, "zz-tpl"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s missing from %q", want, got)
		}
	}
	if slices.Contains(got, "Not_A_Name") || slices.Contains(got, "notes") {
		t.Errorf("a non-project template counted: %q", got)
	}
}

// A Deps with its own Projects answers with it (tests pin their scope on a
// shared broker); without, the host's.
func TestDepsHostProjectsOverride(t *testing.T) {
	d := Deps{Projects: func(context.Context, *redis.Client, string) ([]string, error) { return []string{"pinned"}, nil }}
	if got, err := d.HostProjects(context.Background(), nil, ""); err != nil || !slices.Equal(got, []string{"pinned"}) {
		t.Fatalf("override: %q %v", got, err)
	}
}

func TestLastPath(t *testing.T) {
	if got := LastPath(service.Paths{StateHome: "/s"}); got != "/s/agentbus/backup-last.json" {
		t.Errorf("LastPath = %q", got)
	}
}
