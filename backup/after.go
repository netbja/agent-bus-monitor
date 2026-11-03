package backup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// AfterSpec is one run of backup.after_command (spec "After the archive").
type AfterSpec struct {
	Command   string   // one command line, split as a shell would, run with no shell
	Dir       string   // backup.dir: its working folder and AGENTBUS_BACKUP_DIR
	Archive   string   // the complete archive's absolute path
	Projects  []string // the projects the archive covers
	Scheduled bool
	Env       []string      // the backup process's environment, before the scrub
	Allow     []string      // backup.after_env: names kept despite looking secret
	Timeout   time.Duration // backup.after_timeout
	Grace     time.Duration // SIGTERM to SIGKILL (10 s)
	// WaitDelay is how long the output is read after the command exits
	// (5 s): a child it leaves holding its output (an ssh ControlMaster
	// left by rsync or restic) never makes it a timeout (foureyes).
	WaitDelay time.Duration
	Log       io.Writer // its output, at most afterLogMax, each line redacted
}

// AfterOutcome is how the command ended: "ok", "exit N", "timeout" or "not
// started". Never its output.
type AfterOutcome struct {
	Outcome string
	Exit    int
	At      time.Time
}

const afterLogMax = 64 << 10

// RunAfter runs the person's command once for a complete archive. It never
// touches the archive, whatever the outcome.
func RunAfter(ctx context.Context, s AfterSpec) AfterOutcome {
	res := AfterOutcome{At: time.Now().UTC(), Exit: -1}
	words, err := rolecfg.ShellSplit(s.Command)
	if err != nil || len(words) == 0 {
		res.Outcome = "not started"
		logLine(s.Log, fmt.Sprintf("backup.after_command: not started: %v", err))
		return res
	}
	cmd := exec.Command(words[0], words[1:]...)
	cmd.Dir = s.Dir
	cmd.Env = afterEnv(s)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = s.WaitDelay
	if cmd.WaitDelay <= 0 {
		cmd.WaitDelay = 5 * time.Second
	}
	out := &cappedLog{max: afterLogMax}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		res.Outcome = "not started"
		logLine(s.Log, "backup.after_command: not started: "+health.Redact(err.Error()))
		return res
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	grace := s.Grace
	if grace <= 0 {
		grace = 10 * time.Second
	}
	timer := time.NewTimer(s.Timeout)
	defer timer.Stop()
	var werr error
	select {
	case werr = <-done:
	case <-ctx.Done():
		werr = stopGroup(cmd.Process.Pid, done, grace)
		res.Outcome = "timeout"
	case <-timer.C:
		werr = stopGroup(cmd.Process.Pid, done, grace)
		res.Outcome = "timeout"
	}
	out.flush(s.Log)
	if res.Outcome == "timeout" {
		return res
	}
	// Judged by the command's own exit: ErrWaitDelay only says a child kept
	// its output open, and that child is left alone.
	var ee *exec.ExitError
	switch {
	case errors.As(werr, &ee):
		res.Exit = ee.ExitCode()
	case werr == nil || errors.Is(werr, exec.ErrWaitDelay):
		if cmd.ProcessState == nil {
			res.Outcome = "not started"
			return res
		}
		res.Exit = cmd.ProcessState.ExitCode()
	default:
		res.Outcome = "not started"
		return res
	}
	if res.Exit == 0 {
		res.Outcome = "ok"
	} else {
		res.Outcome = "exit " + strconv.Itoa(res.Exit)
	}
	return res
}

// stopGroup sends the command's process group SIGTERM, then SIGKILL after
// grace, and waits for the command.
func stopGroup(pid int, done chan error, grace time.Duration) error {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case err := <-done:
		_ = syscall.Kill(-pid, syscall.SIGKILL) // what it left behind
		return err
	case <-time.After(grace):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		return <-done
	}
}

