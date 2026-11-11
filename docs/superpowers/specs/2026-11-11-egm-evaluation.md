# Evaluation: the Elephant-Goldfish Model (EGM) for agent-bus

- Status: for review (coder, foureyes). Task `egm-evaluation`. Author: architect, 2026-11-11.
- Read: `/data/AI-Docs/elephant-goldfish-model` at bbfb62e — the 7 skills, the 10 reviewer agents,
  `resources/`, `adapters/opencode`, `pyproject.toml`, `tests/`, `docs/`. Nothing installed or run.
- **Verdict: reject the plugin; adopt one habit (a drift check) inside the roles we already have.**

## What EGM is, concretely

A plugin made only of prompts: about 3 000 lines of Markdown. Each of its skills drives a single
interactive chat session: brainstorm → draft → review → resolve, plus bootstrap (docs for a
codebase) and drift report. The **elephant** is the main session and holds the context. The
**goldfish** are fresh subagents with no conversation history. They play 10 reviewer roles: zoom-out, comprehension, design critic,
structural, style, implementation readiness, codebase details, inventive catalyst, synthesizer,
bootstrap orchestrator. Its Python is only its own repository's tests, packaging and an opencode
installer; there is no runtime dependency. The drift report is not a tool either: it is a
67-line prompt that says "read each doc, find its code, list only the differences".

## Cost

- **Subagents and tokens.** A full `egm-review` runs stages 5 to 9. Stage 5 is zoom-out plus
  inventive catalyst (2 reviewers), stage 6 comprehension (1), stage 7 a loop of 3 reviewers,
  stage 8 zoom-out (1), and stage 9 a loop of 3. A loop converges only after two clean rounds in
  a row, capped at five rounds. Counting reviewers only, that is **16** fresh agents at two
  clean rounds per loop (2+1+3×2+1+3×2) and **34** at the cap (2+1+3×5+1+3×5). That is the
  direct-dispatch path. The synthesizer path, the one `egm-review` documents first, adds one
  synthesizer per loop round and one each for stages 5 and 8: **22** and **46**. Each agent
  re-reads the document and its linked files. Our r14 spec is about 1 000 lines, so
  one review would cost a few hundred thousand tokens, against two reviewers today.
- **Latency and people.** Every gate asks the person **one question at a time in chat**, then
  "remains idle across human turns". On the bus, nobody reads an agent's chat: the person reads
  busmon. EGM would stall or ask questions into the void. A review also needs fresh subagents:
  `egm-review` says to stop without a subagent tool. (Bootstrap instead runs its scouts inline.)
- **Fit with large codebases.** Bootstrap and drift "read files liberally", one document after
  another. Across our 67 specs, that is a whole-repository read per run, with no index and no
  cache.
- **Conflicts.** Its execution rules ("STRICTLY FORBIDDEN…", chat-only gates, "no summaries")
  would compete with our role skills and with the bus protocol in the same context.

## Overlap with what we have

| EGM | agent-bus today |
|---|---|
| Goldfish: fresh-context reviewers | foureyes and reviewer-codex: fresh sessions, and **different models**, which gives more independence than one model with a new context |
| Findings and "settled" list in the chat | verdict ledger bound to the head; gates; `agentbus verdicts` is the audit |
| `egm-resolve`: one question to the user | spec questions to the person through master and busmon asks, before any code |
| Zoom-out, design and readiness stages | one reviewer pass covers them; revisions are listed in the spec header |

## What it has that we lack

1. **A drift check between what a document says and what the code does**, run on demand. We
   review a change, but nobody re-reads a spec's "as built" lines, or a setting's description,
   against the code later.
2. **A comprehension test**: a fresh reader restates the spec without seeing the code. It is
   useful for long specs, but our reviewers already do it implicitly. Not worth a new step.

## Measured: EGM's drift logic, run by hand on one spec

The target was the self-repair watcher spec (2026-11-09), whose step 3 (the settings) is marked
*as built*. The check took six read-only commands and about ten minutes.
- The five keys, their environment variable names, defaults and the `DevOnly`/`FailClosed` rules
  all match `config/keys.go`.
- **Drift found:** the help text for `dispatch.self_repair` on busmon's settings screen says "the
  dispatch service reads it at its start", and its registry entry says `ReadBy: "dispatch"`. But
  `config.SelfRepairMode` has **no caller outside tests** on origin/main a3e60956: the watcher
  (steps 6–7) is not built yet. The person can switch it in busmon and nothing happens.
  `services.start_at_login`, by contrast, honestly says `(not yet)`. → A one-line fix for coder.

The check found a real defect at almost no cost, and without the plugin: the method is
valuable, the machinery is not.

## Smallest adoption that pays

- **foureyes, at every step PR of a spec:** for each "as built" line, `ReadBy`, help text or
  README claim the PR touches, grep the non-test caller before approving. Use a code-only,
  uncut search, which is our absence-claims rule. One line in the foureyes skill; zero runtime
  cost.
- **Architect, on demand only** (when master asks, or before a release): a drift pass on the
  specs whose steps merged since the last tag, appended to that release's notes. No new command,
  no new role, no dependency.
- Nothing else. No plugin install, no personas, no chat gates.

## Verdict

**Reject** EGM as a plugin or a workflow. Its value already exists in our setup: two
fresh-context reviewers of different models, a ledger bound to the head, and the person's
decisions through busmon. Its machinery costs tokens and latency, and assumes a person in the
chat. **Adopt** its drift check as the habit above. The first measured run found a real
mismatch.
