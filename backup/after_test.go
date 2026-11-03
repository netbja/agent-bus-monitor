package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// script writes an executable shell script named name in a new folder and
// returns its path.
func script(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func afterSpec(t *testing.T, cmd string) AfterSpec {
	t.Helper()
	dir := t.TempDir()
	return AfterSpec{Command: cmd, Dir: dir, Archive: filepath.Join(dir, "a.tar.zst"), Projects: []string{"demo", "quiet"},
		Env: []string{"PATH=" + os.Getenv("PATH"), "HOME=/h", "REDIS_PASSWORD=pw", "REDIS_URL=redis://:pw@h", "REDISCLI_AUTH=pw",
			"MY_API_TOKEN=t", "SOME_SECRET=s", "DB_PASSWD=p", "CLOUD_KEY=k", "X_PASS=p", "Y_PWD=p",
			"RESTIC_PASSWORD_FILE=/k", "PLAIN=ok", "LOOKS=ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		Allow: []string{"RESTIC_PASSWORD_FILE"}, Timeout: 5 * time.Second, Grace: 200 * time.Millisecond}
}

func TestRunAfterEnvAndArgs(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	bin := script(t, "send", `pwd > `+out+`; printf '%s\n' "$#" "$1" "$2" "$3" >> `+out+`; env | sort >> `+out+`
`)
	s := afterSpec(t, bin+` 'two words' "$HOME" ';|'`)
	var log bytes.Buffer
	s.Log = &log
	res := RunAfter(context.Background(), s)
	if res.Outcome != "ok" || res.Exit != 0 {
		t.Fatalf("outcome %+v", res)
	}
	got, _ := os.ReadFile(out)
	lines := strings.Split(string(got), "\n")
	if lines[0] != s.Dir || lines[1] != "3" || lines[2] != "two words" || lines[3] != "$HOME" || lines[4] != ";|" {
		t.Fatalf("cwd and args %q", lines[:5])
	}
	env := string(got)
	for _, want := range []string{"AGENTBUS_BACKUP_ARCHIVE=" + s.Archive, "AGENTBUS_BACKUP_DIR=" + s.Dir,
		"AGENTBUS_BACKUP_PROJECTS=demo,quiet", "AGENTBUS_BACKUP_SCHEDULED=0", "RESTIC_PASSWORD_FILE=/k", "PLAIN=ok"} {
		if !strings.Contains(env, want+"\n") {
			t.Errorf("env lacks %s", want)
		}
	}
	for _, gone := range []string{"REDIS_PASSWORD=", "REDIS_URL=", "REDISCLI_AUTH=", "MY_API_TOKEN=", "SOME_SECRET=",
		"DB_PASSWD=", "CLOUD_KEY=", "X_PASS=", "Y_PWD=", "LOOKS="} {
		if strings.Contains(env, "\n"+gone) || strings.HasPrefix(env, gone) {
			t.Errorf("env holds %s", gone)
		}
	}
	s.Scheduled = true
	res = RunAfter(context.Background(), s)
	if got, _ := os.ReadFile(out); res.Outcome != "ok" || !strings.Contains(string(got), "AGENTBUS_BACKUP_SCHEDULED=1") {
		t.Fatal("scheduled flag not passed")
	}
}

func TestRunAfterFailures(t *testing.T) {
	exit3 := script(t, "fail", "echo token=ghp_abcdefghijklmnopqrstuvwxyz0123456789 >&2\nexit 3\n")
	var log bytes.Buffer
	s := afterSpec(t, exit3)
	s.Log = &log
	if res := RunAfter(context.Background(), s); res.Outcome != "exit 3" || res.Exit != 3 {
		t.Fatalf("exit 3: %+v", res)
	}
	if strings.Contains(log.String(), "ghp_") {
		t.Fatalf("the log holds a token: %q", log.String())
	}
	s = afterSpec(t, filepath.Join(t.TempDir(), "none"))
	if res := RunAfter(context.Background(), s); res.Outcome != "not started" {
		t.Fatalf("missing: %+v", res)
	}
	s = afterSpec(t, `unbalanced 'quote`)
	if res := RunAfter(context.Background(), s); res.Outcome != "not started" {
		t.Fatalf("unparsable: %+v", res)
	}
	// A timeout kills the whole group, the command's children included.
	pids := filepath.Join(t.TempDir(), "pids")
	slow := script(t, "slow", "sleep 30 &\necho $! > "+pids+"\ntrap '' TERM\nsleep 30\n")
	s = afterSpec(t, slow)
	s.Timeout = 300 * time.Millisecond
	start := time.Now()
	if res := RunAfter(context.Background(), s); res.Outcome != "timeout" {
		t.Fatalf("timeout: %+v", res)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("took %v", took)
	}
	b, _ := os.ReadFile(pids)
	child, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	time.Sleep(100 * time.Millisecond)
	if child > 0 && syscall.Kill(child, 0) == nil {
		t.Fatalf("child %d still runs", child)
	}
}

func TestRunAfterCapsItsOutput(t *testing.T) {
	loud := script(t, "loud", "yes 0123456789 | head -c 200000\n")
	var log bytes.Buffer
	s := afterSpec(t, loud)
	s.Log = &log
	if res := RunAfter(context.Background(), s); res.Outcome != "ok" {
		t.Fatalf("%+v", res)
	}
	if log.Len() > 70<<10 {
		t.Fatalf("logged %d bytes", log.Len())
	}
}

func TestAfterRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "backup-last.json")
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	if err := RecordAfter(path, []string{"a", "b"}, AfterEntry{At: at, Archive: "x.tar.zst", Outcome: "exit 3"}); err != nil {
		t.Fatal(err)
	}
	if err := RecordAfter(path, []string{"a"}, AfterEntry{At: at.Add(time.Hour), Archive: "y.tar.zst", Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	rec, err := ReadLast(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.After["a"].Outcome != "ok" || rec.After["b"].Outcome != "exit 3" || rec.After["b"].Archive != "x.tar.zst" {
		t.Fatalf("record %+v", rec)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	if _, err := ReadLast(filepath.Join(t.TempDir(), "none.json")); err != nil {
		t.Fatalf("an absent record: %v", err)
	}
}

// foureyes: a command that exits 0 but leaves a child holding its output
// (an ssh ControlMaster left by rsync or restic) is ok, judged by its own
// exit once WaitDelay has passed, and the child is left alone.
func TestRunAfterExitsWithAChildHoldingItsOutput(t *testing.T) {
	pids := filepath.Join(t.TempDir(), "pids")
	bin := script(t, "mux", "sleep 3 &\necho $! > "+pids+"\nexit 0\n")
	s := afterSpec(t, bin)
	s.Timeout, s.WaitDelay = 10*time.Second, 200*time.Millisecond
	start := time.Now()
	res := RunAfter(context.Background(), s)
	if res.Outcome != "ok" || res.Exit != 0 {
		t.Fatalf("outcome %+v", res)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("waited %v for a child that holds the output", took)
	}
	b, _ := os.ReadFile(pids)
	if child, _ := strconv.Atoi(strings.TrimSpace(string(b))); child > 0 && syscall.Kill(child, 0) != nil {
		t.Fatal("the persisting child was killed after a normal exit")
	}
	// The same with a failing exit: judged by the command's code.
	bin = script(t, "mux3", "sleep 3 &\nexit 3\n")
	s = afterSpec(t, bin)
	s.Timeout, s.WaitDelay = 10*time.Second, 200*time.Millisecond
	if res := RunAfter(context.Background(), s); res.Outcome != "exit 3" {
		t.Fatalf("outcome %+v", res)
	}
}
