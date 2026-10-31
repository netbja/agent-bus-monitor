# A local model as a test peer on the bus

Status: design. Plan task `local-llm-peer`. Spec only: nothing here is built yet.

## Why

Bernard's question: does a model running on this machine work as an agent-bus peer? He chose the
shape: an **OpenAI-compatible endpoint** (Ollama or llama.cpp's `llama-server`), its URL and
model **in the config file**, and **no secret** in that file. This spec says how such a peer is
launched, identified, woken, limited and checked, and gives master a short test to run.

It is a **test peer**: read-only first, one per project, small. It proves the bus contract with a
model nobody bills; it does not try to match a vendor client.

## The host, and what it allows

- No model runtime installed today. A Quadro T2000 with **4 GB** of VRAM and 62 GB of RAM: a
  3B model at 4-bit (about 2 GB) fits the GPU whole; a 7B at 4-bit (about 4.5 GB) runs split
  between GPU and CPU, slower; anything larger is CPU only. Every figure here is to be measured
  by the test, never claimed in the docs before.
- The consequence that shapes the design: **every token of prompt costs time** on this host. A
  client that sends several thousand tokens of instructions and tool schemas each turn spends
  that before the model writes one word, and fills a small model's context.

## Alternatives

| | How | For | Against |
|---|---|---|---|
| **A. A minimal peer in `agentbus` (recommended)** | `agentbus peer`: a small Go loop that receives the agent's cmd entries through the bus API, calls `/v1/chat/completions` with a short role prompt and a handful of read-only tools, and answers on the thread | The prompt is ours and short; the tools are exactly the ones we allow; no third-party install; no config of the person's touched; testable with a fake endpoint | New code (estimated 500–700 lines with tests); no shell or edit tools, by design |
| B. Codex CLI pointed at the endpoint | `codex -c model_providers.local.base_url=… -c model_provider="local"` (or `--oss`), set at launch only, so `~/.codex/config.toml` is not edited | No new client; the dispatcher's codex signature and `bind` already exist; full agent | Codex's own instructions and tool schemas are large for this host (to be measured); small models follow its tool protocol poorly; which wire API a given Codex version needs from Ollama/llama.cpp must be verified per version; it still reads the person's `~/.codex` for everything else |
| C. Kimi, Grok or agy pointed at it | Their provider settings | — | Each needs its own config file edited (`~/.kimi-code`, `~/.grok`, …), which the brief rules out |
| D. A third-party CLI (aider, opencode, goose, …) | Their OpenAI-compatible flags | Feature-rich | A new install and its own config for a test; no dispatcher signature; edit-oriented, not a read-only peer |
| E. The dispatcher calls the model | — | — | Refused by rule: the dispatcher runs no code and types only a wake line |

**Recommendation: A.** It is the lightest thing that answers the question on this host, and it
keeps every limit in our hands. B stays the documented way for a person who wants a full local
agent and has a larger machine; nothing in A prevents it later.

## Configuration

### The endpoint: `config.toml`

The machine's config file (spec `2026-10-29-config-file-design.md`:
`$XDG_CONFIG_HOME/agentbus/config.toml`, else `~/.config/agentbus/config.toml`) gains one table
per endpoint:

```toml
[llm.local]
url   = "http://127.0.0.1:11434/v1"   # Ollama; llama-server: http://127.0.0.1:8080/v1
model = "qwen2.5-coder:3b"            # the default model of this endpoint
# remote = true                       # required for a URL that is not loopback
```

- **No secret, ever.** There is no key field; a `key`, `token` or `password` key is refused at
  load with the reason. Local servers need none. A server that needs one is out of scope for the
  test.
- **Loopback only by default.** The peer sends repository text to the URL; a URL whose host is
  not `127.0.0.1`, `::1` or `localhost` is refused unless `remote = true`, so a typo never ships
  the code to another host.
- `url` must be `http` or `https`, with no user info and no query. `model` is a non-empty
  one-line string.
- The config file is not built yet. Until it is, the peer reads only its `[llm.*]` tables from
  that path with a small reader (unknown keys refused); when the config registry lands, the
  table moves under it unchanged. Like the rest of that file, it is the person's preference, not
  a per-project fact.

### The role: `roles.toml`

The peer is launched through the launcher's existing **adapter** path (a client that is not
builtin: `[adapters.<client>] executable`, called with `--spec-file <resolved launch.json>`), so
`rolecfg` needs no new client:

```toml
[adapters.local-llm]
executable = "/path/to/agent-bus-monitor/scripts/local-llm-peer"   # exec agentbus peer "$@"

[profiles.local]
client   = "local-llm"
provider = "local"
model    = "qwen2.5-coder:3b"             # optional: else the endpoint's model
[profiles.local.options]
endpoint = "local"                        # the [llm.<name>] table

[roles.local]
profile = "local"
tier    = "pop"
skills  = []
```

