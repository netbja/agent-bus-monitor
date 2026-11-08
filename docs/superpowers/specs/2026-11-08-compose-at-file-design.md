# @ in the compose window inserts a file's path (compose-at-file)

Status: spec for review (subject `compose-at-file-spec`), v0.29 chantier 5. No code on this branch.
Base `origin/main`. coder is rewriting parts of `cmd/busmon`. This spec names functions and seams,
not line numbers.

## 1. The ask, and what exists

The person wants Claude Code's `@path` in busmon's compose window: type `@`, pick a file, and get
its **path** in the text. Never its content.

What the code has today:

- **The compose window** (`tea_compose.go`, `composeState`) has two fields: `to` (the recipient)
  and `area` (the text, a `textArea` with undo, selection and a rune cap of `bus.CmdMaxRunes`).
  It writes a message or a free answer to a question (`composeAnswer`).
- **`@` already means a recipient,** in two places:
  - in the `to` field, where `toCompletions` lists `@all` and the agents;
  - at the **start of the one-line input** (`i`), where `splitRecipient` reads `@name`.

  In the window's **text** (`area`), `@` means nothing today. The new popup lives there, and only
  there, so the two meanings never meet.
- **`^V`** already pastes a clipboard image as a **path**: the image is saved under the cache's
  `agentbus/attachments`, and nothing is uploaded. The new feature follows the same rule: the
  text carries a path.
- **Secret-looking paths:** `health.IsSecretLikePath` matches `.env*` (not `.env.example`),
  `*.pem`, `*.key`, `id_rsa*`, `id_ed25519*`, `*keypair*.json` and `wallet*.json`. These are the
  same families as the `Read(…)` denies of `roles.toml`'s shared `[tool_rules]`.
- **The project's root:** the template's `working_dir` (`projlist`, for example
  `/data/projects/agent-bus-monitor`).
- **`peer.DenyMatcher`** compiles `roles.toml`'s `Read(…)` denies with Claude Code's glob
  meaning (`**`, a pattern without a slash at any depth, case-insensitive).

Measured on this repository (2026-11-07), for scale only (busmon will not run git, §2.1): git
lists 2 720 entries in 0.04 s, and 1 365 of them are `.worktrees/<name>/` nested worktrees, one
entry each.

## 2. Where it searches

### 2.1 No git process: a walk in Go (foureyes)

The first version ran `git ls-files`. It is **rejected**. git reads the repository's config, and
that config can make git run a command: `core.fsmonitor` is consulted by the untracked scan. Every
agent worktree shares the main `.git/config`, so an agent could write `git config core.fsmonitor
<cmd>` from its worktree, and the person's busmon would run `<cmd>` on the next `@`. That is
repository-controlled code run outside every agent's rules, the class CLAUDE.md forbids for the
dispatcher. Hardening the call (`-c core.fsmonitor=false`, `GIT_CONFIG_NOSYSTEM=1`, …) would
leave busmon chasing every config key that can execute something.

So **busmon runs no program to list files.** It walks the project's root itself, in Go:

- **Ignore rules** come from the `.gitignore` files met on the way and from `.git/info/exclude`.
  They are read as **text** by a small matcher. It covers patterns, `!` negation, a trailing `/`
  for directories only, a leading `/` to anchor, and `**`; a pattern without a slash matches at
  any depth. Reading a file never runs anything. The global excludes file (`core.excludesFile`)
  would need git's config, so it is **not** read. That is a known, harmless difference: a few
  more files may show.
- **Always skipped:** `.git` and every directory that holds a `.git` entry (file or directory),
  which means a nested repository or a worktree (§2.2). Also skipped: `node_modules`, `.venv` and
  `vendor`, because they are too big to be useful.
- **Symlinks** (foureyes, reviewer-codex). A link's name proves nothing: `docs/credentials` can
  point to `~/.aws/credentials` or to the project's `.env`. So:
  - a **symlinked directory** is neither descended into nor listed (no loops, no escape);
  - a **symlinked file** is offered only if its target, resolved with `filepath.EvalSymlinks`,
    is a regular file **inside the root** and passes **both** filters of §3 (the manifest's
    denies and `secretpath`), checked against the target's path as well as the link's. Any other
    link, a dangling one included, is not offered;
  - the inserted path is the link's own path, and the check is the target's.
