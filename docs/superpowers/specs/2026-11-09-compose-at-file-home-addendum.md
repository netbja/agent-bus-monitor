# Addendum: `@~/` offers files under the home directory (compose-at-file)

Status: draft for review (subject `compose-at-file-home`). It extends
[the compose-at-file spec](2026-11-08-compose-at-file-design.md), which lists the project's root
only and refuses a link whose target lies outside it. Nothing here is built before both reviewers
approve it. Part 6 holds two questions for the person.

## 1. The ask

The person, 2026-11-09 (through master): besides the project's files, `@` should reach a path
relative to the home directory, `~/…`. Typical uses are a document in `~/Documents`, a log in
`~/Downloads`, or a file in another checkout under `~/Projects`.

The home directory is not the project:

- **It holds the person's credentials.** These include `~/.ssh`, `~/.aws`, `~/.gnupg`,
  `~/.config/gh/hosts.yml`, `~/.netrc`, `~/.git-credentials`, `~/.docker/config.json`,
  `~/.kube/config`, the clients' own tokens (`~/.claude/.credentials.json`,
  `~/.codex/auth.json`) and agent-bus's `~/.config/agentbus`. This machine has 98 hidden entries
  at the top of home.
- **It has no ignore rules** and can be very large (a Go module cache, venvs, backups).
- **Its files are not the team's.** A path into home names a file only the person's account can
  read, and agents on another host cannot read it at all.

## 2. When `~` applies

- The query starts with `~/` (`@~/Doc`), or is exactly `~` (`@~` lists the top of home). Any
  other query keeps the project's listing, as before.
- `~user/` (another user's home) is not supported: the popup says `only your own home (~/)`.
- No home directory (an empty or relative `$HOME`, or one that does not exist): the popup says
  `no home directory` and offers nothing. The rule is the same as `peer.NewDenyMatcher`, which
  fails closed on a home that is not absolute.

## 3. What it lists: one directory at a time

Home is never walked as a tree. The popup lists **the one directory the query is in**, the way a
shell completes a path:

- `@~/` and `@~/Doc` list the entries of `~`;
- `@~/Documents/` and `@~/Documents/rep` list the entries of `~/Documents`;
- the part after the last `/` is matched against those names, with the spec's ranking (§4.2), on
  the names alone.

One directory read is bounded by the spec's limits: 50 000 entries and 2 s, read in a `tea.Cmd`,
with `reading…` shown until it answers. No depth limit is needed. `tab` on a directory descends
(the query becomes `~/Documents/`), and `enter` inserts it, as in the project (§8 Q5).

**What is never offered:**

1. **Hidden entries directly under home** (a name starting with `.`, at the top of `~`): never
   listed, never descended into, and an exact query for one gets `hidden in your home: type it
   yourself`. That one rule removes every credential store in part 1, and any the list does not
   name. It is the recommendation in part 6, Q1. Hidden entries **below** the top
   (`~/Projects/x/.github/`) are listed, and pass through the filters below.
2. **`secretpath`** on the path relative to home, which also catches `.ssh`, `.aws`, `.gnupg` and
   `.git` deeper down, and the key and environment file families.
3. **roles.toml's shared `Read` denies**, matched against the absolute path. `~/` patterns are
   resolved by `peer.NewDenyMatcher` with this home, and its resolved form under a symlinked home.
4. **Unusual names** (a backtick, a control character), as in the project.
5. **The skip list** (`node_modules`, `.venv`, `vendor`): not listed inside home either.

**Links** follow the project's rule, measured against home instead of the root. A link to a
directory is never listed. A link to a file is offered only if its resolved target is a regular
file **inside home** that passes 1–4. The target's path is checked too, so `~/notes ->
~/.ssh/id_ed25519` is refused by rule 1 on the target. A target in the project's root is inside
home when the root is under home, the usual case.

**No ignore rules** are read under home: it is not a repository. A directory under home that
holds a `.git` entry (another checkout) is listed as a plain directory, and the person descends
into it one level at a time like any other.

## 4. What is inserted

Following the person's answer to §8 Q1 (absolute paths), and part 6, Q2:

- **The text gets the absolute path** (`/home/<user>/Documents/report.md`), built with
  `filepath.Join(home, filepath.FromSlash(rel))`. A recipient reads it with any tool, and
  Claude Code's `Read` needs an absolute path. The popup's rows show `~/Documents/report.md`.
- `compose.at_path = relative` (the project's relative form) does not apply to home. A home path
  is always absolute: relative to the project's root it would read `../../Documents/…`.
- Quoting, the trailing space, one undo step and the cap are the spec's §5.

## 5. What does not change

- The project's listing, the worktree descent and every rule of the spec.
- Nothing is read but names: no file's content, as before.
- The bus still carries text only. An agent that wants the file reads it with its own tools, under
  its own denies (which deny much of home already).

## 6. Questions for the person

1. **Hidden entries at the top of home.** Never offered (recommended), or offered with a list of
   exceptions you name (for example `~/.config/someapp/`)? Each exception would need its own
   filter review.
2. **The inserted form.** The absolute path, with `~/…` shown only in the popup (recommended), or
   `~/…` in the text as typed? `~/…` is shorter to read, but an agent must expand it, and an agent
   on another host has a different home.

## 7. Tests (added to the spec's §9)

- `@~/` lists the top of a fake home (`HOME` set to a temp directory): plain files and
  directories; **no** `.ssh/`, `.config/`, `.netrc`, `.env`; an exact `@~/.ssh/config` says
  `hidden in your home` and inserts nothing.
- One level only: `@~/Documents/` lists `Documents`' entries and nothing deeper. A directory of
  60 000 entries says `list incomplete`.
- Deeper hidden entries pass the filters: `~/p/x/.github/ci.yml` is offered, while
  `~/p/x/.git/config` and `~/p/x/.env` are not.
- Links: `~/notes -> ~/.ssh/id_ed25519` is refused, `~/out -> /etc/passwd` is refused (outside
  home), `~/a.md -> ~/Documents/a.md` is offered as `~/a.md`, and a directory link is not listed.
- A shared `Read(~/private/**)` deny hides `~/private/x`, under a symlinked home too.
- No home (`HOME` empty, relative or missing): `no home directory`, nothing offered.
- `~user/`: refused with its message.
- The insert: the absolute path, never `~`; `compose.at_path = relative` still inserts the
  absolute path for a home file.
- No program is run (the spec's §9 hook covers this listing too).

## 8. Where it lands

`filepick` gets a second entry point, `ListDir(ctx, home, rel, opts)`: one directory, with the
filters above, the same `Entry` and `Refused`. The popup chooses it when the query starts with
`~`. It is built in step 2 (the trigger and the insert), after this addendum is approved. Step 1
(the project's walk) is unchanged.
