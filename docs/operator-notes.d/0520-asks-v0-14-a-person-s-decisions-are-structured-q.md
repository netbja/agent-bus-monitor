- **Asks (v0.14): a person's decisions are structured questions, not report text.** `bus/asks.go`:
  `{p}:asks`, one flat hash (`<id>` definition, `<id>|q|s|a|<qid>|r|c|e`, `open:<agent>`), every write
  one Lua script (`askCreate`, `askAnswer`, `askWithdraw`); the id is the stream id of the `ask` cmd
  (`target=people`, `wake=no`, no `ref`; `people` is never a cmd target or consumer group). First
  answer wins; an answer to a replaced ask follows `replaced_by` only for an unchanged question; one
  open ask per agent; each answer is a `reply` (ref = ask id, `human=yes`) that wakes the agent. Client
  JSON never carries answers. v0.15.1: `ask <agent> --withdraw <id> --question <qid> --note …` closes one
  question (`AskAnswer.Withdrawn`), the ask ends with the last one; a directive typed to an agent with an
  open ask is held once with a hint (Enter sends, Tab opens its panel prefilled). The answer window
  (ask-replaced-answer) follows `replaced_by` as the bus does, for a question carried unchanged (`followAsk`,
  `bus.SameQuestion`), its draft too (`answerDraft`); a question changed, dropped or closed keeps the text, says so,
  and a second send makes it a message to the asking agent (`answerToMessage`), as does closing it (Esc keeps it as a
  message draft to that agent); a message draft is never overwritten, a second one held after it (`heldDrafts`, each
  opening once the one before is sent or emptied); the master skill forbids replacing an
  ask the person may be answering. busmon: the reader's ask page (`tea_reader.go`, `ModeShellAsk`, `!`/`w`/F4; `w` with no ask open
  opens the compose window to master, `ActAskOrWrite`, busmon-keys; Enter on a For you ask or in the agent's page; `askpanel.go`'s bodies), counts and fade follow open
  questions (`waitingQuestions`), bell per ask id; `markdown.go` renders long texts (`renderMarkdown`,
  overlay bodies only; rows stay on `Preview`).
