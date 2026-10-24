package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Asks (v0.14, spec specs/2026-10-16-asks-and-readable-text-design.md
// §11): an agent's structured questions to a person, in the {p}:asks hash.
// Flat fields, every write one Lua script, so an answer, a replacement and a
// withdrawal never interleave:
//
//	<id>          the definition (from, title, questions) — never an answer
//	<id>|q        question ids, comma separated
//	<id>|s        open | answered | replaced | withdrawn
//	<id>|a|<qid>  the answer (JSON: choice or text, by, at)
//	<id>|r        replaced_by
//	<id>|c        question ids carried to the replacement
//	<id>|e        expiry (ms), set when the ask leaves open
//	open:<agent>  the agent's open ask id
//
// The id is the stream id of the ask's cmd entry (type=ask, target=people,
// wake=no, no ref); each answer is a reply cmd (ref = the ask id) that wakes
// the agent like any directive.

// CmdAsk is the cmd type announcing an ask.
const CmdAsk = "ask"

// PeopleTarget is the target of an ask entry: not an agent, never a consumer
// group, never woken.
const PeopleTarget = "people"

// askRetention: a closed ask stays readable this long.
const askRetention = 7 * 24 * time.Hour

// AsksKey is the {p}:asks hash.
func AsksKey(project string) string { return project + ":asks" }

// AskOption is one choice.
type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// AskQuestion is one question's definition.
type AskQuestion struct {
	ID       string      `json:"id"`
	Header   string      `json:"header"`
	Question string      `json:"question"`
	Context  string      `json:"context,omitempty"`
	Options  []AskOption `json:"options"`
}

// AskDef is what an agent asks: only these fields are ever stored from it.
type AskDef struct {
	From      string        `json:"from"`
	Title     string        `json:"title"`
	Task      string        `json:"task,omitempty"` // the plan task it is about (v0.22); never matched in text
	Questions []AskQuestion `json:"questions"`
}

// AskAnswer is a person's answer to one question.
type AskAnswer struct {
	Choice string `json:"choice,omitempty"`
	Text   string `json:"text,omitempty"`
	By     string `json:"by"`
	At     int64  `json:"at"`
	// Withdrawn (v0.15.1): the asking agent closed this question itself,
	// because the person answered it elsewhere (Text says where).
	Withdrawn bool `json:"withdrawn,omitempty"`
}

// Value is the answer as one line: the chosen label or the free text.
func (a AskAnswer) Value() string {
	if a.Withdrawn {
		return "withdrawn by " + a.By + ": " + a.Text
	}
	if a.Choice != "" {
		return a.Choice
	}
	return a.Text
}

// AskQuestionState is a question with its answer, if any.
type AskQuestionState struct {
	AskQuestion
	Answer *AskAnswer `json:"answer,omitempty"`
}

// Ask is one ask as read back.
type Ask struct {
	ID         string             `json:"id"`
	From       string             `json:"from"`
	Title      string             `json:"title"`
	Task       string             `json:"task,omitempty"`
	State      string             `json:"state"`
	ReplacedBy string             `json:"replaced_by,omitempty"`
	CreatedAt  int64              `json:"created_at"`
	Questions  []AskQuestionState `json:"questions"`
}

// Open counts the unanswered questions of an open ask (0 otherwise).
func (a Ask) Open() int {
	if a.State != "open" {
		return 0
	}
	n := 0
	for _, q := range a.Questions {
		if q.Answer == nil {
			n++
		}
	}
	return n
}

// Ask limits (spec §3).
const (
	AskMaxQuestions   = 6
	AskMaxOptions     = 6
	askMaxHeader      = 16
	askMaxLabel       = 60
	askMaxQuestion    = 300
	askMaxDescription = 300
	askMaxContext     = 2000
	askMaxTitle       = 100
)

// ValidateAskDef checks a definition against the limits.
func ValidateAskDef(d AskDef) error {
	if !ValidName(d.From) {
		return fmt.Errorf("invalid agent %q", d.From)
	}
	if err := textField("title", d.Title, askMaxTitle, true); err != nil {
		return err
	}
	if d.Task != "" && !ValidName(d.Task) {
		return fmt.Errorf("task %q must match %s", d.Task, nameRE.String())
	}
	if len(d.Questions) == 0 || len(d.Questions) > AskMaxQuestions {
		return fmt.Errorf("an ask has 1 to %d questions, not %d", AskMaxQuestions, len(d.Questions))
	}
	seen := map[string]bool{}
	for i, q := range d.Questions {
		where := fmt.Sprintf("question %d", i+1)
		if !ValidName(q.ID) {
			return fmt.Errorf("%s: id %q must match %s", where, q.ID, nameRE.String())
		}
		if seen[q.ID] {
			return fmt.Errorf("%s: id %q is used twice", where, q.ID)
		}
		seen[q.ID] = true
		for _, f := range []struct {
			name, v  string
			max      int
			required bool
		}{{"header", q.Header, askMaxHeader, true}, {"question", q.Question, askMaxQuestion, true}, {"context", q.Context, askMaxContext, false}} {
			if err := textField(where+" "+f.name, f.v, f.max, f.required); err != nil {
				return err
			}
		}
		if len(q.Options) == 0 || len(q.Options) > AskMaxOptions {
			return fmt.Errorf("%s: 1 to %d options, not %d", where, AskMaxOptions, len(q.Options))
		}
		rec, labels := 0, map[string]bool{}
		for j, o := range q.Options {
			ow := fmt.Sprintf("%s option %d", where, j+1)
			if err := textField(ow+" label", o.Label, askMaxLabel, true); err != nil {
				return err
			}
			if labels[o.Label] {
				return fmt.Errorf("%s: label %q is used twice", ow, o.Label)
			}
			labels[o.Label] = true
			if err := textField(ow+" description", o.Description, askMaxDescription, false); err != nil {
				return err
			}
			if o.Recommended {
				rec++
			}
		}
		if rec > 1 {
			return fmt.Errorf("%s: at most one recommended option", where)
		}
	}
	return nil
}

func textField(name, v string, max int, required bool) error {
	if strings.TrimSpace(v) == "" {
		if required {
			return fmt.Errorf("%s is empty", name)
		}
		return nil
	}
	if n := len([]rune(v)); n > max {
		return fmt.Errorf("%s is %d characters, at most %d", name, n, max)
	}
	return ValidateText(v, max)
}

// sameQuestion: a carried question keeps its header, question and labels.
func sameQuestion(a, b AskQuestion) bool {
	if a.Header != b.Header || a.Question != b.Question || len(a.Options) != len(b.Options) {
		return false
	}
	for i := range a.Options {
		if a.Options[i].Label != b.Options[i].Label {
			return false
		}
	}
	return true
}

// sweepLua deletes the field groups of asks whose expiry has passed.
const sweepLua = `
local function sweep(key, now)
  local all = redis.call('HGETALL', key)
  local dead = {}
  for i = 1, #all, 2 do
    local f, v = all[i], all[i+1]
    if string.sub(f, -2) == '|e' and tonumber(v) and tonumber(v) < now then
      dead[string.sub(f, 1, -3)] = true
    end
  end
  for i = 1, #all, 2 do
    local f = all[i]
    local id = string.match(f, '^([^|]+)')
    if id and dead[id] then redis.call('HDEL', key, f) end
  end
end
local function nowms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end
local function csvhas(csv, x)
  if not csv then return false end
  for v in string.gmatch(csv, '[^,]+') do if v == x then return true end end
  return false
end
local function remaining(key, id)
  local n = 0
  local qs = redis.call('HGET', key, id .. '|q')
  if not qs then return 0 end
  for q in string.gmatch(qs, '[^,]+') do
    if redis.call('HEXISTS', key, id .. '|a|' .. q) == 0 then n = n + 1 end
  end
  return n
end
`

// askCreate: KEYS asks, cmd. ARGV maxlen, from, title, def, qids, replaces,
// carried, retention ms. Returns {id, state} or an error reply.
var askCreate = redis.NewScript(sweepLua + `
local now = nowms()
sweep(KEYS[1], now)
local from, replaces = ARGV[2], ARGV[6]
local open = redis.call('HGET', KEYS[1], 'open:' .. from)
if open and redis.call('HGET', KEYS[1], open .. '|s') ~= 'open' then open = nil end
if replaces == '' then
  if open then return redis.error_reply('ASK_OPEN ' .. open) end
else
  if open ~= replaces then return redis.error_reply('ASK_NOT_OPEN') end
end
local id = redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[1], '*', 'from', from, 'target', 'people',
  'type', 'ask', 'command', ARGV[3], 'wake', 'no', 'text_complete', 'yes')
redis.call('HSET', KEYS[1], id, ARGV[4], id .. '|q', ARGV[5], id .. '|s', 'open')
if replaces ~= '' then
  for q in string.gmatch(ARGV[7], '[^,]+') do
    local a = redis.call('HGET', KEYS[1], replaces .. '|a|' .. q)
    if a then redis.call('HSET', KEYS[1], id .. '|a|' .. q, a) end
  end
  redis.call('HSET', KEYS[1], replaces .. '|s', 'replaced', replaces .. '|r', id, replaces .. '|c', ARGV[7],
    replaces .. '|e', tostring(now + tonumber(ARGV[8])))
end
local state = 'open'
if remaining(KEYS[1], id) == 0 then
  state = 'answered'
  redis.call('HSET', KEYS[1], id .. '|s', 'answered', id .. '|e', tostring(now + tonumber(ARGV[8])))
  redis.call('HDEL', KEYS[1], 'open:' .. from)
else
  redis.call('HSET', KEYS[1], 'open:' .. from, id)
end
return {id, state}`)

// askAnswer: KEYS asks, cmd. ARGV maxlen, id, qid, answer JSON (no at),
// by, agent, reply text, retention ms. Returns {id, remaining, reply id}.
var askAnswer = redis.NewScript(sweepLua + `
local now = nowms()
sweep(KEYS[1], now)
local id, qid = ARGV[2], ARGV[3]
local s = redis.call('HGET', KEYS[1], id .. '|s')
if s == 'replaced' then
  if not csvhas(redis.call('HGET', KEYS[1], id .. '|c'), qid) then return redis.error_reply('ASK_REPLACED') end
  id = redis.call('HGET', KEYS[1], id .. '|r')
  s = redis.call('HGET', KEYS[1], id .. '|s')
end
if not s then return redis.error_reply('ASK_GONE') end
if s ~= 'open' then return redis.error_reply('ASK_' .. string.upper(s)) end
if not csvhas(redis.call('HGET', KEYS[1], id .. '|q'), qid) then return redis.error_reply('ASK_NO_QUESTION') end
if redis.call('HEXISTS', KEYS[1], id .. '|a|' .. qid) == 1 then return redis.error_reply('ASK_ANSWERED') end
local a = cjson.decode(ARGV[4])
a['at'] = now
redis.call('HSET', KEYS[1], id .. '|a|' .. qid, cjson.encode(a))
local left = remaining(KEYS[1], id)
local text = ARGV[7]
if left > 0 then text = text .. ' (' .. left .. ' question(s) still open)' end
local reply = redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[1], '*', 'from', ARGV[5], 'target', ARGV[6],
  'type', 'reply', 'ref', id, 'command', text, 'human', 'yes', 'text_complete', 'yes')
if left == 0 then
  redis.call('HSET', KEYS[1], id .. '|s', 'answered', id .. '|e', tostring(now + tonumber(ARGV[8])))
  if redis.call('HGET', KEYS[1], 'open:' .. ARGV[6]) == id then redis.call('HDEL', KEYS[1], 'open:' .. ARGV[6]) end
end
return {id, left, reply}`)

// askWithdraw: KEYS asks. ARGV id, agent, retention ms.
var askWithdraw = redis.NewScript(sweepLua + `
local now = nowms()
sweep(KEYS[1], now)
local s = redis.call('HGET', KEYS[1], ARGV[1] .. '|s')
if s ~= 'open' then return redis.error_reply('ASK_NOT_OPEN') end
if redis.call('HGET', KEYS[1], 'open:' .. ARGV[2]) ~= ARGV[1] then return redis.error_reply('ASK_NOT_YOURS') end
redis.call('HSET', KEYS[1], ARGV[1] .. '|s', 'withdrawn', ARGV[1] .. '|e', tostring(now + tonumber(ARGV[3])))
redis.call('HDEL', KEYS[1], 'open:' .. ARGV[2])
return 1`)

// askWithdrawQuestion: KEYS asks, cmd. ARGV id, agent, qid, note, retention
// ms, maxlen. One question closed by its own agent, with its trace on the
// ask's thread (a wake=no reply to people, citing where it was answered);
// the ask ends when none is left open: "answered" if any question has a
// person's answer, else "withdrawn".
var askWithdrawQuestion = redis.NewScript(sweepLua + `
local now = nowms()
sweep(KEYS[1], now)
local id, qid = ARGV[1], ARGV[3]
local s = redis.call('HGET', KEYS[1], id .. '|s')
if s ~= 'open' then return redis.error_reply('ASK_NOT_OPEN') end
if redis.call('HGET', KEYS[1], 'open:' .. ARGV[2]) ~= id then return redis.error_reply('ASK_NOT_YOURS') end
if not csvhas(redis.call('HGET', KEYS[1], id .. '|q'), qid) then return redis.error_reply('ASK_NO_QUESTION') end
if redis.call('HEXISTS', KEYS[1], id .. '|a|' .. qid) == 1 then return redis.error_reply('ASK_ANSWERED') end
redis.call('HSET', KEYS[1], id .. '|a|' .. qid, cjson.encode({withdrawn = true, by = ARGV[2], text = ARGV[4], at = now}))
redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[6], '*', 'from', ARGV[2], 'target', 'people', 'type', 'reply',
  'ref', id, 'command', 'Question ' .. qid .. ' closed: ' .. ARGV[4], 'wake', 'no', 'text_complete', 'yes')
local left = remaining(KEYS[1], id)
if left == 0 then
  local state = 'withdrawn'
  for q in string.gmatch(redis.call('HGET', KEYS[1], id .. '|q'), '[^,]+') do
    local a = cjson.decode(redis.call('HGET', KEYS[1], id .. '|a|' .. q))
    if not a['withdrawn'] then state = 'answered' end
  end
  redis.call('HSET', KEYS[1], id .. '|s', state, id .. '|e', tostring(now + tonumber(ARGV[5])))
  redis.call('HDEL', KEYS[1], 'open:' .. ARGV[2])
end
return left`)

// ErrAskRefused wraps a refusal from an ask script (already answered,
// replaced, another ask open, …); the message says which.
var ErrAskRefused = errors.New("ask refused")

func askErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if i := strings.Index(msg, "ASK_"); i >= 0 {
		why := map[string]string{
			"ASK_OPEN":        "this agent already has an open ask (replace it with --replace)",
			"ASK_NOT_OPEN":    "the ask to replace or withdraw is not this agent's open ask",
			"ASK_REPLACED":    "the ask was replaced and this question changed",
			"ASK_GONE":        "no such ask",
			"ASK_ANSWERED":    "this question is already answered",
			"ASK_NO_QUESTION": "no such question in this ask",
			"ASK_WITHDRAWN":   "the ask was withdrawn",
			"ASK_NOT_YOURS":   "the ask is not this agent's",
		}
		code := strings.Fields(msg[i:])[0]
		if w, ok := why[code]; ok {
			return fmt.Errorf("%w: %s", ErrAskRefused, w)
		}
		return fmt.Errorf("%w: %s", ErrAskRefused, strings.ToLower(strings.TrimPrefix(code, "ASK_")))
	}
	return err
}

