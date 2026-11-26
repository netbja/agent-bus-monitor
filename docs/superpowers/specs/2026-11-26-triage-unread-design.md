# Triage and the unread reports: mark all read (amendment to triage-dedupe)

Status: spec r2, for review (subject `triage-dedupe-amend`; r2, reviewer-codex: the guard reads a new `Held` list that `foldLines` fills for every fold, not `IDs`, and no build starts before the person's Q1). Not built. Base: origin/dev `f53e4fca`.
Author: architect, from master's directive (cmd `1795731923149-0`, task `triage-dedupe-amend`),
relayed by claude-ao as the person's wish and **not verified**: §7's ask confirms it before any
build. Amends [triage-dedupe](2026-11-21-triage-dedupe-design.md) (built, #755). No new dependency;
busmon only. No bus change.

## 1. The problem

Since triage-dedupe, triage holds actions only: a report without `--needs` goes to Activity and is
never a triage item (triage-dedupe §3). It keeps its unread mark there (`markUnread`,
`cmd/busmon/unread.go:13-19`). Today the only way to clear those marks is to open each line:
`u` jumps to the next unread (`cmd/busmon/keys.go:263`), Enter opens it. On a busy day master
sends dozens of status reports, so the unread count grows and stops meaning anything. Triage's
summary does not say they exist.

## 2. What busmon already has

- **Read marks are local.** The person's opened entries are a set of stream ids in busmon's cache
  file (`Read`, `cmd/busmon/localcache.go:17`), loaded into `unreadState.read`
  (`cmd/busmon/tea_model.go:276-283`). Nothing below the last visit (`floor`) is marked. No bus key
  holds them, and each busmon has its own.
- **Triage items do not name every line they hold.** `forYou()` (`cmd/busmon/tea_foryou.go:34`)
  builds the items, and `foldLines` (`:95`) folds Activity lines into them. An item's `IDs` holds
  only the lines that are their own row (`:144-170`: an unread line for the person, a
  `--needs` report item). Three folds record no id and only set `Unread`: an ask's own line
  (`:109-111`), a waiting agent's newest report (`:120-125`), and a `--needs` line naming exactly
  one live target (`:148-153`, a merge item, say). (r2, reviewer-codex: r1's guard read `IDs`
  only, and missed all three.)
- **Keys.** In Activity, `u` is the next unread and `f` the filter (`keys.go:262-263`). `N`, `m`
  and `z` are bound nowhere in busmon (`keys.go`, checked 2026-11-26). Ideas' undo is `u` on the
  Ideas page only (`keys.go:388`, `ideas_actions.go:154`): one change, once.

## 3. The triage summary's last line

The summary (triage-dedupe §4) gains one line under the items, when there is at least one
unread Activity line that is **not** a triage item:

```
  12 unread in Activity, none needs you  ->  u
```

`u` on the summary ends triage and selects the first such line in Activity. That is Activity's
own `u`, so `u` again walks them. The count uses the same rule as §4's guard: unread marks
minus the lines any item holds. With none, the line is absent. The summary's other lines are
unchanged.

## 4. Marking read in Activity

1. **`N`: mark all read.** It asks first: `mark 12 lines read? y/n` (no other key while it
   asks, as `x` on a target's page). On `y` it marks read every line that was unread **at the
   moment `N` was pressed** and is not held by a triage item (the guard below). A line that
   arrives while it asks stays unread. The count in the question is that set's size.
2. **`m`: toggle one line.** On the selected Activity line, read becomes unread and unread
   becomes read. Marking a line unread removes its id from `Read`. A line below the floor is
   never marked: `m` on it says so and does nothing.
3. **`z`: undo, once.** After `N` or `m`, `z` puts back the marks of that last act, from this
   busmon, as Ideas' `u` does. A second `z` says there is nothing to undo. The flash after `N`
   names it: `12 marked read · z undo`.
4. **On an agent's page: `N` marks that agent's lines read** (sender or author, the same match
   as the filter's `@agent`), with the same question, guard and undo.
5. **The filter: `f` then `unread`.** A token `is:unread` in the filter expression
   (`cmd/busmon/filter.go:20`) keeps only lines with `markUnread`. It is ANDed with the others,
   as every token is: `@master is:unread`. A word `unread` stays a word.

**The guard: marking all never clears a triage item.** `foldLines` records every line it folds
into an item in a new field, `attentionItem.Held []string` (stream ids): the item's own `IDs`,
plus each line folded at `:109-111` (the ask's line), `:120-125` (the waiting agent's report) and
`:148-153` (a `--needs` line naming one target). A report **without** `--needs` that only
attaches to the items it names (`:130-136`) is not held: it is exactly what §1 is about, and `N`
marks it. The set `N` marks excludes every id in any item's `Held` at the press. Merges and
gates have no Activity line of their own; a `--needs` report folded into them is in their
`Held`. Those clear only as triage-dedupe §3 says: by opening, by
answering, or by the item's own end. `m` on such a line is refused with
`this line is a triage item: open it from T`. A line that becomes an item later keeps its read
mark, which `forYou()` already ignores for `--needs answer` (`tea_foryou.go:139`). A `--needs
read` line marked read before it became an item is not possible: the guard reads the items at
the press.

**Where it is recorded.** Read marks are this busmon's local state (§2). Marking all read writes
the cache file, as opening a line does, and writes nothing on the bus. §7 Q3 asks whether the
person wants a line in the audit stream too.

## 5. The optional "To read" section

A person-only setting, `display.triage_to_read` (bool, default off, group Display, applies live).
When on, the triage walk ends with one more page before the summary: the unread lines that are
not items (§3's set), one line each, as on the summary. Enter opens one, which reads it. `N`
there is §4's mark-all with its question. Esc returns to the summary. Off, nothing changes.

Settings conflict on the registry's shared lists today (merge-conflicts measure, 2026-11-26). If
hot-files step 2 is not merged when this is built, this step's key is a settings PR, so it runs
alone.

## 6. Build: steps in series

No step starts before the person answers §7 Q1 yes (the wish came relayed and unverified).

1. **Activity marks:** `Held` in `foldLines`, `N`, `m`, `z`, the guard, the agent-page `N`, `is:unread`, help and the
   key map, the palette entries (`mark all read`, `mark read`). Tests: `N` marks the snapshot
   only (a line arriving during the question stays unread). `N` and `m` skip each of the four
   held kinds, one test each: a `--needs read` report item (`IDs`), an ask's own line, a waiting
   agent's newest report, a `--needs answer` report naming one merge item. `N` marks a report
   without `--needs` that names a live merge item (attached, not held). `m` toggles
   and refuses an item line. `z` restores exactly the last act and then has nothing. `is:unread`
   with `@agent`. Floor lines untouched. Mutants: drop the guard; build the guard from `IDs` instead of `Held` (the ask, wait and folded `--needs` tests fail); mark lines arriving after the
   press; let `z` undo twice.
2. **Triage summary line** (§3). Tests: the count equals §4's set; absent at zero; `u` lands on
   the first such line.
3. **"To read" section** (§5), behind the setting. Tests: off changes nothing (the existing
   triage goldens unchanged); on shows the page; `N` there is guarded.

Goldens: the Activity help and the triage summary goldens are regenerated with `-update` in the
step that changes them.

## 7. Questions for the person (ask `1795732025381-0`, in French)

1. **Confirm the wish.** Mark-all-read in Activity, guarded so it never clears what triage holds,
   plus the summary line "N unread in Activity". Recommended: yes, as specified.
2. **The "To read" section at the end of triage:** off by default, or not built? Recommended:
   built, off by default.
3. **Recorded where?** Read marks are local to each busmon. Recommended: no bus write, the undo
   (`z`) is the safety net. Alternative: one audit line per mark-all (`action=read-all`, count,
   by the person).
4. **Keys.** `N` mark all, `m` one line, `z` undo, `is:unread` in the filter. `u` is already
   Activity's "next unread", so it cannot be the undo. Recommended: these.
