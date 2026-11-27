package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/redis/go-redis/v9"
)

// Sibling steps run in series, enforced on the board
// (docs/superpowers/specs/2026-11-26-sibling-enforce-design.md §3): every
// request review --more lists its branch in the request's open_steps; a
// review of another branch, or the target's done, is refused while a listed
// step is not judged merged. The judgement is git's, in the agent's own
// process (the CLI): the bus cannot run git and the dispatcher runs no
// repository code. The CLI passes what it judged; the board script checks
// that the request is still the one it read (review_n) and applies it in
// the same step as the transition, or refuses and writes nothing.

// OpenStep is a --more step reviewed and not yet judged merged.
type OpenStep struct {
	Branch string `json:"branch"`
	Commit string `json:"commit,omitempty"` // the head reviewed
}

// ParallelMark is the sender's or the person's exception: while it is set,
// a review of another branch is not refused (every branch is still listed).
type ParallelMark struct {
	By     string `json:"by"`
	Reason string `json:"reason"`
	At     int64  `json:"at"` // Redis TIME, Unix ms
}

// AbandonedStep is an open step closed unmerged (a PR closed): by the sender
// or the person, never the target.
type AbandonedStep struct {
	Branch string `json:"branch"`
	Head   string `json:"head,omitempty"`
	Reason string `json:"reason"`
	By     string `json:"by"`
	At     int64  `json:"at"`
}

// StepMerged is one open step the CLI judged delivered: the head judged and
// the base tip it was judged against.
type StepMerged struct {
	Branch string `json:"branch"`
	Head   string `json:"head"`
	Base   string `json:"base"`
}

// StepsJudged is what the CLI judged before a review or a done: N the
// request's review_n when it read the request, Merged the steps delivered.
type StepsJudged struct {
	N      int
	Merged []StepMerged
}

// args is s as the transition script takes it: the JSON list ("" none) and
// the review_n read.
func (s StepsJudged) args() (string, string, error) {
	if len(s.Merged) == 0 {
		return "", "", nil
	}
	for _, m := range s.Merged {
		if !config.ValidBranchName(m.Branch) || !ValidHead(m.Head) || !ValidHead(m.Base) {
			return "", "", fmt.Errorf("a judged step: invalid branch, head or base")
		}
	}
	b, err := json.Marshal(s.Merged)
	if err != nil {
		return "", "", err
	}
	return string(b), fmt.Sprint(s.N), nil
}

// ParallelReasonMax bounds parallel's and abandon's reason: one line.
const ParallelReasonMax = 200

// stepReason checks a reason: present, one line, at most ParallelReasonMax runes.
func stepReason(reason string) error {
	switch {
	case strings.TrimSpace(reason) == "":
		return fmt.Errorf("a reason is required")
	case strings.ContainsAny(reason, "\r\n"), utf8.RuneCountInString(reason) > ParallelReasonMax:
		return fmt.Errorf("the reason is one line of at most %d characters", ParallelReasonMax)
	}
	return ValidateText(reason, ParallelReasonMax)
}

// SetRequestParallel sets (off: clears) task's parallel exception, by the
// request's sender, or the person (person: the caller proved it is no
// agent, as request priority does). The target is refused, and a done
// request. Off forgets no listed step. changed: the board moved.
func (b *Bus) SetRequestParallel(ctx context.Context, task, by, reason string, off, person bool) (changed bool, err error) {
	if !ValidName(task) || !ValidName(by) {
		return false, fmt.Errorf("invalid task or name")
	}
	if !off {
		if err := stepReason(reason); err != nil {
			return false, err
		}
	}
	o, p := "", ""
	if off {
		o = "1"
	}
	if person {
		p = "1"
	}
	n, err := requestParallel.Run(ctx, b.r, []string{BoardKey(b.project)}, task, by, reason, o, p).Int()
	if err != nil {
		return false, requestError(err)
	}
	return n == 1, nil
}

