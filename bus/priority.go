package bus

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Request priority (spec docs/superpowers/specs/2026-11-04-request-priority-design.md):
// one of three levels on a tracked request, set by the person, ordering
// ready work and nothing else. The level lives in the request's own board
// entry (RequestInfo.Priority); absent means normal.

const (
	PriorityHigh   = "high"
	PriorityNormal = "normal"
	PriorityLow    = "low"
)

// ValidPriority reports whether level is one a person may set.
func ValidPriority(level string) bool {
	return level == PriorityHigh || level == PriorityNormal || level == PriorityLow
}

// PriorityRank orders levels: high 0, normal 1 (absent or unknown), low 2.
func PriorityRank(level string) int {
	switch level {
	case PriorityHigh:
		return 0
	case PriorityLow:
		return 2
	}
	return 1
}

// PriorityLevel is a stored level as read: "" and anything unknown read normal.
func PriorityLevel(stored string) string {
	switch stored {
	case PriorityHigh, PriorityLow:
		return stored
	}
	return PriorityNormal
}

// RequestLess orders ready requests: priority rank, then created_at, then id.
func RequestLess(a, b RequestInfo) bool {
	if ra, rb := PriorityRank(a.Priority), PriorityRank(b.Priority); ra != rb {
		return ra < rb
	}
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt < b.CreatedAt
	}
	return idLess(a.ID, b.ID)
}

// NextRequest is the request agent should take next, from the board as
// Bus.Board returns it (Availability derived), or why none: an accepted
// request is finished first; else the first ready one (requested, its body
// retained and not expired) by RequestLess. A blocked request does not hold
// the agent, and a done one is in review: neither in hand nor ready (no
// review barrier). Pure: it reads only the derived values.
func NextRequest(board map[string]BoardEntry, agent string) (task, why string) {
	var inHand, ready string
	var inHandR, readyR RequestInfo
	better := func(cur string, curR RequestInfo, t string, r RequestInfo) bool {
		if cur == "" || RequestLess(r, curR) {
			return true
		}
		return !RequestLess(curR, r) && t < cur // equal: the task name decides, so the answer is stable
	}
	for t, e := range board {
		r := e.Request
		if r == nil || r.Target != agent {
			continue
		}
		switch {
		case e.State == "accepted":
			if better(inHand, inHandR, t, *r) {
				inHand, inHandR = t, *r
			}
		case e.State == "requested" && r.Availability == "retained":
			if better(ready, readyR, t, *r) {
				ready, readyR = t, *r
			}
		}
	}
	switch {
	case inHand != "":
		return inHand, "finish first"
	case ready != "":
		return ready, ""
	}
	return "", "nothing ready"
}

// SetRequestPriority sets a tracked request's level, as the person by: high
// or low stores priority, priority_by and priority_at (Redis TIME); normal
// removes all three. One script on the board: unknown, untracked and done
// requests are refused (ErrRequestRefused, requestTransition's own words);
// the same level as stored writes nothing (changed false); updated is left
// as it was, since a priority is no progress. No stream entry is written.
func (b *Bus) SetRequestPriority(ctx context.Context, task, level, by string) (changed bool, err error) {
	if !ValidName(task) || !ValidName(by) {
		return false, fmt.Errorf("invalid task or person")
	}
	if !ValidPriority(level) {
		return false, fmt.Errorf("priority must be high, normal or low")
	}
	n, err := requestPriority.Run(ctx, b.r, []string{BoardKey(b.project)}, task, level, by).Int()
	if err != nil {
		return false, requestError(err)
	}
	return n == 1, nil
}

var requestPriority = redis.NewScript(requestPrioritySrc)

// requestPrioritySrc's error_reply literals are refusals requestError marks
// (TestEveryScriptRefusalIsListed reads both scripts).
const requestPrioritySrc = `
local raw=redis.call('HGET',KEYS[1],ARGV[1]);if not raw then return redis.error_reply('unknown request') end
local e=cjson.decode(raw);local r=e.request
if not r then return redis.error_reply('board task is not a tracked request') end
if e.state=='done' then return redis.error_reply('request already completed') end
local cur=r.priority;if cur~='high' and cur~='low' then cur='normal' end
if cur==ARGV[2] then return 0 end
if ARGV[2]=='normal' then
 r.priority=nil;r.priority_by=nil;r.priority_at=nil
else
 local t=redis.call('TIME')
 r.priority=ARGV[2];r.priority_by=ARGV[3];r.priority_at=t[1]*1000+math.floor(t[2]/1000)
end
redis.call('HSET',KEYS[1],ARGV[1],cjson.encode(e))
return 1`
