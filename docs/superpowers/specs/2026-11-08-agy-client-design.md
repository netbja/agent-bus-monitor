# agy as a builtin launcher client (agy-client)

Status: spec for review (subject `agy-client-spec`), v0.29 chantier 7. No code on this branch.
Facts checked on 2026-11-07 against agy 1.5.3 on the person's machine (`agy --help`,
`agy models`, `agy changelog`, the docs agy bundles under
`~/.gemini/antigravity-cli/builtin/skills/`, its logs and settings). §3 lists what could not be
checked, and §9 orders the probe that closes it.

## 1. The gap

agent-bus already knows agy (Antigravity CLI, Google) everywhere except the launcher:

| Part | What exists on `origin/main` (43664c2) |
|---|---|
| Provider | `bus/stream.go` `clientProviders`: `agy` → `google` |
| Quota | `quota/agy.go` `AgyReader`: a `RESOURCE_EXHAUSTED (code 429) … Resets in <d>` line holds the `google` window until the stated reset |
| Model table | `models/readers.go`: `agy models` rows, other makers' models carry `family` |
| Reviewer pick | `cmd/agentbus/reviewers.go`: knows `agy`, refuses a same-maker model served through agy |
| Dispatcher | `dispatch/herdr.go`: the agy input box, idle/typed/approval screens (real captures of 1.3.3) |
| busmon | the agy icon, the `gemini` quota label |

The launcher does not. `rolecfg` has four builtin clients (`claude-code`, `codex`, `kimi-cli`,
`grok`), and a profile with `client = "agy"` fails with `unknown client agy: register an adapter`.
Today agy runs only by hand or through a home-made `[adapters.*]` script, as it did in the
v0.10 missions acceptance. The person wants `client = "agy"` in `roles.toml` to work like the
other four.

## 2. The change in one paragraph

`agy` becomes the fifth builtin client. The launcher runs `agy --model <m> [--mode <m>]
[--effort <r>] [--sandbox] --settings <session.json> -i "<role prompt and skill paths>"`. The role
prompt is the initial user turn, as with Codex. The shared `AGENTS.md` reaches agy on its own,
because agy reads it. The session settings are a file the launcher writes to its own cache. agy
uses them for this session only and never saves them. They make the permission mode explicit and
carry the manifest's deny rules that translate to agy. The launcher writes nothing into the
repository or into `~/.gemini`. The quota reader, model reader, dispatcher screens and icons
already exist. They get a re-check on 1.5.3 and nothing more, except one question about separate
quotas (§6).

## 3. What agy is (facts, and how each was checked)

### 3.1 Checked