- **Bounds:** depth 12, 50 000 entries, and a 2 s deadline, in a `tea.Cmd`, never on the update
  path. Until it answers, the popup shows `reading the file list…`. On a cap or the deadline it
  says `list incomplete (<why>)` and works on what it got. The list is read once per window
  opening and dropped when the window closes.
- The root is the template's `working_dir`, made absolute and cleaned.

### 2.2 Worktrees, on demand

A directory with a `.git` entry under the root (`.worktrees/<name>/`) is listed as **one** entry
(the way git itself shows them: 1 365 such entries on this repository on 2026-11-07). When the
query starts with that directory (`.worktrees/agy-probe/…`), the same walk runs inside it with
the same rules, and the paths are prefixed. That avoids listing a full copy of the tree for each
worktree, and a coder's branch is still reachable.

### 2.3 When there is nothing to list

- **No checkout on this machine** (busmon against a remote broker, or a template whose
  `working_dir` is missing): the popup shows only `no checkout of <project> on this machine`, and
  `@` stays a plain character.
- **Only paths inside the root are offered.** A query that starts with `/`, `~` or `..` gets no
  list and shows `outside the project: type the path yourself`. The person can always type any
  text; the popup only refuses to *offer* it.

## 3. What is never offered

Two filters, both applied before ranking:

1. **The manifest's own denies.** The shared `[tool_rules]` `Read(…)` patterns of `roles.toml`,
   compiled with `peer.DenyMatcher` against the root. What no agent may read is never offered
   to be pointed at.
2. A shared function, `secretpath.Is(path) (kind string, ok bool)`, moved from
   `health.IsSecretLikePath` so that health and busmon use one rule. It covers a machine with no
   `roles.toml`, and it adds two kinds of path:

- any path with a component `.ssh`, `.gnupg`, `.aws` or `.git`;
- the agent-bus files the manifest already denies: `notify.toml` and `config.toml` under an
  `agentbus` config directory.

A matching entry is dropped before ranking, and the popup never shows it. If the query exactly
names such a file, the footer says `<kind>: not offered` and inserts nothing. Typing the path by
hand stays possible, since busmon cannot police text. The path still never carries the content.

## 4. The popup

### 4.1 When it opens

- In the **text** field of either window kind (a message or an answer).
- When `@` is typed at the start of the text, or right after whitespace or one of `(` `[` `"`
  `'`. So `a@b.c` (an email) never opens it.
- The query is what follows the `@` up to the cursor, as long as it holds no whitespace. A space,
  moving the cursor out of the token, or `esc` closes the popup and leaves the typed text as it is.
- A paste never opens it. Only a typed `@` does.

### 4.2 Ranking

The match is case-insensitive. The order is:

1. the file name starts with the query;
2. a path segment starts with it;
3. the path contains it;
4. the query's runes appear in order in the path (a subsequence).

Ties go to the shorter path, then to alphabetical order. A query with a `/` matches the path
from its start (`cmd/bu` lists `cmd/busmon/…`). An empty query (just `@`) lists the files the
person picked most recently in this busmon session first, then the top-level entries.

### 4.3 Look and tiers

The popup is anchored under the cursor's row, inside the window, and flips above the row when
there is no room below:

| Tier | Rows | Width |
|---|---|---|
| wide ≥ 120 | 8 | the window's text width |
| medium 80–119 | 6 | the same |
| narrow < 80 (the window is full-width) | 4 | the full width |

- A path too wide for the row is cut **on the left** (`…/busmon/tea_compose.go`), using the
  existing `truncLeft`.
- The selected row is a pill in `RoleAccent`. Directories end with `/`, and choosing one descends
  into it (the query becomes `dir/`, and the popup stays open).