// afterEnv is the backup's environment minus what can carry a broker's or a
// client's secret (spec "Never a secret"), except the names allowed, plus
// the four AGENTBUS_BACKUP_* variables.
func afterEnv(s AfterSpec) []string {
	allow := map[string]bool{}
	for _, n := range s.Allow {
		allow[n] = true
	}
	var out []string
	for _, kv := range s.Env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || strings.HasPrefix(name, "AGENTBUS_BACKUP_") {
			continue
		}
		if allow[name] || !secretVar(name, kv) {
			out = append(out, kv)
		}
	}
	scheduled := "0"
	if s.Scheduled {
		scheduled = "1"
	}
	return append(out, "AGENTBUS_BACKUP_ARCHIVE="+s.Archive, "AGENTBUS_BACKUP_DIR="+s.Dir,
		"AGENTBUS_BACKUP_PROJECTS="+strings.Join(s.Projects, ","), "AGENTBUS_BACKUP_SCHEDULED="+scheduled)
}

// secretVar: a variable that can carry a secret, by its name or (best
// effort) by its value's shape.
func secretVar(name, kv string) bool {
	u := strings.ToUpper(name)
	switch u {
	case "REDIS_PASSWORD", "REDIS_URL", "REDISCLI_AUTH":
		return true
	}
	for _, w := range []string{"PASSWORD", "PASSWD", "TOKEN", "SECRET"} {
		if strings.Contains(u, w) {
			return true
		}
	}
	for _, suf := range []string{"_KEY", "_PASS", "_PWD"} {
		if strings.HasSuffix(u, suf) {
			return true
		}
	}
	return health.Sensitive(kv)
}

// cappedLog keeps the first max bytes of the command's output.
type cappedLog struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (c *cappedLog) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
			c.truncated = true
		} else {
			c.buf.Write(p)
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	return len(p), nil
}

// flush writes the kept output to log under one heading line, each line
// through health.Redact.
func (c *cappedLog) flush(log io.Writer) {
	if c.buf.Len() == 0 {
		return
	}
	logLine(log, "backup.after_command output:")
	sc := bufio.NewScanner(&c.buf)
	sc.Buffer(make([]byte, 64<<10), afterLogMax+1)
	for sc.Scan() {
		logLine(log, health.Redact(sc.Text()))
	}
	if c.truncated {
		logLine(log, "backup.after_command: (output cut at 64 KiB)")
	}
}

func logLine(w io.Writer, s string) {
	if w != nil {
		fmt.Fprintln(w, s)
	}
}

// AfterEntry is one run of the command for an archive covering a project.
type AfterEntry struct {
	At      time.Time `json:"at"`
	Archive string    `json:"archive"`
	Outcome string    `json:"outcome"` // ok, exit N, timeout, not started
}

// Last is <state>/backup-last.json, never a secret: the latest scheduled
// run, written only by the chore, and, per project, the latest run of
// after_command, by hand or scheduled.
type Last struct {
	Scheduled *ScheduledRun         `json:"scheduled,omitempty"`
	After     map[string]AfterEntry `json:"after,omitempty"`
}

// ScheduledRun is the latest scheduled run (spec "Record"): its archive or
// its error, the projects in its scope, each project's EXEC time, and the
// error that left the secrets out (a recipient but no age), which counts as
// a failure for the health check.
type ScheduledRun struct {
	At           time.Time         `json:"at"`
	Archive      string            `json:"archive,omitempty"`
	Error        string            `json:"error,omitempty"`
	Projects     []string          `json:"projects"`
	Exec         map[string]string `json:"exec,omitempty"`
	SecretsError string            `json:"secrets_error,omitempty"`
}

// RecordScheduled replaces the record's scheduled part, leaving after as it
// was.
func RecordScheduled(path string, run ScheduledRun) error {
	l, err := ReadLast(path)
	if err != nil {
		return err
	}
	l.Scheduled = &run
	return writeLast(path, l)
}

// ReadLast reads the record; an absent one is empty.
func ReadLast(path string) (Last, error) {
	var l Last
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	return l, json.Unmarshal(b, &l)
}

// RecordAfter sets the command's outcome for projects in the record (0600,
// written to a temporary file and renamed), leaving the rest as it was.
func RecordAfter(path string, projects []string, e AfterEntry) error {
	l, err := ReadLast(path)
	if err != nil {
		return err
	}
	if l.After == nil {
		l.After = map[string]AfterEntry{}
	}
	for _, p := range projects {
		l.After[p] = e
	}
	return writeLast(path, l)
}

func writeLast(path string, l Last) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".backup-last-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
