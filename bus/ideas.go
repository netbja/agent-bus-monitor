package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/redis/go-redis/v9"
)

// Ideas (ideas slice 1, architect's report 1794598756532-0): the project's
// ideas in the {p}:ideas hash, which live for months, unlike asks. Flat
// fields, every write one Lua script with its time from Redis TIME:
//
//	seq        the last number given (INCR): ids stay short, i12
//	<id>       the definition JSON {title, text, by, at, source}; never rewritten
//	<id>|s     the state: to discuss, decided, doing, dropped
//	<id>|log   the audit: a JSON list of {from, to, by, at}, the newest ideaLogMax
//
// An idea always starts at "to discuss", and anyone may add one. Only a
// person sets a state: the CLI refuses an agent's environment, and the
// script refuses a name registered as an agent of the project or a reserved
// one. That is a guard against accidents and an audit, not a proof: agents
// run under the person's uid, so a deliberate `--as <person>` or a raw HSET
// is not stopped (docs/operator-notes.md, Ideas).

// The four states. No state is final: dropped can go back to discussion.
const (
	IdeaToDiscuss = "to discuss"
	IdeaDecided   = "decided"
	IdeaDoing     = "doing"
	IdeaDropped   = "dropped"
)

// IdeaStates are the states in their order of life.
var IdeaStates = []string{IdeaToDiscuss, IdeaDecided, IdeaDoing, IdeaDropped}

// Idea limits.
const (
	IdeaMaxTitle  = 100
	IdeaMaxText   = 2000
	IdeaMaxSource = 200
	ideaLogMax    = 50
)

// IdeasKey is the {p}:ideas hash.
func IdeasKey(project string) string { return project + ":ideas" }

// ParseIdeaState reads a state as typed: case and - or _ for the space do
// not matter, and "discuss" is "to discuss".
func ParseIdeaState(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(s)))
	if s == "discuss" {
		return IdeaToDiscuss, true
	}
	for _, st := range IdeaStates {
		if s == st {
			return st, true
		}
	}
	return "", false
}

// reservedPeople are names a person may not set a state as: the bus's own
// senders and placeholders, never a person.
var reservedPeople = []string{"agentbus", "dispatch", "anonymous", "pilot", PeopleTarget, "conductor"}

// IdeaDef is what an idea is added with; At is set by the script.
type IdeaDef struct {
	Title  string `json:"title"`
	Text   string `json:"text,omitempty"`
	By     string `json:"by"`
	At     int64  `json:"at"`
	Source string `json:"source,omitempty"` // where it came from: a file, a note, a thread
	// Request, when set, makes the add idempotent (reviewer-codex): a retry
	// with the same id, after a reply lost once the script committed, finds
	// the idea it made instead of making another. Never stored in the
	// definition; its field req|<id> expires after ideaRequestTTL.
	Request string `json:"-"`
}

// ideaRequestTTL keeps an add's request id this long: retries come within
// seconds, a day is plenty.
const ideaRequestTTL = 24 * 60 * 60

var ideaRequestRE = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// ValidIdeaRequest: a request id is 8 to 64 letters, digits, - or _.
func ValidIdeaRequest(s string) bool { return ideaRequestRE.MatchString(s) }

// IdeaChange is one state change of the audit.
type IdeaChange struct {
	From string `json:"from"`
	To   string `json:"to"`
	By   string `json:"by"`
	At   int64  `json:"at"`
}

// Idea is one idea as read back.
type Idea struct {
	ID string `json:"id"`
	IdeaDef
	State string       `json:"state"`
	Log   []IdeaChange `json:"log"`
}

// SetBy is the last change's author and time ("", 0 before any).
func (i Idea) SetBy() (string, int64) {
	if len(i.Log) == 0 {
		return "", 0
	}
	l := i.Log[len(i.Log)-1]
	return l.By, l.At
}

// ValidateIdeaDef checks a definition against the limits.
func ValidateIdeaDef(d IdeaDef) error {
	if !ValidName(d.By) {
		return fmt.Errorf("invalid author %q", d.By)
	}
	if err := textField("title", d.Title, IdeaMaxTitle, true); err != nil {
		return err
	}
	if err := textField("text", d.Text, IdeaMaxText, false); err != nil {
		return err
	}
	if err := textField("source", d.Source, IdeaMaxSource, false); err != nil {
		return err
	}
	// No control character: one line for the title and the source, the
	// text may keep its newlines and tabs.
	for _, f := range []struct{ name, v, keep string }{{"title", d.Title, ""}, {"source", d.Source, ""}, {"text", d.Text, "\n\t"}} {
		if strings.ContainsFunc(f.v, func(r rune) bool {
			return (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) && !strings.ContainsRune(f.keep, r)
		}) {
			return fmt.Errorf("the %s holds a control character", f.name)
		}
	}
	return nil
}

const ideaNowLua = `
local function nowms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end
`

// ideaAdd: KEYS ideas. ARGV def JSON (no at), request id ("" none), its
// TTL in seconds. Returns the id: the one this request made before, if any.
var ideaAdd = redis.NewScript(ideaNowLua + `
if ARGV[2] ~= '' then
  local had = redis.call('HGET', KEYS[1], 'req|' .. ARGV[2])
  if had then return had end
end
local n = redis.call('HINCRBY', KEYS[1], 'seq', 1)
local id = 'i' .. n
local d = cjson.decode(ARGV[1])
d['at'] = nowms()
redis.call('HSET', KEYS[1], id, cjson.encode(d), id .. '|s', 'to discuss')
if ARGV[2] ~= '' then
  redis.call('HSET', KEYS[1], 'req|' .. ARGV[2], id)
  redis.call('HEXPIRE', KEYS[1], tonumber(ARGV[3]), 'FIELDS', 1, 'req|' .. ARGV[2])
end
return id`)

