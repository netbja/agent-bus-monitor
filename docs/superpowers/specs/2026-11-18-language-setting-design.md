# The person's language: master talks to the person in it, the bus stays English

Status: spec, r7, answered; built on `feat/person-language` (subject `language-setting`). Base: origin/dev
`77eab8f2`.

r5:
- reviewer-codex on r4: master never quotes the person's language to another agent;
- foureyes on r4: the `Invalid` text, the busmon keys `i`/`I`, and a docs guard.

r6 (reviewer-codex on r5): the idea note links the spec without a revision label.

r7, the person's answers to §7 (relayed by master, 2026-11-20): Q1, a peer answering the person
directly stays English; Q2, any valid ISO 639-1 code, and an invalid value falls back to `en` and
says so (§2).

r4 is a rewrite. The person reframed idea 17 on 2026-11-20 (bus thread `1795188144565-0`,
relayed by master as cmd `1795188170169-0`): "Idea 17 was never about translating busmon." r1 to
r3 specified a busmon translation: an i18n catalogue, `T()` and `display.language`. That is
dropped, together with r3's §3 to §6. r1 to r3 were by coder (task `language-setting-spec`). r4
is by architect.

## 1. The need

The person wants to talk with master in their own language. Later this extends to the
multi-project "concierge" agent.

Nothing else changes language:
- busmon, the CLI, the logs and the project's files stay English;
- there is no i18n catalogue, no `T()` and no `display.language`.

## 2. The setting

- **The key:** `person.language`, the language the person reads and writes. Its value is a
  two-letter code (`en`, `fr`, …), and the default is `en`.
- **In the registry** (`config/keys.go`):
  - `Kind: String`;
  - env `AGENT_BUS_PERSON_LANGUAGE`, through `config.EnvName`;
  - `ReadBy: "master's role skill (agentbus config)"`;
  - `Invalid: "master writes English"`. `config_test.go` requires an `Invalid` text on every row
    whose env rule can refuse a value;
  - in the Identity group, beside `identity.as` (`config/group.go`), so busmon's settings page
    (Shift+C) shows and edits it with no new code.
- **Validation:** a value that is not an ISO 639-1 code in lower case is refused by the env rule
  (`envInvalid`) and the file check (`FailClosed`), with the registry's usual settings warning.
  The effective value is then `en` (answer to §7 Q2).
- **No code reads it.** No Go code changes behaviour on it. busmon, the dispatcher and the CLI
  never translate anything.

## 3. The rule, in master's role skill

- **Reading it.** On boot, and after a `/clear` or a reload, master runs `agentbus config` and
  reads the `person.language` row. `agentbus config` prints each setting's effective value and
  its source. A change made while master runs reaches it at its next boot.
- **To the person: in `person.language`.** This covers:
  - every `agentbus report master --to <person>`;
  - the question text of an `agentbus ask`;
  - master's replies in its own pane.
- **To every other agent: English.** This covers directives, requests, replies, challenges,
  verdicts and status messages.
- **Relaying the person's words.** When master passes on what the person said or decided, it
  writes it in English only: a summary in its own words, or a translation of the exact words.
  It never quotes the person's language to another agent.
  - The thread's root entry keeps the person's original. An agent that wants the exact words reads
    the thread, and master's English is what it acts on.
- **The person writes to master in any language.** Master answers in `person.language`, whatever
  language the message came in.
- **The ask's choice labels stay English.** Each answer comes back to master as the label, and a
  label can end up in a directive to another agent. Only the question text follows
  `person.language`.
- **Unknown or `en`:** master writes English to everyone, as today.

## 4. The bus rule and its exception

- **The rule:** everything on the bus is English. One language serves every model, and an agent
  never has to guess.
  - It holds today, but no file says it. It is written in the peer skill and in master's skill
    (§5).
- **The exception:** a message from the person, or addressed to the person, may be in
  `person.language`. That covers:
  - what the person types in busmon (the input line `i`, the compose window `I`, an ask's free
    answer);
  - master's reports and asks to the person;
  - what the notification targets carry to the person (ntfy, Discord). Those carry reports
    addressed to the person. They never translate a text, and their own words (the dispatcher's
    notices) stay English.
- **Peers keep English,** including a peer answering a human message sent to several agents with
  `@all` (question 1).

## 5. Files that change

| File | Change |
|---|---|
| `config/keys.go` | the `person.language` row (§2) |
| `config/group.go` | `"person.language": GroupIdentity` |
| `skills/agent-bus-master/SKILL.md` | a section "The person's language" after "Write reports people can read": the reading (§3), the rule (§3) and the bus rule with its exception (§4). Also one line in "When you need the human's decision: `agentbus ask`": the question in `person.language`, the choice labels in English. |
| `skills/agent-bus/SKILL.md` | a short section "Language on the bus" after "Write reports people can read": English to every agent; a message from or to the person may be in their language; relay nothing from the person to another agent in that language. |
| `roles/master.md` | boot step 4 also reads `person.language` (`agentbus config`) |
| `docs/operator-notes.d/person-language.md` | a new fragment, one bullet: the setting, who reads it, and that nothing is translated |
| `docs/memory/open-questions/2026-11-17-language-setting.md` | its finding is replaced by the person's decision and a link to this spec; title and `IDEAS.md` line to match |

- **Not changed:** busmon's code, the CLI, the dispatcher, `notify/`, the goldens, every other
  role and skill.
- **The concierge** has no role file yet. When it gets one, its skill takes §3's rule, reading
  the same key.
- **A role's text in another project** comes from the same skills, so it follows the next
  `setup` (skills install).

## 6. Tests

- **Registry tests:**
  - the row exists with its group and `ReadBy`;
  - `fr` and `en` are accepted;
  - `french`, `FR` and `f` are refused;
  - an unset key reads `en`.
  These are the same shape as `config/follow_test.go`.
- **No behaviour test:** no Go code reads the value.
- **A docs guard:** a test that master's skill names `person.language`, which pins the one reader
  `ReadBy` claims.
- **Docs:** `go run ./cmd/agentbus mem check` and the scrub test, as for every docs change.
- **Live check, once built:** the person sets `person.language = fr`, then relaunches master.
  - master answers a busmon message in French;
  - its next directive to coder, given on that message, is English;
  - `agentbus thread` shows both.

## 7. Questions for the person (answered 2026-11-20)

Answers: 1, English; 2, any valid ISO 639-1 code, an invalid value falling back to `en`, said.

1. **A peer answering you directly** (an `@all` message, or a reply to your busmon message): should
   it stay English, as recommended, or also use `person.language`?
2. **Free text or a list:** should any two-letter code be accepted, or only `en` and `fr` for now?
   Recommended: any code. The rule is in the skill, and the models write most languages.

## 8. Steps

- **One PR, from origin/dev:** the registry row, the two skills, the role file, the operator
  note and the idea note.
- **After it merges,** master's next boot reads the key. Agents that are running keep the skills
  they loaded, so master tells them with a wake=no directive (the "Skill updates" section of
  master's skill).
