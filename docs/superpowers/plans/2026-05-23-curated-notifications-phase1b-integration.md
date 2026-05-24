# Curated Bus Notifications — Phase 1b (Integration Wiring) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Wire the bus foundation (Phase 1, PR #1) into the live system: Claudes emit curated reports, `hermes_laptop` relays only the worthwhile ones to Signal, and the per-task-stop Signal spam is removed.

**Architecture:** Emission in `adv-trading-ai` (intentional `agentbus report` + a guarded `Stop`-hook safety net on the bus, not Signal). Relay on the laptop via `~/.hermes/scripts/report_relay.sh` (note→verbatim+throttle+dedup+log; auto→keyword fast-path, fail-closed otherwise). No LLM in this phase.

**Status of prerequisites (done in this session):**
- `agentbus` rebuilt + deployed to `~/.local/bin` and `~/go/bin` (the `report` subcommand is live).
- `~/.hermes/scripts/report_relay.sh` created and offline-verified (routing/throttle/dedup correct).

**BLOCKER — agent identity convention (user decision required):** claude1/claude2 identity has no machine-discoverable source (not in any file; running sessions carry only `HERDR_PANE_ID` p_2/p_3). Each Claude session must export `AGENT_BUS_AGENT=claude1|claude2`. Decide the pane→agent mapping and set it per pane (e.g. in the herdr pane launch / shell rc). Until set, the Stop hook below no-ops (safe) and intentional reports must pass the agent explicitly.

---

### Task A: Run the relay; stop the legacy blanket-forward (laptop, `~/.hermes`)

**Files:**
- Modify: `~/.hermes/scripts/bus_watch_hdl.sh`
- Use: `~/.hermes/scripts/report_relay.sh` (already created)

- [ ] **Step 1: Stop `bus_watch_hdl.sh` forwarding everything to Signal**

Reports are now the only Signal source. In `bus_watch_hdl.sh`, replace the non-heartbeat arm's `hermes-notify "BUS: ${result}"` with a log-only line (hermes still acts on `hermes:cmd:hermes_laptop` for coordination, but does not auto-notify):

```bash
    *)
      echo "[${ts}] BUS_MESSAGE: ${result}" >&2
      # (no hermes-notify here: curated reports go through report_relay.sh)
      ;;
```

- [ ] **Step 2: Launch the report relay (mirror however bus_watch_hdl.sh is started)**

Run (and add to the same startup that launches `bus_watch_hdl.sh` — e.g. systemd user unit / herdr pane / rc):

```bash
agentbus listen "hermes:report:*" | ~/.hermes/scripts/report_relay.sh &
```

- [ ] **Step 3: Verify end-to-end (will send ONE real Signal message)**

```bash
agentbus report hermes_laptop "report relay smoke test"
# Expect: a Signal message "hermes_laptop: report relay smoke test"
tail -3 ~/.hermes/logs/reports.log   # Expect: NOTE-FWD hermes_laptop :: report relay smoke test
```

---

### Task B: Repoint the `Stop` hook off Signal (adv-trading-ai)

**Files:**
- Modify: `adv-trading-ai/.claude/settings.local.json` (the `Stop` hook, tracked file)

- [ ] **Step 1: Replace the hermes-notify command with a guarded bus report**

Replace the `Stop` hook command so it publishes a safety-net summary to the **bus** (not Signal), keyed by `AGENT_BUS_AGENT`, and does nothing if that is unset:

```json
"command": "test -n \"$AGENT_BUS_AGENT\" && agentbus report \"$AGENT_BUS_AGENT\" --auto \"$(cd \"$CLAUDE_PROJECT_DIR\" 2>/dev/null && git log -1 --format='%s' 2>/dev/null || echo 'task stop')\" 2>/dev/null || true"
```

Effect: the per-stop Signal ping is gone immediately; once `AGENT_BUS_AGENT` is set per session, the auto summary flows to the bus and `report_relay.sh` gates it (keyword → Signal, else skip).

- [ ] **Step 2: Verify no Signal ping on stop, and (with AGENT_BUS_AGENT set) a bus report appears**

```bash
# In a session with AGENT_BUS_AGENT=claude1, on Stop:
agentbus listen "hermes:report:*"   # should show: [hermes:report:claude1] auto|<commit subject>
# And: no "[adv-trading-ai ...] Claude task stop" Signal message.
```

- [ ] **Step 3: Commit on a dedicated branch in adv-trading-ai (not PRE-PROD directly)**

```bash
cd ~/Projects/adv-trading-ai
git checkout -b chore/bus-report-stop-hook
git add .claude/settings.local.json
git commit -m "chore(hooks): Stop hook publishes bus report instead of Signal ping"
```

---

### Task C: Emission guidance for the Claudes (adv-trading-ai)

**Files:**
- Modify: `adv-trading-ai/.claude/CLAUDE.md`

- [ ] **Step 1: Add a "Reporting to the human" section**

```markdown
## Reporting to the human (agent bus)

Set `AGENT_BUS_AGENT` (claude1/claude2) in your session. Surface only what a human
should see, in one short line:

    agentbus report "$AGENT_BUS_AGENT" "bug X corrigé"      # milestones, fixes, blockers, soak start/stop

Do NOT report routine task completion — the Stop hook already publishes an `--auto`
safety-net summary that hermes filters. hermes_laptop relays worthwhile reports to Signal.
```

- [ ] **Step 2: Commit (same branch as Task B)**

```bash
cd ~/Projects/adv-trading-ai
git add .claude/CLAUDE.md
git commit -m "docs: agent-bus reporting guidance for Claude sessions"
```

---

## Phase 2 (later, optional)

Replace `report_relay.sh`'s `AUTO-SKIP(no-keyword)` branch with a non-blocking
`hermes --oneshot` judge (`--oneshot` confirmed available) returning `{forward, summary}`,
~5s timeout, fail-open with `[auto]` marker on timeout/error.

## Self-Review

- **Spec coverage** (Phase 1 §Architecture 2-3 + de-noise): emission intentional+auto → Tasks B/C; relay note/auto+throttle+dedup+log → `report_relay.sh` (done) + Task A; remove blanket forward → Task A; AGENT_BUS_AGENT → prerequisite/Tasks B-C. ✓
- **Placeholders:** none — concrete commands/edits throughout.
- **Identity blocker** surfaced explicitly; Stop hook degrades to no-op when unset (safe). ✓