var requestParallel = redis.NewScript(requestStepsWhoLua + requestParallelSrc)

// requestStepsWhoLua: steps_who(r) refuses anyone but the request's sender
// (ARGV[2]) or the person (ARGV[5] '1'); the target never, even as sender.
const requestStepsWhoLua = `
local function steps_who(r)
 if ARGV[5]=='1' then return nil end
 if r.from~=ARGV[2] or r.target==ARGV[2] then return 'only the request\'s sender or the person may change its steps (the target never)' end
 return nil
end
`

// requestParallelSrc's error_reply literals are refusals requestError marks.
const requestParallelSrc = `
local raw=redis.call('HGET',KEYS[1],ARGV[1]);if not raw then return redis.error_reply('unknown request') end
local e=cjson.decode(raw);local r=e.request
if type(r)~='table' then return redis.error_reply('board task is not a tracked request') end
local who=steps_who(r);if who then return redis.error_reply(who) end
if e.state=='done' then return redis.error_reply('request already completed') end
if ARGV[4]=='1' then
 if type(r.parallel)~='table' then return 0 end
 r.parallel=nil
else
 local t=redis.call('TIME')
 r.parallel={by=ARGV[2],reason=ARGV[3],at=t[1]*1000+math.floor(t[2]/1000)}
end
redis.call('HSET',KEYS[1],ARGV[1],cjson.encode(e))
return 1`

// AbandonStep closes task's open step on branch unmerged (its PR closed),
// by the sender or the person, never the target: it leaves open_steps, its
// head and reason go to abandoned_steps; the recorded branch's facts are
// cleared when it is that branch, and a request in review goes back to
// accepted. It returns the head abandoned ("" when none was recorded).
func (b *Bus) AbandonStep(ctx context.Context, task, branch, by, reason string, person bool) (string, error) {
	if !ValidName(task) || !ValidName(by) || !config.ValidBranchName(branch) {
		return "", fmt.Errorf("invalid task, name or branch")
	}
	if err := stepReason(reason); err != nil {
		return "", err
	}
	p := ""
	if person {
		p = "1"
	}
	head, err := requestAbandon.Run(ctx, b.r, []string{BoardKey(b.project)}, task, by, reason, branch, p).Text()
	if err != nil {
		return "", requestError(err)
	}
	return head, nil
}

var requestAbandon = redis.NewScript(requestStepsWhoLua + requestAbandonSrc)

// requestAbandonSrc: ARGV task, by, reason, branch, person.
const requestAbandonSrc = `
local raw=redis.call('HGET',KEYS[1],ARGV[1]);if not raw then return redis.error_reply('unknown request') end
local e=cjson.decode(raw);local r=e.request
if type(r)~='table' then return redis.error_reply('board task is not a tracked request') end
local who=steps_who(r);if who then return redis.error_reply(who) end
if e.state=='done' then return redis.error_reply('request already completed') end
local steps=r.open_steps;if type(steps)~='table' then steps={} end
local kept={};local found=nil
for _,s in ipairs(steps) do if s.branch==ARGV[4] then found=s else kept[#kept+1]=s end end
if not found then return redis.error_reply('no such open step on this request') end
if #kept>0 then r.open_steps=kept else r.open_steps=nil end
local t=redis.call('TIME');local now=t[1]*1000+math.floor(t[2]/1000)
local ab=r.abandoned_steps;if type(ab)~='table' then ab={} end
ab[#ab+1]={branch=ARGV[4],head=found.commit,reason=ARGV[3],by=ARGV[2],at=now}
r.abandoned_steps=ab
if r.branch==ARGV[4] then r.branch=nil;r.commit=nil;r.review_more=nil;r.review_base=nil end
if e.state=='review' then e.state='accepted' end
e.updated=math.floor(now/1000)
redis.call('HSET',KEYS[1],ARGV[1],cjson.encode(e))
return found.commit or ''`