- The popup's own footer line replaces the window's while it is open:
  `↑↓ choose · tab/enter insert · esc keep text`, plus `N files` or the state (§2).
- **Mouse:** a click on a row inserts it, and the wheel scrolls the list. The window's existing
  click-to-place-cursor keeps working outside the popup.

### 4.4 Keys (keys.go, the one table)

The popup is a sub-state of `ModeCompose`. It gets its own table, `composeFileBindings`, which
the handler, the footer and the help all read (the pattern `settingsBindings` uses):

| Keys | Action |
|---|---|
| `↑` `↓` | move the selection |
| `pgup` `pgdn` | a popup page |
| `tab`, `enter` | insert the selected path (a directory: descend) |
| `esc` | close the popup only; the typed `@query` stays, and a second `esc` closes the window as before |
| any other key | goes to the text, and the query follows it |

While the popup is open, `enter` inserts and does not add a new line, and `tab` inserts and does
not switch fields. Once it is closed, both mean what they mean today. `keys_test.go`'s checks
(footer == table, no key advertised twice) cover the new table.

## 5. What is inserted

- **The `@query` is replaced by the path.** The `@` goes too: the text reads like a sentence
  with a path in it, and an `@word` in a message already suggests an agent.
- **The form of the path is §8 Q1.** The recommendation is the **absolute** path
  (`/data/projects/agent-bus-monitor/cmd/busmon/compose.go`). Agents work in different
  worktrees, so a relative path names a different file for each reader. The team already writes
  full paths for that reason. An absolute path names **the main checkout's copy** (foureyes),
  which can differ from an agent's branch in its worktree. A path picked under
  `.worktrees/<x>/` names that worktree's copy. A setting, `compose.at_path` (`absolute` | `relative`, registered
  in `config/keys.go`), lets the person choose relative paths. A relative path is relative to the
  project's root.
