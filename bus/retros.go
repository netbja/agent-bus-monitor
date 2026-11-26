package bus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Retros (self-improving loop, step 2; spec
// docs/superpowers/specs/2026-11-24-self-improving-loop-design.md §4-§7):
// the {p}:retros hash, the working state of every filed retro.
//
//	<task>                 the retro's record (RetroRecord, JSON)
//	rej|<file>|<lesson>    a rejected proposal: its file and normalised
//	                       lesson, the value <task>#<n> (§6)
//
// A retro's ask is the retro author's (RetroSender), never master's: one is
// open at a time, the rest wait queued; the answers go to master.

// RetroSender is the author of every retro ask: a reserved name like
// SystemSender, never an agent's (agentbus ask refuses it, no role takes it).
// Its one ask slot is open:retro, so a retro never blocks master's asks.
const RetroSender = "retro"

// RetroAnswerTo is who a retro ask's answers go to, and wake: master runs
// route once the ask closes (§6).
const RetroAnswerTo = "master"

// RetroDocs is the role that applies a routed retro (the person's Q1 A).
const RetroDocs = "docs"

// RetroMaxRows: at most three proposals a retro (§4, the bloat guard).
const RetroMaxRows = 3

// A retro's states.
const (
	RetroQueued = "queued" // filed, its ask waiting for the retro slot
	RetroOpen   = "open"   // its ask is open, or answered and not routed yet
	RetroRouted = "routed" // retro-apply sent to docs, the outcomes recorded
	RetroClosed = "closed" // docs delivered it (retro close --pr)
)

// A proposal's outcome (§6): accepted only by RetroApproved.
const (
	OutcomeAccepted   = "accepted"
	OutcomeRejected   = "rejected"
	OutcomeLater      = "later"
	OutcomeUnanswered = "unanswered"
	// OutcomeUnverified: the person answered yes, but no reply entry proves
	// it any more (trimmed from the stream, not in the archive read): not
	// applied, and never rejected (foureyes on r1).
	OutcomeUnverified = "unverified"
)

// RetroArchive reads the history archive's reply entries to ask askID
// (ref = askID), as Events; the CLI reads the archive (bus does not).
type RetroArchive func(ctx context.Context, askID string) ([]Event, error)

// SetRetroArchive makes the retro checks read the archive too (spec §5: the
// reply "stays in the stream or in the history archive"); nil: the stream
// alone. A failed archive read is an error, never a refusal.
func (b *Bus) SetRetroArchive(f RetroArchive) { b.retroArchive = f }

// ErrRetroArchive marks a failed archive read.
var ErrRetroArchive = errors.New("retro: reading the archive")

// RetroDestinations are a row's destination column's values (§4 point 4).
var RetroDestinations = []string{"skill", "context", "new skill"}

// RetroBlobAbsent is a new skill's recorded blob: its file absent on the
// base at propose time.
const RetroBlobAbsent = "absent"

// RetrosKey is the {p}:retros hash.
func RetrosKey(project string) string { return project + ":retros" }

// RetroRow is one proposal: the table's columns, and the target file's git
// blob id read at propose time (RetroBlobAbsent for a new skill).
type RetroRow struct {
	N           int      `json:"n"`
	Lesson      string   `json:"lesson"`
	Evidence    []string `json:"evidence"`
	Destination string   `json:"destination"`
	File        string   `json:"file"`
	Change      string   `json:"change"`
	Blob        string   `json:"blob"`
}

// RetroOutcome is a proposal's outcome at route, with the entry id that
// decided it ("" for unanswered).
type RetroOutcome struct {
	N       int    `json:"n"`
	Outcome string `json:"outcome"`
	Entry   string `json:"entry,omitempty"`
}

// RetroRecord is a filed retro.
type RetroRecord struct {
	Task      string         `json:"task"`
	Owner     string         `json:"owner"`
	Root      string         `json:"root"`
	Rows      []RetroRow     `json:"rows"`
	Notes     []string       `json:"notes,omitempty"` // the propose's notes for the person (a file past retro.prune_lines)
	State     string         `json:"state"`
	AskID     string         `json:"ask_id,omitempty"`
	CreatedAt int64          `json:"created_at"` // Redis TIME, Unix ms
	Outcomes  []RetroOutcome `json:"outcomes,omitempty"`
	Apply     string         `json:"apply,omitempty"` // the retro-apply request's task
	PR        int            `json:"pr,omitempty"`
}

// NormLesson is a lesson as the rejected list compares it: lower case,
// whitespace collapsed (§6).
func NormLesson(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func rejectedField(file, lesson string) string { return "rej|" + file + "|" + NormLesson(lesson) }

// RetroApplyName is retro-apply-<task>, for docs; for a task too long for
// a valid name, its first 11 characters and 8 hex of its sha256.
func RetroApplyName(task string) string {
	const prefix = "retro-apply-"
	if name := prefix + task; len(name) <= 32 {
		return name
	}
	sum := sha256.Sum256([]byte(task))
	return prefix + task[:11] + "-" + hex.EncodeToString(sum[:4])
}

// ValidateRetro checks a record's own shape (§4): its names, the root
// cause, one to three rows numbered 1..n with every column filled, bus ids
// as evidence, a known destination. The checks that need git, the streams
// or the archive are the caller's (agentbus retro propose).
func ValidateRetro(r RetroRecord) error {
	if !ValidName(r.Task) || !ValidName(r.Owner) {
		return fmt.Errorf("retro: invalid task or owner")
	}
	if err := textField("root cause", r.Root, askMaxContext/4, true); err != nil {
		return fmt.Errorf("retro: %w", err)
	}
	if len(r.Rows) == 0 || len(r.Rows) > RetroMaxRows {
		return fmt.Errorf("retro: 1 to %d proposals, not %d", RetroMaxRows, len(r.Rows))
	}
	for i, row := range r.Rows {
		where := fmt.Sprintf("retro: row %d", i+1)
		if row.N != i+1 {
			return fmt.Errorf("%s is numbered %d: number them 1..n", where, row.N)
		}
		for _, f := range []struct {
			name, v string
			max     int
		}{{"lesson", row.Lesson, askMaxQuestion}, {"destination", row.Destination, 16}, {"file", row.File, 200}, {"change", row.Change, 600}, {"blob", row.Blob, 64}} {
			if err := textField(where+" "+f.name, f.v, f.max, true); err != nil {
				return err
			}
		}
		if strings.Contains(row.File, "|") {
			return fmt.Errorf("%s: a file name with | is not a destination", where)
		}
		if len(row.Evidence) == 0 {
			return fmt.Errorf("%s has no evidence: a bus id is required", where)
		}
		for _, id := range row.Evidence {
			if !streamIDPattern.MatchString(id) {
				return fmt.Errorf("%s: evidence %q is not a bus id", where, id)
			}
		}
		ok := false
		for _, d := range RetroDestinations {
			ok = ok || row.Destination == d
		}
		if !ok {
			return fmt.Errorf("%s: destination %q is not one of %s", where, row.Destination, strings.Join(RetroDestinations, ", "))
		}
	}
	return nil
}

// retroAskDef is the retro's ask (§4): from RetroSender, one question p<n>
// per proposal (the lesson; the root cause, file, change and evidence as
// its context), each answered yes, no or later. of is the retro's place in
// the queue ("retro 1 of N").
func retroAskDef(r RetroRecord, of int) AskDef {
	d := AskDef{From: RetroSender, Title: fmt.Sprintf("retro 1 of %d: %s", of, r.Task)}
	for _, row := range r.Rows {
		ctx := fmt.Sprintf("Root cause: %s\nFile: %s (%s)\nChange: %s\nEvidence: %s", r.Root, row.File, row.Destination, row.Change, strings.Join(row.Evidence, ", "))
		for _, n := range r.Notes {
			ctx += "\nNote: " + n
		}
		d.Questions = append(d.Questions, AskQuestion{ID: "p" + strconv.Itoa(row.N), Header: "Retro #" + strconv.Itoa(row.N), Question: row.Lesson, Context: ctx,
			Options: []AskOption{{Label: "yes", Description: "apply it"}, {Label: "no", Description: "reject it: never proposed again"}, {Label: "later", Description: "not now: a later retro may propose it again"}}})
	}
	return d
}

// retroStore files a retro: KEYS retros. ARGV task, record JSON, cap,
// day start ms, then each row's rejected field. Refused when the task has
// a retro, when cap retros were filed since the day's start, or when a row
// repeats a rejected proposal; nothing is written then.
var retroStore = redis.NewScript(`
if redis.call('HEXISTS',KEYS[1],ARGV[1])==1 then return redis.error_reply('RETRO_EXISTS') end
local cap,since=tonumber(ARGV[3]),tonumber(ARGV[4])
if cap>0 then
 local n=0
 local all=redis.call('HGETALL',KEYS[1])
 for i=1,#all,2 do
  if string.sub(all[i],1,4)~='rej|' then
   local ok,o=pcall(cjson.decode,all[i+1])
   if ok and type(o)=='table' and tonumber(o.created_at) and tonumber(o.created_at)>=since then n=n+1 end
  end
 end
 if n>=cap then return redis.error_reply('RETRO_CAP '..n) end
end
for i=5,#ARGV do
 if redis.call('HEXISTS',KEYS[1],ARGV[i])==1 then return redis.error_reply('RETRO_REJECTED '..(i-4)) end
end
redis.call('HSET',KEYS[1],ARGV[1],ARGV[2])
return 1`)

// retroCAS replaces a record only if it is still the one read: KEYS
// retros. ARGV task, old JSON, new JSON.
var retroCAS = redis.NewScript(`
if redis.call('HGET',KEYS[1],ARGV[1])~=ARGV[2] then return redis.error_reply('RETRO_CHANGED') end
redis.call('HSET',KEYS[1],ARGV[1],ARGV[3])
return 1`)

// RetroPropose files r (§4): checked (ValidateRetro, its ask), stored as
// queued with the Redis time, refused over cap retros since dayStart (0 =
// no cap), or when a row repeats a rejected proposal; then its ask is
// opened if the retro slot is free. It returns the record as stored.
func (b *Bus) RetroPropose(ctx context.Context, r RetroRecord, cap int, dayStart int64) (RetroRecord, error) {
	if err := ValidateRetro(r); err != nil {
		return RetroRecord{}, err
	}
	if err := ValidateAskDef(retroAskDef(r, 9)); err != nil {
		return RetroRecord{}, fmt.Errorf("retro: its ask: %w", err)
	}
	now, err := b.ServerTime(ctx)
	if err != nil {
		return RetroRecord{}, err
	}
	r.State, r.AskID, r.Outcomes, r.Apply, r.PR, r.CreatedAt = RetroQueued, "", nil, "", 0, now.UnixMilli()
	raw, err := json.Marshal(r)
	if err != nil {
		return RetroRecord{}, err
	}
	args := []any{r.Task, string(raw), cap, dayStart}
	for _, row := range r.Rows {
		args = append(args, rejectedField(row.File, row.Lesson))
	}
	if err := retroStore.Run(ctx, b.r, []string{RetrosKey(b.project)}, args...).Err(); err != nil {
		msg := err.Error()
		switch {
		case strings.HasPrefix(msg, "RETRO_EXISTS"):
			return RetroRecord{}, fmt.Errorf("retro: %s has a retro filed already (agentbus retro show %s)", r.Task, r.Task)
		case strings.HasPrefix(msg, "RETRO_CAP"):
			return RetroRecord{}, fmt.Errorf("retro: the project's retro.max_per_day (%d) is reached today: close it with agentbus retro none %s \"capped: <its signals>\"", cap, r.Task)
		case strings.HasPrefix(msg, "RETRO_REJECTED "):
			n, _ := strconv.Atoi(strings.TrimPrefix(msg, "RETRO_REJECTED "))
			file := ""
			if n >= 1 && n <= len(r.Rows) {
				file = r.Rows[n-1].File
			}
			return RetroRecord{}, fmt.Errorf("retro: row %d repeats a rejected proposal (agentbus retro rejected --file %s)", n, file)
		}
		return RetroRecord{}, err
	}
	if _, err := b.openRetroAsk(ctx, r.Task); err != nil {
		return RetroRecord{}, fmt.Errorf("retro: %s filed queued, but its ask: %w", r.Task, err)
	}
	return b.Retro(ctx, r.Task)
}

// Retro reads task's retro.
func (b *Bus) Retro(ctx context.Context, task string) (RetroRecord, error) {
	rec, _, err := b.retroRaw(ctx, task)
	return rec, err
}

func (b *Bus) retroRaw(ctx context.Context, task string) (RetroRecord, string, error) {
	if !ValidName(task) {
		return RetroRecord{}, "", fmt.Errorf("retro: invalid task %q", task)
	}
	raw, err := b.r.HGet(ctx, RetrosKey(b.project), task).Result()
	if errors.Is(err, redis.Nil) {
		return RetroRecord{}, "", fmt.Errorf("retro: %s has no retro filed", task)
	}
	if err != nil {
		return RetroRecord{}, "", err
	}
	var r RetroRecord
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return RetroRecord{}, "", fmt.Errorf("retro: %s: %w", task, err)
	}
	return r, raw, nil
}

// retroUpdate applies f to task's record and writes it if nobody changed
// it in between (a few tries).
func (b *Bus) retroUpdate(ctx context.Context, task string, f func(*RetroRecord) error) (RetroRecord, error) {
	for range 5 {
		rec, old, err := b.retroRaw(ctx, task)
		if err != nil {
			return RetroRecord{}, err
		}
		if err := f(&rec); err != nil {
			return RetroRecord{}, err
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			return RetroRecord{}, err
		}
		err = retroCAS.Run(ctx, b.r, []string{RetrosKey(b.project)}, task, old, string(raw)).Err()
		if err == nil {
			return rec, nil
		}
		if !strings.HasPrefix(err.Error(), "RETRO_CHANGED") {
			return RetroRecord{}, err
		}
	}
	return RetroRecord{}, fmt.Errorf("retro: %s kept changing", task)
}

// retros reads every record, oldest first.
func (b *Bus) retros(ctx context.Context) ([]RetroRecord, error) {
	all, err := b.r.HGetAll(ctx, RetrosKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	var out []RetroRecord
	for f, v := range all {
		var r RetroRecord
		if strings.HasPrefix(f, "rej|") || json.Unmarshal([]byte(v), &r) != nil {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].Task < out[j].Task
	})
	return out, nil
}

// openRetroAsk opens queued task's ask when the retro slot is free (true),
// else leaves it queued (false).
func (b *Bus) openRetroAsk(ctx context.Context, task string) (bool, error) {
	rec, err := b.Retro(ctx, task)
	if err != nil || rec.State != RetroQueued {
		return false, err
	}
	all, err := b.retros(ctx)
	if err != nil {
		return false, err
	}
	waiting := 0
	for _, r := range all {
		if r.State == RetroQueued {
			waiting++
		}
	}
	id, _, err := b.Ask(ctx, retroAskDef(rec, max(1, waiting)), "")
	if err != nil && errors.Is(err, ErrAskRefused) && strings.Contains(err.Error(), askOpenWhy) {
		return false, nil // the slot is taken: it stays queued
	}
	if err != nil {
		return false, err
	}
	_, err = b.retroUpdate(ctx, task, func(r *RetroRecord) error {
		r.State, r.AskID = RetroOpen, id
		return nil
	})
	return err == nil, err
}

// RetroApproved is the one check of an approval (§5): proposal n of task's
// retro is approved only by a reply entry with typed=yes, ref the retro's
// ask, qid p<n> and said=yes; while the ask's record is kept, its answer to
// p<n> must be the choice yes too. It returns that entry's id, or why not.
// The cmd stream is read, then the archive when one is set
// (SetRetroArchive): an entry in neither approves nothing.
func (b *Bus) RetroApproved(ctx context.Context, task string, n int) (string, error) {
	rec, err := b.Retro(ctx, task)
	if err != nil {
		return "", err
	}
	if n < 1 || n > len(rec.Rows) {
		return "", fmt.Errorf("retro: %s has no proposal %d", task, n)
	}
	if rec.AskID == "" {
		return "", fmt.Errorf("retro: %s was not asked yet (queued)", task)
	}
	qid := "p" + strconv.Itoa(n)
	kept, err := b.r.HExists(ctx, AsksKey(b.project), rec.AskID).Result()
	if err != nil {
		return "", err
	}
	if kept {
		raw, err := b.r.HGet(ctx, AsksKey(b.project), rec.AskID+"|a|"+qid).Result()
		if errors.Is(err, redis.Nil) {
			return "", fmt.Errorf("retro: %s #%d is not answered", task, n)
		}
		if err != nil {
			return "", err
		}
		var a AskAnswer
		if json.Unmarshal([]byte(raw), &a) != nil || a.Withdrawn || a.Choice != "yes" {
			return "", fmt.Errorf("retro: %s #%d was answered %q, not yes", task, n, a.Value())
		}
	}
	e, ok, err := b.retroReply(ctx, rec.AskID, qid)
	if err != nil {
		return "", err
	}
	if !ok || e.Said != "yes" {
		return "", fmt.Errorf("retro: no typed yes from the person to %s #%d", task, n)
	}
	return e.ID, nil
}

// retroReply is the person's typed reply to question qid of ask askID: the
// reply entry with typed=yes, that ref and that qid (the last one), from
// the cmd stream and the archive.
func (b *Bus) retroReply(ctx context.Context, askID, qid string) (Event, bool, error) {
	evs, err := b.Thread(ctx, askID)
	if err != nil {
		return Event{}, false, err
	}
	if b.retroArchive != nil {
		old, err := b.retroArchive(ctx, askID)
		if err != nil {
			return Event{}, false, fmt.Errorf("%w: %v", ErrRetroArchive, err)
		}
		evs = append(old, evs...) // archived ones are older; a copy in both is the same entry
	}
	var found Event
	ok := false
	for _, e := range evs {
		if e.Type == CmdReply && e.Ref == askID && e.QID == qid && e.Typed {
			found, ok = e, true
		}
	}
	return found, ok, nil
}

// RetroOutcomes are each proposal's outcome (§6): accepted only when
// RetroApproved passes; unverified for a yes it cannot prove (never
// rejected); later for a later; rejected for any other answer; unanswered
// for none, or a question withdrawn. A failed read is an error.
func (b *Bus) RetroOutcomes(ctx context.Context, task string) ([]RetroOutcome, error) {
	rec, err := b.Retro(ctx, task)
	if err != nil {
		return nil, err
	}
	var out []RetroOutcome
	for _, row := range rec.Rows {
		o := RetroOutcome{N: row.N, Outcome: OutcomeUnanswered}
		if id, err := b.RetroApproved(ctx, task, row.N); err == nil {
			o.Outcome, o.Entry = OutcomeAccepted, id
			out = append(out, o)
			continue
		} // a failed archive read fails retroReply below too: an error, never an outcome
		if rec.AskID == "" {
			out = append(out, o)
			continue
		}
		qid := "p" + strconv.Itoa(row.N)
		e, typed, err := b.retroReply(ctx, rec.AskID, qid)
		if err != nil {
			return nil, err
		}
		value, answered := "", false
		if raw, err := b.r.HGet(ctx, AsksKey(b.project), rec.AskID+"|a|"+qid).Result(); err == nil {
			var a AskAnswer
			if json.Unmarshal([]byte(raw), &a) == nil && !a.Withdrawn {
				value, answered = a.Value(), true
			}
		} else if !errors.Is(err, redis.Nil) {
			return nil, err
		} else if typed { // the ask's record is gone: the reply entry is the record
			value, answered = e.Said, true
		}
		if answered {
			switch v := strings.ToLower(strings.TrimSpace(value)); v {
			case "later":
				o.Outcome = OutcomeLater
			case "yes": // a yes not proven: never a rejection
				o.Outcome = OutcomeUnverified
			default:
				o.Outcome = OutcomeRejected
			}
			if typed {
				o.Entry = e.ID
			}
		}
		out = append(out, o)
	}
	return out, nil
}

// RetroRoute is master's act once task's ask has closed (§6): answered, or
// withdrawn. It records the outcomes and the rejected proposals, sends
// retro-apply-<task> to docs whatever the answers, and opens the next
// queued retro's ask. It returns that retro's task ("" for none).
func (b *Bus) RetroRoute(ctx context.Context, task string) (string, error) {
	rec, err := b.Retro(ctx, task)
	if err != nil {
		return "", err
	}
	if rec.State != RetroOpen || rec.AskID == "" {
		return "", fmt.Errorf("retro: %s is %s: only an asked retro is routed", task, rec.State)
	}
	st, err := b.r.HGet(ctx, AsksKey(b.project), rec.AskID+"|s").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", err
	}
	if st == "open" {
		return "", fmt.Errorf("retro: %s's ask %s is still open: wait for the answers, or agentbus retro withdraw %s", task, rec.AskID, task)
	}
	outs, err := b.RetroOutcomes(ctx, task)
	if err != nil {
		return "", err
	}
	for _, o := range outs {
		if o.Outcome == OutcomeRejected {
			row := rec.Rows[o.N-1]
			if err := b.r.HSet(ctx, RetrosKey(b.project), rejectedField(row.File, row.Lesson), task+"#"+strconv.Itoa(o.N)).Err(); err != nil {
				return "", err
			}
		}
	}
	apply := RetroApplyName(task)
	body := fmt.Sprintf("Apply the retro of %s: read it with agentbus retro show %s and apply only its accepted rows, one commit each "+
		"on retro/%s from origin/dev (trailers Retro: %s #<n> and Approved-By: <entry id>), with its record "+
		"docs/memory/retros/<date>-%s.md written from retro show; deliver it, then agentbus retro close %s --pr <n>.",
		task, task, task, task, task, task)
	if _, err := b.requestBy(ctx, apply, SystemSender, RetroDocs, "", body, 0, false, "", false); err != nil {
		return "", fmt.Errorf("retro: sending %s to %s: %w", apply, RetroDocs, err)
	}
	if _, err := b.retroUpdate(ctx, task, func(r *RetroRecord) error {
		r.State, r.Outcomes, r.Apply = RetroRouted, outs, apply
		return nil
	}); err != nil {
		return "", err
	}
	all, err := b.retros(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range all {
		if r.State != RetroQueued {
			continue
		}
		opened, err := b.openRetroAsk(ctx, r.Task)
		if err != nil || !opened {
			return "", err
		}
		return r.Task, nil
	}
	return "", nil
}

// RetroWithdraw closes task's open retro ask (master's act, §6): its
// unanswered questions stay unanswered; route follows.
func (b *Bus) RetroWithdraw(ctx context.Context, task string) error {
	rec, err := b.Retro(ctx, task)
	if err != nil {
		return err
	}
	if rec.State != RetroOpen || rec.AskID == "" {
		return fmt.Errorf("retro: %s is %s: no open ask to withdraw", task, rec.State)
	}
	return b.Withdraw(ctx, RetroSender, rec.AskID)
}

// RetroClose records docs' delivery of a routed retro (retro close --pr).
func (b *Bus) RetroClose(ctx context.Context, task string, pr int) error {
	if pr <= 0 {
		return fmt.Errorf("retro: --pr takes the pull request's number")
	}
	_, err := b.retroUpdate(ctx, task, func(r *RetroRecord) error {
		if r.State != RetroRouted {
			return fmt.Errorf("retro: %s is %s: only a routed retro is closed", task, r.State)
		}
		r.State, r.PR = RetroClosed, pr
		return nil
	})
	return err
}

// RejectedRow is a rejected proposal (§6): its file, normalised lesson,
// and the retro and row it came from.
type RejectedRow struct {
	File, Lesson, From string
}

// RetroRejected lists the rejected proposals on file ("" for all), sorted.
func (b *Bus) RetroRejected(ctx context.Context, file string) ([]RejectedRow, error) {
	all, err := b.r.HGetAll(ctx, RetrosKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	var out []RejectedRow
	for f, v := range all {
		rest, ok := strings.CutPrefix(f, "rej|")
		if !ok {
			continue
		}
		i := strings.Index(rest, "|") // a file holds no | (ValidateRetro)
		if i < 0 {
			continue
		}
		r := RejectedRow{File: rest[:i], Lesson: rest[i+1:], From: v}
		if file == "" || r.File == file {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Lesson < out[j].Lesson
	})
	return out, nil
}

// EntryExists says whether id is an entry of a stream a retro cites as
// evidence (§4): the cmd, report, notify and status streams, the events
// stream, or the verdict ledger. The history archive is the caller's.
func (b *Bus) EntryExists(ctx context.Context, id string) (bool, error) {
	if !streamIDPattern.MatchString(id) {
		return false, nil
	}
	keys := []string{EventsKey(b.project), VerdictsKey(b.project)}
	for _, k := range []string{"cmd", "report", "notify", "status"} {
		keys = append(keys, StreamKey(b.project, k))
	}
	for _, k := range keys {
		msgs, err := b.r.XRangeN(ctx, k, id, id, 1).Result()
		if err != nil {
			return false, err
		}
		if len(msgs) > 0 {
			return true, nil
		}
	}
	return false, nil
}
