package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

// fakeHerdrEnv points HERDR_BIN at dispatch's fake-herdr, serving the given
// process-info / pane-get answers.
func fakeHerdrEnv(t *testing.T, processInfo, paneGet string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"process-info.json": processInfo, "pane-get.json": paneGet} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin, err := filepath.Abs("../../dispatch/testdata/fake-herdr")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN", bin)
	t.Setenv("FAKE_HERDR_DIR", dir)
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w9:p1")
}

func TestStatusRecordsIdentityViaFakeHerdr(t *testing.T) {
	b, r := dialMain(t)
	t.Cleanup(func() { bus.DeleteProject(context.Background(), r, b.Project()) })
	parent := os.Getppid()
	fakeHerdrEnv(t,
		`{"result":{"process_info":{"foreground_processes":[{"pid":999999999},{"pid":`+strconv.Itoa(parent)+`}]}}}`,
		`{"result":{"pane":{"pane_id":"w9:p1","terminal_id":"term_fake"}}}`)

	ctx := context.Background()
	who := statusIdent(ctx)
	if _, err := b.Status(ctx, "dev", "idle", "hi", who); err != nil {
		t.Fatal(err)
	}
	m, err := b.Agents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(m) // what `agents --json` prints
	want, _ := dispatch.ProcStart(parent)
	for _, frag := range []string{
		`"pid":` + strconv.Itoa(parent),
		`"pid_start":` + strconv.FormatUint(want, 10),
		`"terminal":"term_fake"`,
		`"pane":"w9:p1"`,
	} {
		if !strings.Contains(string(out), frag) {
			t.Errorf("agents --json lacks %s:\n%s", frag, out)
		}
	}
}

func TestStatusIdentityFailureLeavesFieldsEmpty(t *testing.T) {
	fakeHerdrEnv(t, `{"error":{"code":"pane_not_found","message":"pane not found"}}`, `{}`)
	who := statusIdent(context.Background())
	if who.PID != 0 || who.PIDStart != 0 || who.Terminal != "" || who.Pane != "w9:p1" {
		t.Fatalf("failed lookup: %+v", who)
	}
	t.Setenv("HERDR_ENV", "")
	if who := statusIdent(context.Background()); who.PID != 0 {
		t.Fatalf("outside herdr: %+v", who)
	}
}