- **Quoting.** A path with a space, a tab or one of ``(`)[]{}<>"'`` is wrapped in backticks:
  `` `docs/my notes.md` ``. It still renders as code in busmon's Markdown and reads well in an
  agent's pane. A path that contains a backtick is not offered (§3's footer: `unusual name: type
  it yourself`).
- **One space follows** the path, unless the next rune is already whitespace or punctuation, so
  the person can type `@` again for a second file. Several files in one message are several
  `@`s.
- **The insert is one undo step** (`textArea.edit("insert", …)`), and `^Z` brings back the
  `@query`.
- **The cap.** `bus.CmdMaxRunes` (or the answer's 2 000) counts the path. A path that would
  overflow is not inserted, and the popup says `the message is full`. A path is never cut in two.

## 6. How the recipient sees it

As plain text in the message body. The bus carries text only: no attachment, no file, no
content. An agent that wants the file reads it with its own tools and its own permissions, so the
agent's `Read` denies still apply to it. Nothing in `bus/`, the dispatcher or `subscribe` changes.
In busmon's Activity and Requests panels and the reader, a backticked path renders as code, as
today.

## 7. How it composes with the rest

- **The `to` field and the input line** keep `@` as a recipient: `toCompletions` and
  `splitRecipient` are unchanged. A text the window opens with from the line (`^O`), which starts
  with `@name`, is still split into the recipient. The popup only listens to keys typed in the
  text field.
- **The home inbox (For you, merged) and triage** reach the window through the reader and the
  reply key: `tea_reader.go` and `reply_key.go` call `openAnswerCompose` and
  `openMessageCompose`, and triage walks items in the reader. The popup works there the same way,
  because it belongs to the text field and not to the window kind. The reader's keys (triage's
  `n`/`p` among them) are not read while the window is open, as today.
- **`^V` image paste** and the popup both insert a path, and the two are independent.
- **The draft** (`composeDraft`) keeps the inserted text like any other text. The popup's state
  is not saved, and a reopened window starts with it closed.
- **coder's rewrite of `cmd/busmon`.** The feature is a pure part plus a thin UI part, so it can
  land on either side of the rewrite:
  - the pure part: the listing, the ranking, the trigger and the insert;
  - the UI part: the popup in the window's View, and the key table.

## 8. Questions for the person

1. **Absolute or relative path by default?** The recommendation is absolute (§5), with
   `compose.at_path` to switch. Know this when choosing: an absolute path names the main
   checkout's copy, not the recipient agent's branch. A relative path is read in each agent's own
   worktree.
2. **Keep the `@` in the text** (`@cmd/busmon/compose.go`, as Claude Code does) or drop it (the
   recommendation, since an `@word` reads as an agent)?
3. **The one-line input (`i`)** too? It reads a leading `@` as the recipient. The popup could
   work there after the first word. The recommendation is the compose window only, in v1.
4. **Worktrees.** Only on demand (§2), or should the popup also offer the files of the worktree
   of the agent the message goes to?
5. **Directories** as a pick (insert `cmd/busmon/`), or only files (the recommendation: a
   directory descends, and `tab` on a directory followed by `esc` leaves `@cmd/busmon/` as typed
   text)?

## 9. Tests

The seam is pure functions where possible, then `Model` with a fake file lister, as the existing
compose tests use. No test runs git on the real repository.

- **The trigger:** `atToken(text, cursor) (query string, start int, ok bool)`: at the start, after a
  space or a `(`, not in `a@b`, closed by a space, cursor outside the token, a paste.
- **No program is run** (foureyes): a fake repository whose `.git/config` sets `core.fsmonitor`
  (and `core.hooksPath`) to a script that touches a file. After `@`, the file does not exist. A
  test hook on the process runner fails the test if busmon starts any process while listing.
- **The walk:** names with spaces; `.gitignore` at the root and in a subdirectory, negation,
  dir-only, anchored and `**` patterns; `.git/info/exclude`; a nested `.worktrees/x/` (one entry,
  then the descent on the prefix query); depth, count and deadline caps (`list incomplete`); no
  checkout gives the message.
- **Symlinks** (reviewer-codex's fixtures):
  - `docs/credentials` → a file outside the root: not offered;
  - `docs/notes` → the project's `.env`: not offered (an in-root secret target);
  - `docs/alias.md` → `README.md` inside the root: offered as `docs/alias.md`;
  - a dangling link: not offered;
  - a symlinked directory (a loop to the root, a link to `/etc`): neither listed nor walked.
- **The denies:** a `roles.toml` with a shared `Read(**/secrets/**)` hides `secrets/a.txt`.
- **Secrets:** every `secretpath` kind is absent from the popup, including an exact query for one.
  `health`'s existing secret-path tests pass against the moved function.
- **Ranking:** a table of queries → expected order (file-name prefix, segment prefix, substring,
  subsequence, ties).
- **The insert:**
  - absolute and relative forms;
  - backticks for a space;
  - a backtick in a name is not offered;
  - the trailing space rule;
  - one undo step restores `@query`;
  - the cap refuses an insert;
  - two files in one message.
- **Keys:** with the popup open, `enter` inserts and adds no new line, `tab` inserts and does not
  switch fields, and `esc` closes the popup and then the window. `@` in the `to` field still
  lists agents. A line opened with `@coder fix` still splits.
- **Goldens** at the existing sizes (60×20, 80×24, 140×40): the popup below, the popup flipped
  above, the narrow popup, `reading the file list…`, `outside the project`, `list incomplete`.
- **The answer window:** the popup works in `composeAnswer`. Triage's answer flow keeps its keys.

## 10. Steps (each its own subject for 4-eyes)

1. `secretpath` (moved from `health`, plus the extra kinds), the manifest-deny filter, the
   gitignore matcher, the walk (no process, no symlink followed, the worktree descent) and the
   ranking: pure, with tests. **Security review on master's call** (foureyes): the walk is the
   only new thing reading an agent-writable tree on the person's behalf.
2. The trigger, the insert, the `compose.at_path` setting and the key table: pure and table tests.
3. The popup in the window (View, mouse, tiers, goldens), after or alongside coder's rewrite.
4. Docs: README's compose keys, OPERATIONAL-GUIDE (and `.fr`), and the help text from the table.