// Ask stores an agent's questions; with replaces, it supersedes that open
// ask, carrying the answers of unchanged questions. It returns the ask id and
// its state (open, or answered when every question was carried answered).
func (b *Bus) Ask(ctx context.Context, def AskDef, replaces string) (string, string, error) {
	if err := ValidateAskDef(def); err != nil {
		return "", "", err
	}
	var carried []string
	if replaces != "" {
		old, err := b.askDef(ctx, replaces)
		if err != nil {
			return "", "", err
		}
		byID := map[string]AskQuestion{}
		for _, q := range old.Questions {
			byID[q.ID] = q
		}
		for _, q := range def.Questions {
			if o, ok := byID[q.ID]; ok {
				if !sameQuestion(o, q) {
					return "", "", fmt.Errorf("question %q changed wording: give it a new id", q.ID)
				}
				carried = append(carried, q.ID)
			}
		}
	}
	raw, _ := json.Marshal(def)
	ids := make([]string, len(def.Questions))
	for i, q := range def.Questions {
		ids[i] = q.ID
	}
	res, err := askCreate.Run(ctx, b.r, []string{AsksKey(b.project), StreamKey(b.project, "cmd")},
		streamMaxLen, def.From, def.Title, string(raw), strings.Join(ids, ","), replaces,
		strings.Join(carried, ","), askRetention.Milliseconds()).StringSlice()
	if err != nil {
		return "", "", askErr(err)
	}
	return res[0], res[1], nil
}