The role's prompt is `roles/local.md`, as for every role. An adapter's `executable` is resolved
against the manifest's own directory, so a manifest outside this checkout names the script by
its absolute path (or setup links it into `BIN_DIR` beside `agent-launch`; a choice for step 1).
The shipped manifest gets nothing; `examples/roles.local-llm.toml` holds this, and the test
copies it into a throwaway project.

## Launch and identity

- `agent-launch local <project>` resolves the profile, binds the pane (the launcher binds, then
  `exec`s: the pid never changes), and execs `scripts/local-llm-peer --spec-file …`, which execs
  `agentbus peer`. The identity is the one every agent gets: `AGENT_BUS_AGENT`, `_PROJECT`,
  `_ROLE`, `_PROFILE`, `_CLIENT=local-llm`, `_PROVIDER=local`, `_MODEL`, and a verified pid.
- Every status the peer declares is written in-process with its own pid and start time, through
  the same identity path as `agentbus status` (never a bare HSET), so it never weakens the bound
  identity.
- `agentbus up`: a template tab `agent-launch local <p>` works as any other; `checkClients` finds
  the adapter's executable. No trust gate (not Claude Code nor Codex). Resume: none. The peer is
  stateless between messages, so a relaunch is a fresh start, as for the other non-Claude
  clients.

## How it reads the bus

The peer is its own receiver, not a terminal session waiting to be woken:

1. At start, it **drains** (`Bus.DrainCmd`: PEL first, then unread, no floor), exactly what a
   woken agent runs, so nothing sent while it was down is lost.
2. It then **holds the receiver lease for its whole life** and receives through
   `WatchCmdDelivery` (the subscribe path, at-least-once, ACK after handling). The lease is
   renewed while a turn runs, too.

Why the lease must stay held during a turn: the dispatcher leaves an agent with a receiver lease
alone (`Decide` rule 2). Without it, a message arriving during a slow CPU turn would be an
obligation for a pane herdr detects no agent in: never ready, deferred, then **escalated to
master after 10 minutes**. Held, the dispatcher never touches the peer's pane, and the peer
needs **no herdr signature**. This needs one exported way to hold the lease beyond one delivery
(today `holdReceiver` is internal); the lease's contract (token, renewal, loss cancels) is
unchanged.

This is not the forbidden wrapper loop: that rule is about a terminal client that a restarted
watcher never wakes. Here the receiving process *is* the agent.

## A turn

For each cmd entry addressed to it (directive, challenge, reply, request):

1. `status working "<first words>"`.
2. One fresh conversation: the role prompt (short: who it is, that it is read-only, how to
   answer), the entry (type, from, text), and its thread when it has a `ref` (read with the same
   code as `agentbus thread`, last 20 entries, capped at 8 KiB). No memory across messages, so
   the context never grows.
3. `POST {url}/chat/completions` with `tools` (OpenAI function calling; Ollama supports it,
   `llama-server` needs `--jinja`). The tool loop is bounded: at most 8 calls per turn, a
   per-turn wall clock (default 10 minutes, `options.turn_timeout`), `max_tokens` from the
   profile (default 1024).
4. The final text is posted as a **reply on the entry's thread** (ref = its thread), plus a
   one-line `report` (the preview), then `status idle`.
5. The pane shows a plain transcript (received, tool calls, answer, timings, the endpoint's
   `usage` counts). The peer reads nothing from the pane's stdin; Ctrl+C stops it.

### Tools, read-only

| Tool | Does | Limits |
|---|---|---|
| `read_file(path, offset?, limit?)` | Text of a file under the project's root | Path resolved and kept under the root (symlinks resolved, `..` refused); 64 KiB per call; refuses what the manifest's `Read(…)` denies match (the top-level `[tool_rules]` of the manifest the launch names; the shipped list covers `.env*`, `*.pem`, `*.key`, SSH keys, …) |
| `list_dir(path)` | Names under a directory of the root | 500 entries; same root rule |
| `grep(pattern, path?)` | Matching lines under the root | Go regexp; 200 matches, 1 MiB read per call; skips `.git`, binaries and denied files |
| `bus_thread(id)` | A thread's entries | Read-only, 20 entries |
| `bus_agents()` | Who is on the bus and their state | Read-only |

No shell, no write to any file, no network but the configured URL. Its only bus writes are its
own status, the reply on the thread it was asked on, and its report. **No verdict** in this
step: a model nobody has measured does not get to approve a review. A challenge gets a reply
with its opinion; the gate stays open for a real reviewer.

A directive that asks for a change gets a reply saying the peer is read-only and what it would
change. Text the model reads in the repository may try to instruct it; with these tools the
worst it can do is say something wrong on one thread.

## Quota, budget, tokens

- Provider `local` gets a **builtin `unlimited` reader** (`quota.Builtins`), so the person writes
  no `quota.toml` and the dispatcher never holds or pauses it. busmon shows it as unlimited; the
  provider gets its colour (`providerHues`; `TestEveryQuotaProviderHasItsColour` requires it).
