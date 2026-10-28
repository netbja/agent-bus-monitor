package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
)

// The resume hook (v0.21.1, spec
// docs/superpowers/specs/2026-10-21-launcher-resume-design.md): the launcher
// gives every Claude Code session a SessionStart hook running `agentbus roles
// report-resume <agent-launch>`. It tells herdr the command that resumes the
// session through agent-launch, so herdr's native restore brings the agent
// back with its role, rules and identity instead of a bare `claude --resume`.
// It reports no state and takes no authority over the pane: only a resume
// command, which herdr accepts from a source whose agent label matches the
// one it detects. If herdr never accepts it, herdr:claude's own resume
// applies, as before.

// reportResume is the hook. It never prints on stdout (a SessionStart hook's
// stdout becomes Claude's context) and always exits 0: it hands the report to
// a detached sender and returns at once, so Claude's start never waits on
// herdr. It also hands the session to a detached prover (lifecycle spec, part
// D), with its ancestors read here: once detached, the sender is reparented
// and its own ancestry proves nothing.
func reportResume(args, env []string, stdin io.Reader, stdout, stderr io.Writer, spawn func(argv []string) error, ancestors func() []dispatch.ProcID) int {
	get := func(k string) string { v, _ := lookupEnv(env, k); return v }
	pane, herdr, role, project, manifest := get("HERDR_PANE_ID"), get("HERDR_BIN_PATH"), get("AGENT_BUS_AGENT"), get("AGENT_BUS_PROJECT"), get("ROLES_TOML")
	if len(args) != 1 || get("HERDR_ENV") != "1" || pane == "" || herdr == "" || role == "" || project == "" || manifest == "" {
		return 0 // not a launched agent inside herdr: nothing to report
	}
	var in struct {
		Event     string `json:"hook_event_name"`
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
	}
	raw, _ := io.ReadAll(io.LimitReader(stdin, 64<<10))
	if json.Unmarshal(raw, &in) != nil || in.Event != "SessionStart" || in.SessionID == "" || in.AgentID != "" {
		return 0 // a subagent's start resumes nothing
	}
	if !bus.ValidName(project) {
		return 0
	}
	paths, err := service.PathsFrom(get)
	if err != nil {
		return 0
	}
	logPath := filepath.Join(paths.StateDir(project), "resume.log")
	if chain := ancestors(); len(chain) > 0 && bus.ValidName(role) && bus.ValidSessionID(in.SessionID) {
		argv := []string{"roles", "prove-session", logPath, project, role, in.SessionID, "--"}
		for _, p := range chain {
			argv = append(argv, strconv.Itoa(p.PID)+":"+strconv.FormatUint(p.Start, 10))
		}
		if err := spawn(argv); err != nil {
			resumeLog(logPath, time.Now(), "session prover not started: "+err.Error())
		}
	}
	profile, err := resumeProfile(manifest, role, get("AGENT_BUS_PROFILE"))
	if err != nil {
		resumeLog(logPath, time.Now(), "refused: "+err.Error())
		return 0
	}
	argv, err := rolecfg.ResumeArgv(args[0], manifest, role, project, profile, in.SessionID)
	if err != nil {
		resumeLog(logPath, time.Now(), "refused: "+err.Error())
		return 0
	}
	if err := spawn(append([]string{"roles", "report-resume-send", logPath, herdr, pane, "--"}, argv...)); err != nil {
		resumeLog(logPath, time.Now(), "sender not started: "+err.Error())
	}
	return 0
}

// resumeProfile is the --profile of the resume command: the profile the
// agent runs on when it is not its role's own (a swapped role), else "".
func resumeProfile(manifest, role, running string) (string, error) {
	m, err := rolecfg.Load(manifest)
	if err != nil {
		return "", err
	}
	if _, ok := m.Roles[role]; !ok {
		return "", fmt.Errorf("role %s not in %s", role, manifest)
	}
	own, err := m.Resolve(role)
	if err != nil {
		return "", err
	}
	if running == "" || running == own.Profile {
		return "", nil
	}
	return running, nil
}

