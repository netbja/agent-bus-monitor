package bus

import (
	"context"
	"fmt"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/redis/go-redis/v9"
)

// Close on merge (board-drift §3.3, §3.4): the merge queue's pass found a
// review request's branch head delivered into the base. One fenced script
// checks the lease, that the request is still in review on that branch, and
// closes it (done, closed_on_merge, responded_at from TIME) or, for a
// --more step, sends it back to accepted with the step recorded. It writes
// no reply, so it wakes nobody (Q3), and one request-merged event.

// What FencedRequestMerged did.
const (
	MergedClosed = "closed" // done, closed on merge
	MergedStep   = "step"   // a --more step: back to accepted
	MergedOpen   = "open"   // a last review, steps still listed (sibling-enforce §3.4): back to accepted
	MergedNone   = ""       // nothing: not in review on that branch any more
)

// requestMergedScript: KEYS lease, board, events, audit. ARGV token, task,
// branch, head, base, events maxlen, the review's number the pass read, then
// three fencedAudit blocks: the line if it closes (8-19), if it steps
// (20-31), if a last review merges with steps still listed (32-43).
// The merge's audit line (audit step 2, master's answer: merge is recorded
// here) goes in the same step, its fields checked in Go.
var requestMergedScript = redis.NewScript(auditLua + `
if redis.call('GET',KEYS[1])~=ARGV[1] then return 'not-holder' end
local raw=redis.call('HGET',KEYS[2],ARGV[2]);if not raw then return '' end
local e=cjson.decode(raw);local r=e.request
if type(r)~='table' or e.state~='review' or r.branch~=ARGV[3] or (tonumber(r.review_n) or 0)~=tonumber(ARGV[7]) then return '' end
local t=redis.call('TIME');local now=t[1]*1000+math.floor(t[2]/1000)
local out='closed'
if r.review_more then
 local steps=r.merged_steps
 if type(steps)~='table' then steps={} end
 steps[#steps+1]=ARGV[4]
 r.merged_steps=steps
 r.branch=nil;r.commit=nil;r.review_more=nil;r.review_base=nil
 e.state='accepted'
 out='step'
elseif type(r.open_steps)=='table' and #r.open_steps>0 then
 local steps=r.merged_steps
 if type(steps)~='table' then steps={} end
 steps[#steps+1]=ARGV[4]
 r.merged_steps=steps
 r.branch=nil;r.commit=nil;r.review_base=nil
 e.state='accepted'
 out='open'
else
 r.closed_on_merge={head=ARGV[4],base=ARGV[5]}
 r.responded_at=now
 e.state='done'
end
e.updated=math.floor(now/1000)
redis.call('HSET',KEYS[2],ARGV[2],cjson.encode(e))
local detail=ARGV[2]..' '..ARGV[3]..' '..ARGV[4]
if out=='open' then
 local names={}
 for _,s in ipairs(r.open_steps) do names[#names+1]=s.branch end
 detail=detail..' open '..table.concat(names,',')
end
redis.call('XADD',KEYS[3],'MAXLEN','~',ARGV[6],'*','kind','request-merged','agent',r.target or '','term','','by','dispatch','detail',detail)
if out=='closed' then audit_fenced(KEYS[4],8,'') elseif out=='step' then audit_fenced(KEYS[4],20,'') else audit_fenced(KEYS[4],32,'') end
return out`)

// FencedRequestMerged closes task on its branch's merge, fenced by the
// dispatch lease; MergedNone when the request changed since it was read:
// another state or branch, or another review (reviewN, its review_n then).
func (b *Bus) FencedRequestMerged(ctx context.Context, token, task, branch, head, base string, reviewN int) (string, error) {
	if !ValidName(task) || !config.ValidBranchName(branch) || !ValidHead(head) || !config.ValidBranchName(base) {
		return "", fmt.Errorf("request merged: invalid task, branch, head or base")
	}
	line := func(out string) []any {
		return fencedAudit(AuditEntry{Action: "merge", Actor: "dispatch", Target: branch, Outcome: AuditOK,
			Detail: "head " + head[:8] + " into " + base + ", request " + task + " " + out})
	}
	args := append([]any{token, task, branch, head, base, eventsMaxLen, reviewN}, line(MergedClosed)...)
	args = append(args, line(MergedStep)...)
	args = append(args, line(MergedOpen)...)
	v, err := requestMergedScript.Run(ctx, b.r, []string{DispatchKey(b.project), BoardKey(b.project), EventsKey(b.project), AuditKey(b.project)}, args...).Text()
	if err == redis.Nil {
		return MergedNone, nil
	}
	if err != nil {
		return "", err
	}
	if v == "not-holder" {
		return "", ErrNotHolder
	}
	return v, nil
}
