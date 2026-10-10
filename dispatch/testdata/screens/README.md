# Screen fixtures

Captured 2026-10-10 with `herdr pane read <pane> --source recent --lines 40`
(herdr 0.9.3, session `busmon`). Files for clients whose signature has `ANSI`
set (claude, codex) were read with `--ansi` and keep their escapes and `\r`.
Spec §10 records what each state means.

| File | Source |
|---|---|
| `claude-empty.txt` | REAL — fresh Claude Code 2.1.296 in a dedicated probe pane, dim `Try "…"` placeholder |
| `claude-idle.txt` | REAL — same pane after a turn, empty `❯` |
| `claude-typed.txt` | REAL — `zz-probe` typed (send-text, no Enter), then cleared with `ctrl+u` |
| `claude-busy.txt` | REAL — turn running (`✶ Kneading… (4s · ↓ 94 tokens)`), herdr `working` |
| `claude-modal.txt` | REAL — `AskUserQuestion` menu (`❯ 1. Red`), herdr `blocked`; dismissed with Escape |
| `codex-empty.txt` | REAL — w2:p1, idle, unfocused |
| `codex-typed.txt` | REAL — w2:p1 with `zz-probe` typed, then cleared with `ctrl+u` |
| `codex-scroll.txt` | SYNTHETIC — `codex-empty.txt` with its last footer line replaced by a transcript-scroll footer carrying `enter/esc latest` (spec §10 "Modal hazard"); not reproduced because it needs keystrokes in a peer's live session |
| `kimi-empty.txt` | REAL — w4:p1, idle, unfocused |
| `kimi-typed.txt` | REAL — w4:p1 with `zz-probe` typed, then cleared |
| `kimi-spinner.txt` | SYNTHETIC — `kimi-empty.txt` plus a `🌒 · Tip: …` line above the input box (Kimi 2.1 spinner form, spec §10 findings); reproducing it needs a prompt to the peer |
| `grok-empty.txt` | REAL — w3:p1, idle, unfocused |
| `grok-typed.txt` | REAL — w3:p1 with `zz-probe` typed, then cleared |
| `agy-empty.txt` | REAL — w5:p1, idle, unfocused |
| `agy-typed.txt` | REAL — w5:p1 with `zz-probe` typed, then cleared |
| `agy-approval.txt` | SYNTHETIC — top of `agy-empty.txt`, input box replaced by an approval menu with a `> 1. Yes` line and an `esc to dismiss` banner (spec §10 "Modal hazard"); reproducing it needs a prompt to the peer |
| `<client>-old-empty-typed.txt` (claude, codex, kimi, grok, agy) | DERIVED — last 12 lines of `<client>-empty.txt` (an OLD empty input box in scrollback) above the last 28 lines of `<client>-typed.txt` (the CURRENT box with `zz-probe` typed); must read not ready. Escapes and `\r` kept as in the sources |
| `<client>-old-typed-empty.txt` (same clients) | DERIVED — last 12 lines of `<client>-typed.txt` above the last 28 lines of `<client>-empty.txt`; must read ready (only the current box counts) |
| `old-prompt.txt` | DERIVED — `kimi-empty.txt` plus an earlier `[agentbus#abc123]` wake line in the transcript, for the nonce test |

`../herdr/*.json` are real replies (`agent get`, `pane get`, `pane process-info`,
not-found errors, `wait-output` timeout) except `prompt-ok.json` (trimmed real
shape) and `prompt-stalled.json` (SYNTHETIC: the error object shape with code
`agent_prompt_stalled`, seen in the Kimi probe but not saved then).
`agent-get-claude.json` is the real reply from the probe pane.
