# Ideas waiting for the person

One note per idea, in [open-questions](open-questions/) (status proposed): the repository's record
of each idea, its context and a recommendation, for the person to decide. An idea is written as a
note, never as a plan task owned by the person (those flood For you). A decided idea becomes a task,
and its note is marked resolved or superseded.

busmon does not list these notes today. Its Work page's IDEAS section shows the broker's ideas
(`agentbus idea list`, `{p}:ideas`, read by `cmd/busmon/work_ideas.go`). The requests-plan redesign
(§9) planned IDEAS to list the open-question notes, and `memory.OpenQuestions` can read them, but
nothing calls it. Until that is wired, an idea the person should see in busmon is also added with
`agentbus idea add`, its note's path in the text.

From 2026-11-17:

- [The person's language: master talks to them in it, the bus stays English (decided, built on feat/person-language)](open-questions/2026-11-17-language-setting.md)
- [A services view with restart buttons and readable logs](open-questions/2026-11-17-services-view.md)
- [A right-click context menu in busmon](open-questions/2026-11-17-context-menu.md)
- [Agents' background shells and sub-agents visible in Agents](open-questions/2026-11-17-agent-background-work.md)
- [Per-provider quota reliability](open-questions/2026-11-17-quota-reliability.md)
- [README and operational guide rewrite](open-questions/2026-11-17-readme-guide-rewrite.md)
- [Migrating the person's other projects, one by one](open-questions/2026-11-17-project-migration.md)
- [Braille graphs in the Tokens screen](open-questions/2026-11-17-braille-graphs.md)
- [Updating from busmon, without setup.sh: the person's `agentbus update` variant (questions open)](open-questions/2026-11-17-busmon-update.md)
- [The Fable profile's permission mode](open-questions/2026-11-17-fable-permissions.md)
- [Holder G2: a PTY owner and a restartable core](open-questions/2026-11-17-holder-g2-split.md)

From 2026-11-19:

- [A CI loop to the branch's owner, newer-client detection, richer Claude state (from agent-orchestrator)](open-questions/2026-11-19-ci-loop-and-status-ideas.md)

From 2026-11-20:

- [A turn lost without its Stop hook (API error, Esc): one notice to master, no auto-wake](open-questions/2026-11-20-lost-turn-notice.md)
- [Merge a PR from busmon's merge popup, the exact command shown first (GitHub first)](open-questions/2026-11-20-busmon-merge-button.md)