| Fact | Source |
|---|---|
| Executable `agy` (`~/.local/bin/agy`, ELF), version 1.5.3; self-updates (`agy update`, `updater/`); the binary changed today at 21:07 | `agy --version`, `ls -l`, `~/.gemini/antigravity-cli/updater/` |
| Interactive by default. `-i`/`--prompt-interactive <p>` "run an initial prompt interactively and continue the session"; `-p`/`--print` is headless | `agy --help` |
| `--model <m>`, `--effort low\|medium\|high\|xhigh\|max`, `--mode accept-edits\|auto\|plan`, `--dangerously-skip-permissions`, `--sandbox` ("terminal restrictions enabled") | `agy --help` |
| `--settings <json\|path>`: "Settings for this session only, never saved: a JSON object of settings.json keys, inline or as a file path" | `agy --help` |
| Resume: `--conversation <id>`, `-c`/`--continue` (most recent); conversations are `~/.gemini/antigravity-cli/conversations/<uuid>.db` | `agy --help`, directory listing |
| Other flags this spec does not use: `--add-dir`, `--agent`, `--project`, `--new-project`, `--remote-agent`, `--remote-control`, `--output-format`, `--input-format`, `--json-schema`, `--log-file`, `--disable-slash-commands`, `--print-timeout`; subcommand `acp` (ACP stdio server) | `agy --help` |
| Models on this account: `gemini-3.8-flash-high`, `-medium`, `-low`, `claude-sonnet-4-6`, `claude-opus-4-6-thinking`, `gpt-oss-120b-medium`. No Gemini Pro row | `agy models` |
| Instructions: agy reads `GEMINI.md` and `AGENTS.md` from the working directory up to the repository root, and `.agents/rules/*.md`. Each file is capped at 24 000 bytes, and all of them share a 20 000-token budget; files over budget become path pointers | bundled `agy-customizations/docs/rules.md` |
| Settings: `~/.gemini/antigravity-cli/settings.json`; `permissions` has `allow`, `deny`, `ask`; the entries seen are `command(<prefix>)`; the default tool permission is `request-review` | the person's settings file, and the startup log line `permissions=&{Allow:[…] Deny:[] Ask:[]}, toolPermission=request-review` |
| Hooks: one `hooks.json` per customization root (`.agents/` in a project, `~/.gemini/config/` globally, or a plugin's `hooks/`). Events: `SessionStart`, `PreToolUse`, `PostToolUse`, `PreInvocation`, `PostInvocation`, `Stop`. JSON on stdin and stdout, with `conversationId` in every payload; a hook runs in the folder of its `hooks.json` | bundled `docs/hooks.md` |
| Logs: `~/.gemini/antigravity-cli/log/cli-<YYYYMMDD>_<HHMMSS>.log` (glog). Each start logs `You are not logged into Antigravity` and `doRefreshQuota: skipped (not logged in)` before the token loads. That is normal startup noise, not a signed-out account | 32 local logs |
| `--dangerously-skip-permissions` still blocks the system command denylist (1.5.2). In `-p` mode it denies access outside the workspace when an enterprise admin requires review | `agy changelog` |
| agy retries 429 and 5xx itself and honours `Retry-After` (1.5.0) | `agy changelog` |
| The banner names the plan: `(Antigravity Starter Quota)` | `dispatch/testdata/screens/agy-empty.txt` (1.3.3) |

### 3.2 Not checked, and why

Each item below needs a live session, and each request spends the person's quota. I did not
start one. §9 is the probe that settles them.

1. **Which form `--model` takes.** The settings file stores the display name (`"Gemini 3.8 Flash
   (High)"`). `agy models` prints an id and a display name. Which one `--model` accepts is not
   known.
2. **Whether `--settings` accepts `permissions` (and `deny`), `defaultMode`, and a hooks path.**
   The binary contains the strings `defaultMode`, `permissions`, `hooks_paths` and
   `hooksInheritUser`. None of them is documented as a settings key.
3. **The deny syntax for anything but a command.** Is there a file-read rule (an equivalent of
   Claude's `Read(**/.env)`)? Unknown.
4. **Whether a session started with `-i` takes a multi-kilobyte prompt** and keeps it as its first
   turn, and whether it shows a folder-trust dialog in a new worktree.
5. **The 1.5.3 quota message.** None of the 32 local logs has a `code 429` line, so `AgyReader`'s
   pattern is checked only against its test sample. The binary also has a `MODEL_CAPACITY…`
   string, which is probably a server capacity error and not an account quota.
6. **Whether agy has separate quotas** (per model family, per window) and how a hit names its pool.
   The person says agy has "many quotas". Nothing local shows them.
7. **Whether `--settings` replaces or merges with the person's own settings file** (foureyes).
   `~/.gemini/antigravity-cli/settings.json` holds the person's `permissions.allow`: 52 entries
   today, counted and not read. If a session `permissions` object merges with it, an agy role
   inherits every command the person allowed for their own use, with no prompt. A later
   `toolPermission` change in that file would also reach every `default` role. This is the grok
   lesson §4.4 cites, in another key.
8. **Where the official install page is.** The bundled guide points to
   `https://antigravity.google/docs/cli/reference`. I did not open it.

## 4. Design

### 4.1 Client registration

- `rolecfg`: add `agy` to `Clients` (`Exe: "agy"`, `Name: "Antigravity CLI (Google)"`,
  `Install:` the URL that §3.2.8 confirms) and to `builtinBinary`, and make `isBuiltin` return
  true for it. `TestClientsMatchTheLauncher` already ties the two together.
- The client is named `agy`, the name that `clientProviders`, `reviewers.go`, the dispatcher
  screen table and the icon map already use. The provider is `google`.

### 4.2 Profile options (validated in `config.go`, beside grok's block)

| Key | Values | Becomes |
|---|---|---|
| `permission` | `default` (or absent), `accept-edits`, `auto`, `plan`, `skip` | `default`: no `--mode`. agy then asks before each tool use (`request-review`); the session settings pin that (§4.4). `accept-edits`/`auto`/`plan`: `--mode <v>`. `skip`: `--dangerously-skip-permissions` |
| `sandbox` | `true` / `false` | `--sandbox` when true |
| `reasoning` (profile field) | `low`, `medium`, `high`, `xhigh`, `max` | `--effort <v>`. These are the values `--help` lists, so the 2026-09-27 rule is met (an override names the levels the client supports) |

- Any other key is refused, as for grok (`checkKeys`).
- `plan` is accepted, but no shipped role uses it. The reason is the one the manifest gives for
  grok and Kimi: no `--help` says whether a plan-mode agent can still run the `agentbus` commands
  it needs to report.
- `skip` and `auto` go in the profile's risk parts (the `roles doctor` and dry-run summary that
  already lists `sandbox=danger-full-access` for Codex and `permission=bypassPermissions` for
  grok). `sandbox = false` with `skip` is listed as well.
- A model id whose maker is not Google (`claude-*`, `gpt-oss-*`) is legal. `models` already gives
  it its `family`, and `reviewers.go` already refuses it as an independent reviewer of that
  maker's work. This spec adds nothing there.

### 4.3 The command line (`build`)

```
agy --model <model> [--mode <mode> | --dangerously-skip-permissions] [--effort <r>] [--sandbox]
    --settings <cache>/agy-<hash>.json -i "<role prompt and skill paths>"
```

- **The role prompt is the initial user turn (`-i`)**, as for Codex: it adds to whatever agy
  already reads and replaces nothing. agy has no flag that appends to its system prompt (Claude's
  `--append-system-prompt`, grok's `--rules`), and none that takes an agent file (Kimi's
  `--agent-file`). `--agent` takes the name of an agent defined in `~/.gemini/agents/`, and the
  launcher does not write there.
- **The dry run** prints `-i "<role prompt and skill paths>"` and `--settings
  "<generated agy settings>"` and writes nothing, as the other clients do.
- **Not used:** `--remote-agent` and `--remote-control` (both would run or expose the session off
  this machine), `--project`/`--new-project` (agy's own projects), `--add-dir` (the worktree is
  the working directory), `-p` (a role is interactive, and the dispatcher wakes it in its pane).
- **How agy learns its bus role** is the same as for every client. The launcher exports
  `AGENT_BUS_PROJECT`, `AGENT_BUS_AGENT` and the rest. The `-i` prompt names the role file and the
  skills' `SKILL.md` paths, which the agent reads. `AGENTS.md`, the entry point every agent
  shares, reaches agy natively (§3.1), and so does `docs/memory/INDEX.md`, which it carries.
  agy's own skill discovery (`.agents/skills.json`) is not used, because it would mean writing
  into the repository.

### 4.4 Session settings (`--settings`, a cache file)

The launcher writes one JSON object to its cache, like Kimi's agent file, and passes its path:

```json
{ "permissions": { "deny": ["command(agentbus suspend)", "…"] },
  "defaultMode": "<only if §9 shows the key exists and is needed to pin default>" }
```

- **It pins the mode.** grok taught that a client with no mode on its command line falls back to
  the person's own config (`permission_mode = "always-approve"` in `~/.grok/config.toml`). The
  same can happen with agy's settings `defaultMode`. If §9 shows the key exists, the session file
  sets it to the profile's mode; if it does not, `default` relies on agy's `request-review`, and
  the risk summary says so.
- **Deny rules.** A manifest `Bash(<prefix>*)` or `Bash(<prefix>)` deny becomes
  `command(<prefix>)`. A rule that does not translate (`Read(…)`, `Edit(…)`, a `*` in the middle
  of a pattern) is not dropped silently: the launcher prints one line,
  `agy: N tool rules have no agy form and are not enforced: …`, and `roles doctor` shows the
  same count. If §9 finds a file rule syntax, `Read`/`Edit` translate as well. The translation is
  one pure function, and it is tested on its own.
- **Allow rules** are not translated in v1. An agy role asks the person, or runs with the mode its
  profile grants, **plus whatever it inherits from the person's own settings** (next point).
- **The person's settings are inherited until proven otherwise** (§3.2.7, foureyes). §9 step 2
  settles which wins. The rule, by outcome:
  - **The session replaces them.** The session object carries an explicit `"allow": []` and
    pins the tool permission (`toolPermission`, or `defaultMode`, whichever key §9 shows works).
    `default` then means "ask", and the docs may say so.
  - **The session merges with them** (or nothing pins the tool permission). The launch prints
    `agy: inherits N allow rules and toolPermission=<v> from ~/.gemini/antigravity-cli/settings.json`.
    The profile's risk parts and `roles doctor` carry the same count and value. Neither the docs,
    the manifest comment nor the risk summary call `default` "ask". The launcher reads only the
    count and that one value, never the rules' text into the bus or a log. It never edits the
    file.
  - **Either way**, a deny in the session still applies, if §9 step 2 shows that a session deny
    beats an inherited allow. If it does not, the denies are named as not enforced, like an
    untranslated rule.
- **Defence in depth, not a boundary.** The same caveat as `[tool_rules]`: agy matches command
  text, and a process the agent starts can still read files.

### 4.5 Turn hooks (auto-state)

agy's `PreInvocation` (before each model call), `Stop` (the loop ends) and `SessionStart` map to
the existing turn-start, turn-end and resume hooks (`launchHooks`). Where the hook file goes is
the open part:

1. **Through `--settings`**, if §9 shows that a hooks path key (`hooks_paths`) works there. The
   hook file then lives in the launcher's cache, and nothing is written outside it. This is
   recommended.
2. **`<worktree>/.agents/hooks.json`**, written and listed in `.git/info/exclude` the way grok's
   are. This is rejected: agy has **one** `hooks.json` per root, so the file would collide with a
   repository's own hooks.
3. **No hooks in v1.** The agent declares its state itself, and the dispatcher reads its pane
   from the screens it already knows. This is the fallback if option 1 fails.

`PreInvocation` fires once per model call, several times a turn. A repeated `working` is
harmless (it is idempotent). The `SessionStart` payload carries `conversationId`, which is what
§4.6 needs.

### 4.6 Resume

v1 keeps `LaunchResume` Claude-only. The refusal `session resume is supported only for Claude
Code` already covers agy, so `agentbus up --resume` starts agy roles fresh. A follow-up can pass
`--conversation <id>`, with the id coming from the `SessionStart` hook (§4.5 option 1). It is out
of scope here.

### 4.7 Preflight

`preflight` checks `agy --help` for `--model`, `--settings`, `--prompt-interactive`, plus
`--mode` / `--dangerously-skip-permissions`, `--effort` and `--sandbox` when the profile uses
them. agy updates itself, and the binary was replaced today, so this check and the client
fingerprint (`clientfp`, the client-reload notice) are what catch a flag that changes.

## 5. Which roles agy may hold

| Role | Recommendation | Why |
|---|---|---|
| **Second reviewer** (beside `reviewer-codex`) | **yes, first** | It is a reviewer from another maker, which is what `reviewers.go` looks for. agy already did a final mission review in v0.10. A reviewer's profile pins a Gemini model, because a `claude-*` model through agy is the author's own maker |
| `scout` / research | yes | It reads and answers. Default permission is enough |
| `ui-*` panel | yes | The same use as `ui-grok` and `ui-kimi` |
| `coder` / implementer | not by default; the person decides (§10 Q1) | The only Gemini models on the account are Flash, and an implementer needs edits, which means `accept-edits`, `auto` or `skip` |
| `master`, `docs` | no | `pops` writes Claude Code allow rules, and `delivers` pushes. Both stay on Claude in the shipped manifest. No new refusal is added: these are manifest choices, not launcher rules |
| `sentinel` | no | Its narrow restart and model-refresh actions rely on the Claude tool rules |

**A reviewer's verdict must count.** The approved auto-merge spec (`56d1e54`) counts an agy
verdict only through `models.MakerOf(provider, model)`, with the model taken from the launcher's
`AGENT_BUS_MODEL`. A model the table does not know gives an unknown maker, and that verdict never
counts. So the agy reviewer profile carries a model id exactly as `agy models` prints it
(`gemini-3.8-flash-high`). That id is the model table's key, and `AGENT_BUS_MODEL` is
`profile.model` unchanged. If §9 step 1 shows `--model` takes only the display name, the
launcher maps id → display name for the command line alone, using the `agy models` rows, and
leaves `AGENT_BUS_MODEL` as the id. A test pins that `AGENT_BUS_MODEL` is an id `MakerOf`
resolves to `google`.

**Shipped profile.** The profile goes in `roles.toml`, and no role uses it until the person picks
one (§10 Q1). The same block goes in `examples/roles.mixed.toml`:

```toml
# agy (Antigravity CLI, Google): a reviewer or panelist from another maker. permission stays
# default: agy's own tool permission, plus any allows it inherits from the person's agy settings
# (the launch names them; agy-client spec §4.4). The model is Gemini, never
# a claude-* row agy also serves: that would review Anthropic's work with Anthropic's model.
[profiles.agy-review]
client = "agy"
provider = "google"
model = "gemini-3.8-flash-high"   # id form, unless §9 shows --model wants the display name
[profiles.agy-review.options]
sandbox = true
```

## 6. Quota

- **It already exists.** `AgyReader` opens only the 8 newest logs modified in the last 8 days
  (their last 256 KiB), finds `RESOURCE_EXHAUSTED (code 429): Individual quota reached. … Resets
  in <d>`, and holds the `google` window at 100 % until then. agy logs no proof of an answer, so
  nothing ends a hit early. No credential is opened.
- **Several quotas, one window: the open problem.** agy serves Gemini, Claude and GPT-OSS models
  from one account. If each family, or each plan window, has its own pool, then one hit on
  `claude-opus-4-6-thinking` holds the whole `google` window. The swap then avoids a Gemini
  profile that still has quota. The fix belongs in the reader: key the window by the pool the 429
  line names (a model or a quota name). It needs real lines to do that, and none exist locally
  (§3.2.5–6). **This spec does not change the reader.** It asks for samples (§10 Q3), and §9's
  probe records any 429 it meets.
- **A capacity error is not a quota.** A `MODEL_CAPACITY…` error (a server-side shortage, which
  agy retries itself) must never be read as a quota hit. The reader's pattern already requires
  `Resets in`. A test pins that a capacity line holds nothing.
- **The plan name** (`Antigravity Starter Quota` in the banner) is not read. It is not needed.

## 7. Failure modes

| Failure | What happens | Handling |
|---|---|---|
| agy signed out | agy shows its sign-in screen in the pane. `--help` works signed out, so preflight cannot see it | busmon shows the pane blocked (the existing prompt-notify, since it is not the input box). Docs: "sign in once with `agy` by hand". `roles doctor` adds no check: the log's "not logged in" line appears at every start (§3.1) |
| Quota hit (hours to days: `66h41m` in the reader's sample) | agy stops answering | The `google` window is held, and the existing swap ledger moves the role to another profile |
| Several pools (§6) | one hit holds every agy profile | Known limit in v1, written in the docs; §10 Q3 |
| Capacity or 5xx | agy retries | Nothing to do; not a quota |
| An approval prompt | the pane waits for the person | The dispatcher already has the `agy-approval` screen and does not type into it; prompt-notify rings once |
| Self-update changes a flag | the launch would break | Preflight refuses with `agy lacks required launch flags`, and the fingerprint flags the new client |
| The dispatcher's screens are from 1.3.3 | 1.5.3 may draw its input box differently | §9 captures 1.5.3 screens and replaces the `agy-*.txt` fixtures if they differ |
| A deny rule with no agy form | the rule is not enforced | Named at launch and in `roles doctor` (§4.4), never silent |
| The role prompt passes 24 KB, or the rules budget | none: `-i` is a user turn, not a rules file | The 24 000-byte/20k-token limits apply to `AGENTS.md`. It is 5 522 bytes today, so there is room. A test may pin it under agy's cap |
| The person's own allows reach the role | a `default` role runs commands without asking | §4.4: proven by §9 step 2, and named at launch and in `roles doctor` until it is replaced |

## 8. Security

- **agy's own default, which is "ask" only once proven.** The shipped profile is `default` +
  `sandbox = true`. `default` means "ask" only if §9 step 2 shows the session replaces the
  person's allows and pins the tool permission. Until then, the inherited allow count and the
  tool permission are named (§4.4). `skip` exists, but no shipped role uses it, and it shows in
  the risk summary.
- **Nothing written outside the cache.** No `GEMINI.md`, no `.agents/`, no change to
  `~/.gemini/antigravity-cli/settings.json`. `--settings` is session-only, as `--help` states.
- **Off this machine.** `--remote-agent` and `--remote-control` are never passed, and a profile
  option to add them is not offered.
- **Secrets.** The manifest's `Read(**/.env)` family of denies has no checked agy form (§3.2.3).
  Until it has one, an agy role's protection of secret files is agy's own `request-review` prompt,
  and the launch line names the gap. That is one more reason not to ship `skip`.
- **Data egress.** The code an agy role reads goes to Google, and through Google to Anthropic or
  OpenAI's open model when such a model is chosen. This is the same choice as any client, and
  the docs name it.
- **No credential is read.** The quota and model readers are unchanged: logs and `agy models`
  only.

## 9. Step 0: the live probe (the person runs it, or allows it; it costs a few requests)

Run it in a throwaway worktree, with a fake broker project:

1. `agy --model gemini-3.8-flash-high -p "say ok"`, then the same with `"Gemini 3.8 Flash (High)"`:
   which form `--model` takes (§3.2.1).
2. `agy -p --settings '{"permissions":{"deny":["command(echo)"]}}' "run: echo hi"`: whether the
   session deny applies. Then **replace or merge** (§3.2.7): pick a command the person's own file
   allows (`command(ls)` is one), and run `agy -p --settings '{"permissions":{"allow":[]}}' "run:
   ls"`. A prompt or a refusal means the session replaced the file's allows; `ls` running with no
   prompt means it merged. Then run a session `deny` of that same command: whether a session deny
   beats an inherited allow. Then the same with a hooks-path key pointing at a temp `hooks.json`
   whose `Stop` hook touches a file (§3.2.2, §4.5).
3. `agy --settings '{"defaultMode":"…"}'`: whether the key exists.
4. `agy -i "$(cat <a 10 KB prompt>)"` in a new worktree: whether the turn is kept, and whether a
   trust dialog appears (§3.2.4). Capture the idle, typed and approval screens with `herdr`
   (§7, the dispatcher fixtures).
5. `grep -h 'code 429\|MODEL_CAPACITY' ~/.gemini/antigravity-cli/log/*.log`, now and after any hit,
   for §6.

The implementation plan starts after step 0. Each answer settles a branch of §4.4–§4.5, and the
spec gets an amendment with the results before coding starts.

## 10. Questions for the person

1. **Which role first?** The recommendation is a second reviewer (§5). Do you also want it as an
   implementer, knowing that only Flash models are listed on your account?
2. **Should `skip` (`--dangerously-skip-permissions`) be allowed in a profile at all?** It is
   allowed in the recommendation, never shipped, and flagged in the risk summary.
3. **The quotas.** What are the "many quotas" you see: one per model family, a daily and a weekly
   one, something else? Could you paste one 429 line (with the email removed) the next time agy
   hits one? Without it, v1 keeps one `google` window.
4. **The probe (§9).** Will you run it, or may an agent run it? It spends a few requests.
5. **The default model:** `gemini-3.8-flash-high` (the recommendation) or `-medium` to save quota?

## 11. What the implementation touches

- `rolecfg`: `clients.go` (`Clients`), `launch.go` (`builtinBinary`, `build`, `preflight`, the
  risk parts), `config.go` (the agy options block, `isBuiltin`), and a new agy settings unit: the
  session JSON and the `[tool_rules]` → `permissions.deny` translation, a pure function. Add a
  hooks writer only if §4.5 option 1 holds.
- Tests. The seam is the one the other clients use: the dry-run `Launch` output and `build()`'s
  argv, with a fake `agy` on `PATH` that prints the real 1.5.3 `--help` (kept as testdata).
  Cases: each permission value, `sandbox`, `reasoning`, an unknown option refused, an untranslated
  rule named, preflight refusing a missing flag, and the dry run writing nothing.
  `TestClientsMatchTheLauncher` and `migration_test` cover the new profile. The quota reader gets
  the capacity-line case (§6). The dispatcher fixtures are refreshed if §9 finds new screens.
- Installer and `agentbus up`: the "no agent CLI on PATH" guidance prints `ClientGuidance`
  (`cmd/agentbus/up.go`), so agy appears there once it is in `Clients`, with no other code.
  `scripts/setup-legacy.sh` already probes `agy`. The tests stub `agy` as they stub the other
  clients.
- Docs: `README.md` and `README.fr.md` (the client list), `docs/AGENT-PROFILES.md` (the agy
  options), `docs/OPERATIONAL-GUIDE.md` and `.fr.md` (sign-in, the quota limit of §6),
  `docs/operator-notes.md` (the rules agy cannot enforce), `examples/roles.mixed.toml`, and the
  `roles.toml` comment.
- Not touched: `bus/`, `dispatch/` (except its fixtures), `models/`, `cmd/busmon` (coder is
  rewriting it).