func (b *Bus) askDef(ctx context.Context, id string) (AskDef, error) {
	raw, err := b.r.HGet(ctx, AsksKey(b.project), id).Result()
	if err == redis.Nil {
		return AskDef{}, fmt.Errorf("%w: no such ask", ErrAskRefused)
	}
	if err != nil {
		return AskDef{}, err
	}
	var d AskDef
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return AskDef{}, err
	}
	return d, nil
}

// Answer records a person's answer to one question and sends it to the agent
// as a reply (ref = the ask id), in one script. choice must be an option
// label; otherwise text is the free answer. It returns the ask id answered
// (the replacement when the ask was replaced) and the questions still open.
func (b *Bus) Answer(ctx context.Context, askID, qid, by, choice, text string) (string, int, error) {
	if !ValidName(by) {
		return "", 0, fmt.Errorf("invalid person %q", by)
	}
	def, err := b.askDef(ctx, askID)
	if err != nil {
		return "", 0, err
	}
	var q *AskQuestion
	for i := range def.Questions {
		if def.Questions[i].ID == qid {
			q = &def.Questions[i]
		}
	}
	if q == nil {
		return "", 0, fmt.Errorf("%w: no such question in this ask", ErrAskRefused)
	}
	a := AskAnswer{By: by}
	switch {
	case choice != "":
		ok := false
		for _, o := range q.Options {
			ok = ok || o.Label == choice
		}
		if !ok {
			return "", 0, fmt.Errorf("%q is not an option of %q", choice, q.Header)
		}
		a.Choice = choice
	case strings.TrimSpace(text) != "":
		a.Text = strings.TrimSpace(text)
	default:
		return "", 0, fmt.Errorf("an answer is an option or some text")
	}
	reply := fmt.Sprintf("Answer to %q: %s", q.Header, a.Value())
	// the script may add " (N question(s) still open)": the whole line must fit
	if err := ValidateText(reply+fmt.Sprintf(" (%d question(s) still open)", AskMaxQuestions), CmdMaxRunes); err != nil {
		return "", 0, err
	}
	raw, _ := json.Marshal(a)
	res, err := askAnswer.Run(ctx, b.r, []string{AsksKey(b.project), StreamKey(b.project, "cmd")},
		streamMaxLen, askID, qid, string(raw), by, def.From, reply, askRetention.Milliseconds()).Slice()
	if err != nil {
		return "", 0, askErr(err)
	}
	id, _ := res[0].(string)
	left, _ := res[1].(int64)
	return id, int(left), nil
}