- No cost, by rule. Tokens: the peer has no Claude session, so the U page shows `?` with its
  reason, as for any peer without a proved session. Counting the endpoint's `usage` into
  `{p}:tokens` is a later step, not this one.

## Health: `llm-endpoint`

One check in the dispatcher's health loop, run only when a registered agent has client
`local-llm`:

- For each endpoint those agents use: `GET {url}/models`, 2 s deadline, every minute.
- **ok**: it answers and lists the model. **warn**: it answers without the model (not pulled, or
  another name). **fail**: no answer, or an error status. **unknown**: no config readable, or no
  such agent.
- Remediation, static: start the server (`ollama serve`, `llama-server …`) or fix `[llm.<name>]`
  in `config.toml`. Sentinel restarts nothing here (not a project service); a fail is escalated
  like any other.

The peer checks the same thing itself before each turn: an endpoint down declares `blocked
"llm endpoint: <reason>"`, retries every 30 s, and goes back to `idle` when it answers. busmon
shows that without the health check; the check adds F5's history.

## Failure modes

| What | What the peer does |
|---|---|
| Endpoint down at start or mid-turn | `blocked` with the reason; the entry stays unacknowledged and is handled when the endpoint answers |
| Model not pulled / wrong name | `blocked "model <m> not served by <url>"`; health warns |
| Model without tool calling (the server refuses `tools`) | Retries the turn once without tools (answer from the thread alone) and says so in the reply |
| Malformed tool call (bad JSON, unknown tool) | The error goes back to the model as the tool's result; counts towards the 8 calls |
| Path outside the root, denied file | Refused as the tool's result, with the rule; nothing read |
| Turn longer than the wall clock | Stopped; reply "timed out after N min" with what was done; `idle` |
| Context too small (Ollama's default context is small, and its OpenAI endpoint does not take `num_ctx`) | The server truncates silently; the test measures it; the remedy is server-side (`OLLAMA_CONTEXT_LENGTH`, `llama-server -c`), stated in the example's comments |
| GPU out of memory | The server's error; `blocked` with it |
| Two peers under one name | The second cannot take the receiver lease, says so, and exits |
| Broker down | Retries with backoff, as `subscribe` does; nothing lost (the cmd group keeps its entries) |

## Test plan (master)

On this host, in a **throwaway project** (`llmtest`) and, for herdr, a throwaway session; never
the person's client configs, never `agent-bus-monitor`'s agents.

0. **Runtime, the person's choice** (installing one is a cost): `llama-server` from a release
   archive unpacked in a temporary directory (no root), or Ollama's user-mode archive with
   `OLLAMA_MODELS` in a temporary directory. Model: Qwen2.5-Coder-3B-Instruct, 4-bit, to fit the
   4 GB GPU. Then `curl -s 127.0.0.1:<port>/v1/models` lists it.
1. `config.toml` gets `[llm.local]`; `agentbus peer --check local` prints the endpoint's answer
   and exits 0.
2. `examples/roles.local-llm.toml` copied into the throwaway project; `agent-launch local
   llmtest` in a pane. **Expect:** `agentbus agents` shows `local` verified (pid), client
   `local-llm`, provider `local`; busmon shows it idle and the provider unlimited.
3. `agentbus cmd local "In one paragraph: what does bus/board.go hold?"`. **Expect:** `working`,
   then a reply on that thread and `idle`; the pane shows the tool calls; the dispatcher's log
   shows rule 2 for `local` and never a prompt.
4. A message sent during a turn waits and is handled next; no escalation after 10 minutes.
5. `agentbus cmd local "Fix the typo in README.md"`. **Expect:** a reply saying it is
   read-only; `git status` clean.
6. Ask it to read `../` or `.env`. **Expect:** refused in the tool result; nothing read.
7. Stop the server. **Expect:** `blocked "llm endpoint: …"` within one turn; F5 `llm-endpoint`
   fail within a minute. Start it: `idle` again, health ok.
8. Kill the peer, send a message, relaunch it. **Expect:** the drain handles that message.
9. Record per turn: prompt and completion tokens, seconds, tokens per second, for the 3B on GPU
   and, if wanted, a 7B split. Write them in the journal; they decide whether a reviewer step is
   worth it.

Passes when 2–8 hold. Clean-up: kill the server, remove the temporary directory, `busmon
--delete llmtest`.

## Steps, once approved

1. `agentbus peer` (`peer/` package: config reader, client, tools, loop) with a fake endpoint in
   tests (`httptest`: tools, no tools, slow, down, bad JSON); the exported receiver hold; the
   adapter script and example manifest; provider `local` unlimited with its colour.
2. The `llm-endpoint` health check.
3. Later, only after the test's numbers: a verdict for a peer the person trusts, the endpoint's
   `usage` into `{p}:tokens`, and Codex (B) documented for larger machines.

## Not in scope

Writes to the repository, a shell tool, secrets or hosted endpoints, more than one local peer per
project, choosing or installing a runtime or model for the person, and any claim about speed or
quality before step 9's numbers.
