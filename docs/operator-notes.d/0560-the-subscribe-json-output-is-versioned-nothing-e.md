- **The `subscribe` JSON output is versioned; nothing else is.** Every `subscribe` event carries
  `"v"` (= `bus.ProtocolVersion`, currently 1). The rule: within a `v`, changes are **additive only**
  and consumers **must ignore unknown fields**; **bump `v` only on a breaking change** to the
  subscribe contract (a field removed/renamed/repurposed or a semantics change). A consumer that
  sees a higher `v` than it knows should **fail loud**, not best-effort — that is what turns a format
  cutover (the old sentinel→JSON break) into an explicit signal. `agentbus version` prints the
  constant. Stream entries and `agents`/`usage`/`verdicts` output are **not** versioned (they are
  additive-by-key via `ParseEntry`/`json.Unmarshal`).