// Withdraw closes an agent's own open ask.
func (b *Bus) Withdraw(ctx context.Context, agent, askID string) error {
	err := askWithdraw.Run(ctx, b.r, []string{AsksKey(b.project)}, askID, agent, askRetention.Milliseconds()).Err()
	return askErr(err)
}

// WithdrawQuestion closes one question of agent's open ask (v0.15.1 §11.4):
// the person answered it elsewhere, note says where. It returns how many
// questions stay open; the ask ends with the last one.
func (b *Bus) WithdrawQuestion(ctx context.Context, agent, askID, qid, note string) (int, error) {
	if err := ValidateText(note, CmdMaxRunes); err != nil {
		return 0, err
	}
	n, err := askWithdrawQuestion.Run(ctx, b.r, []string{AsksKey(b.project), StreamKey(b.project, "cmd")},
		askID, agent, qid, note, askRetention.Milliseconds(), streamMaxLen).Int()
	return n, askErr(err)
}

// Asks reads every ask, oldest first.
func (b *Bus) Asks(ctx context.Context) ([]Ask, error) {
	all, err := b.r.HGetAll(ctx, AsksKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	var out []Ask
	for f, v := range all {
		if strings.Contains(f, "|") || strings.HasPrefix(f, "open:") {
			continue
		}
		var d AskDef
		if json.Unmarshal([]byte(v), &d) != nil {
			continue
		}
		ms, _ := splitID(f)
		a := Ask{ID: f, From: d.From, Title: d.Title, Task: d.Task, State: all[f+"|s"], ReplacedBy: all[f+"|r"], CreatedAt: ms}
		for _, q := range d.Questions {
			qs := AskQuestionState{AskQuestion: q}
			if raw, ok := all[f+"|a|"+q.ID]; ok {
				var ans AskAnswer
				if json.Unmarshal([]byte(raw), &ans) == nil {
					qs.Answer = &ans
				}
			}
			a.Questions = append(a.Questions, qs)
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return idLess(out[i].ID, out[j].ID) })
	return out, nil
}
