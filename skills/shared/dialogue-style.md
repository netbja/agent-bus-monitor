# Dialogue style: how an agent writes to the person

These rules apply to everything an agent writes for the person to read: replies in its own pane,
reports `--to <person>`, and the question text of an ask. Master's skill and the concierge's skill
point here, so the rules are stated once (status-summary §5.1, concierge §4.5).

They are written as intents and hold in any language. They were inspired by `blader/humanizer`, an
agent skill under the MIT licence that removes signs of AI-generated writing (checked on 2026-11-27
with `gh api repos/blader/humanizer`). Nothing of it is copied here.

## Shape

- **The answer or the decision comes first**, in one or two sentences.
- Short bullets follow, only if they are needed. No headings in a short reply.
- No preamble, no staged opener, and no one-line closer.
- No filler and no chat wrappers ("Great question", "Hope this helps", "Let me know").

## Wording

- No "not X but Y" contrast used for emphasis.
- No reflexive groups of three, no dashes used as rhetoric, and no bold labels.
- Do not re-explain what the person already knows: what they said in this conversation, or what
  their memory holds.
- Vary the sentence length.
- **State uncertainty with its source and that source's time**: "the board said so at 09:12; not
  checked since".

## Language

- Write to the person in `person.language` (`agentbus config`, its `person.language` row),
  applying the same intents in that language. To every other agent, write English (the bus rule).

## Never rewritten

- Verbatim quotes, the person's typed text, ids and sources. No style rule touches them.
- There is no second pass: the rules shape the one reply. Nothing reruns it through a "humanizer".

## Where a report differs

- A report's first line stays its summary (the report rule): busmon shows only that line in a list.
- When a report gives state it read, its last line is the sources line (status-summary §5.3):
  `↳ ` followed by what was read and when, for example `↳ summary 22:31, board, #841`. busmon draws
  it dim and folds it.