// spawnDetached starts this binary with argv in its own session, without
// stdio, and does not wait for it.
func spawnDetached(argv []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, argv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// senderDeps is what the sender reads and runs, injected for tests.
type senderDeps struct {
	now    func() time.Time
	sleep  func(time.Duration)
	run    func(ctx context.Context, bin string, args ...string) ([]byte, error)
	budget time.Duration // how long herdr may answer resume_not_accepted
}

func defaultSenderDeps() senderDeps {
	return senderDeps{now: time.Now, sleep: time.Sleep, budget: 15 * time.Second,
		run: func(ctx context.Context, bin string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, bin, args...).CombinedOutput()
		}}
}

// reportResumeSend reports the resume command to herdr: <log> <herdr> <pane>
// -- <argv...>. While Claude is not yet detected in the pane herdr answers
// resume_not_accepted; it retries once a second within the budget. Every
// outcome is one line of the log.
func reportResumeSend(args []string, d senderDeps) int {
	if len(args) < 5 || args[3] != "--" {
		return 0
	}
	logPath, herdr, pane, argv := args[0], args[1], args[2], args[4:]
	deadline := d.now().Add(d.budget)
	for {
		call := append([]string{"pane", "report-agent-session", pane, "--source", "agentbus", "--agent", "claude",
			"--seq", strconv.FormatInt(d.now().UnixNano(), 10), "--"}, argv...)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := d.run(ctx, herdr, call...)
		cancel()
		switch {
		case err == nil:
			resumeLog(logPath, d.now(), "accepted: "+rolecfg.ShellJoin(argv))
			return 0
		case !bytes.Contains(out, []byte("resume_not_accepted")):
			resumeLog(logPath, d.now(), "herdr refused: "+oneLine(out, err))
			return 0
		case !d.now().Before(deadline):
			resumeLog(logPath, d.now(), "not accepted within "+d.budget.String()+"; herdr's own resume applies")
			return 0
		}
		d.sleep(time.Second)
	}
}

// sessionProver is the bus as the prover needs it.
type sessionProver interface {
	Agents(ctx context.Context) (map[string]bus.AgentSnapshot, error)
	ProveSession(ctx context.Context, agent string, pid int, pidStart uint64, session string) error
}

// openProver connects to the project's bus from the environment the hook
// left the prover.
func openProver(project string) (sessionProver, func(), error) {
	r, err := bus.Connect("")
	if err != nil {
		return nil, nil, err
	}
	b, err := bus.Open(r, project)
	if err != nil {
		r.Close()
		return nil, nil, err
	}
	return b, func() { r.Close() }, nil
}

// proveSessionSend records the new session as the one proved with the agent's
// process: <log> <project> <agent> <session> -- <pid:start>..., the hook's
// ancestors. It proves only when the stored verified pid, with its start time,
// is one of them; ProveSession checks again in its transaction. Every outcome
// is one line of the log.
func proveSessionSend(args []string, open func(project string) (sessionProver, func(), error)) int {
	if len(args) < 6 || args[4] != "--" {
		return 0
	}
	logPath, project, agent, session := args[0], args[1], args[2], args[3]
	chain := map[string]bool{}
	for _, a := range args[5:] {
		chain[a] = true
	}
	b, done, err := open(project)
	if err != nil {
		resumeLog(logPath, time.Now(), "session not proved: "+err.Error())
		return 0
	}
	defer done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snaps, err := b.Agents(ctx)
	if err != nil {
		resumeLog(logPath, time.Now(), "session not proved: "+err.Error())
		return 0
	}
	s := snaps[agent]
	if s.PID <= 0 || !chain[strconv.Itoa(s.PID)+":"+strconv.FormatUint(s.PIDStart, 10)] {
		resumeLog(logPath, time.Now(), "session not proved: "+agent+"'s verified process is not this hook's client")
		return 0
	}
	if err := b.ProveSession(ctx, agent, s.PID, s.PIDStart, session); err != nil {
		resumeLog(logPath, time.Now(), "session not proved: "+err.Error())
		return 0
	}
	resumeLog(logPath, time.Now(), "session proved: "+agent+" "+session)
	return 0
}

func oneLine(out []byte, err error) string {
	s := strings.Join(strings.Fields(string(out)), " ")
	if len(s) > 300 {
		s = s[:300]
	}
	if s == "" {
		return err.Error()
	}
	return s
}

// resumeLog appends one line to the project's resume log (0600), starting
// over once it passes 256 KiB. A log that cannot be written is not an error.
func resumeLog(path string, at time.Time, line string) {
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if st, err := os.Stat(path); err == nil && st.Size() > 256<<10 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", at.UTC().Format(time.RFC3339), line)
}