// ideaSet: KEYS ideas, agents, cmd. ARGV id, expected state, new state, by,
// reserved names (comma separated), log max, stream maxlen. A
// compare-and-set: refused unless the idea is in the expected state.
var ideaSet = redis.NewScript(ideaNowLua + `
local id, expect, to, by = ARGV[1], ARGV[2], ARGV[3], ARGV[4]
for r in string.gmatch(ARGV[5], '[^,]+') do
  if r == by then return redis.error_reply('IDEA_RESERVED') end
end
if redis.call('HEXISTS', KEYS[2], by) == 1 then return redis.error_reply('IDEA_AGENT') end
local def = redis.call('HGET', KEYS[1], id)
local s = redis.call('HGET', KEYS[1], id .. '|s')
if not def or not s then return redis.error_reply('IDEA_GONE') end
if s ~= expect then return redis.error_reply('IDEA_CHANGED ' .. s) end
if s == to then return redis.error_reply('IDEA_SAME') end
local now = nowms()
local log = {}
local raw = redis.call('HGET', KEYS[1], id .. '|log')
if raw then log = cjson.decode(raw) end
table.insert(log, {from = s, to = to, by = by, at = now})
while #log > tonumber(ARGV[6]) do table.remove(log, 1) end
redis.call('HSET', KEYS[1], id .. '|s', to, id .. '|log', cjson.encode(log))
local title = cjson.decode(def)['title'] or ''
redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[7], '*', 'from', by, 'target', 'people', 'type', 'reply',
  'command', 'Idea ' .. id .. ' ' .. title .. ': ' .. s .. ' → ' .. to, 'wake', 'no', 'human', 'yes', 'text_complete', 'yes')
return 1`)

// ErrIdeaRefused wraps a refusal from an idea script; the message says why.
var ErrIdeaRefused = errors.New("idea refused")

func ideaErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	i := strings.Index(msg, "IDEA_")
	if i < 0 {
		return err
	}
	f := strings.Fields(msg[i:])
	switch f[0] {
	case "IDEA_CHANGED":
		now := strings.Join(f[1:], " ")
		return fmt.Errorf("%w: the idea is now %q (someone else changed it): read it again", ErrIdeaRefused, now)
	case "IDEA_GONE":
		return fmt.Errorf("%w: no such idea", ErrIdeaRefused)
	case "IDEA_SAME":
		return fmt.Errorf("%w: the idea is already in that state", ErrIdeaRefused)
	case "IDEA_AGENT":
		return fmt.Errorf("%w: that name is an agent of this project: only a person sets a state", ErrIdeaRefused)
	case "IDEA_RESERVED":
		return fmt.Errorf("%w: that name is the bus's own, not a person's", ErrIdeaRefused)
	}
	return fmt.Errorf("%w: %s", ErrIdeaRefused, strings.ToLower(strings.TrimPrefix(f[0], "IDEA_")))
}

// AddIdea stores a new idea at "to discuss" and returns its id.
func (b *Bus) AddIdea(ctx context.Context, d IdeaDef) (string, error) {
	if err := ValidateIdeaDef(d); err != nil {
		return "", err
	}
	if d.Request != "" && !ValidIdeaRequest(d.Request) {
		return "", fmt.Errorf("invalid request id %q", d.Request)
	}
	d.At = 0
	raw, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	id, err := ideaAdd.Run(ctx, b.r, []string{IdeasKey(b.project)}, string(raw), d.Request, ideaRequestTTL).Text()
	return id, ideaErr(err)
}

// SetIdeaState moves idea id from expect to to, as the person by.
func (b *Bus) SetIdeaState(ctx context.Context, id, expect, to, by string) error {
	if !ValidName(by) {
		return fmt.Errorf("%w: %q is not a valid name", ErrIdeaRefused, by)
	}
	e, ok1 := ParseIdeaState(expect)
	t, ok2 := ParseIdeaState(to)
	if !ok1 || !ok2 {
		return fmt.Errorf("%w: a state is one of %s", ErrIdeaRefused, strings.Join(IdeaStates, ", "))
	}
	err := ideaSet.Run(ctx, b.r, []string{IdeasKey(b.project), AgentsKey(b.project), StreamKey(b.project, "cmd")},
		id, e, t, by, strings.Join(reservedPeople, ","), ideaLogMax, streamMaxLen).Err()
	return ideaErr(err)
}

// Ideas reads every idea, in the order they were added.
func (b *Bus) Ideas(ctx context.Context) ([]Idea, error) {
	raw, err := b.r.HGetAll(ctx, IdeasKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	var out []Idea
	for f, v := range raw {
		if f == "seq" || strings.Contains(f, "|") {
			continue
		}
		it := Idea{ID: f, State: raw[f+"|s"]}
		if json.Unmarshal([]byte(v), &it.IdeaDef) != nil {
			continue
		}
		if l, ok := raw[f+"|log"]; ok {
			_ = json.Unmarshal([]byte(l), &it.Log)
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return ideaNum(out[i].ID) < ideaNum(out[j].ID) })
	return out, nil
}

func ideaNum(id string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "i"))
	if err != nil {
		return 1 << 30
	}
	return n
}
