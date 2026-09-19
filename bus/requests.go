package bus

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RequestInfo is metadata in the EXISTING board. It does not duplicate message
// bodies, the cmd thread, ownership or the verdict ledger. Times are Unix ms.
type RequestInfo struct {
	ID                string `json:"id"`
	Thread            string `json:"thread"`
	From              string `json:"from"`
	Target            string `json:"target"`
	CreatedAt         int64  `json:"created_at"`
	ExpiresAt         int64  `json:"expires_at,omitempty"`
	Delivery          string `json:"delivery"`
	OutputWrittenAt   int64  `json:"output_written_at,omitempty"`
	Attempts          int64  `json:"attempts,omitempty"`
	DuplicatePossible bool   `json:"duplicate_possible,omitempty"`
	AcceptedAt        int64  `json:"accepted_at,omitempty"`
	ResponseID        string `json:"response_id,omitempty"`
	RespondedAt       int64  `json:"responded_at,omitempty"`
	BlockedReason     string `json:"blocked_reason,omitempty"`
	Availability      string `json:"availability,omitempty"` // derived, never persisted
}

// Request atomically publishes and records a named request. Reusing a task is
// rejected with its existing ID: retries must inspect it, not create new work.
// ttl is an action deadline; metadata is kept until explicit board removal.
func (b *Bus) Request(ctx context.Context, task, from, target, ref, body string, ttl time.Duration) (string, error) {
	if !ValidName(task) || !ValidName(from) || !ValidName(target) {
		return "", fmt.Errorf("invalid task, sender or target")
	}
	if ttl < 0 || (ttl > 0 && ttl < time.Millisecond) {
		return "", fmt.Errorf("ttl must be zero or at least 1ms")
	}
	if err := ValidateText(body, CmdMaxRunes); err != nil {
		return "", err
	}
	return publishRequest.Run(ctx, b.r, []string{BoardKey(b.project), StreamKey(b.project, "cmd")}, task, from, target, ref, body, ttl.Milliseconds(), streamMaxLen).Text()
}

var publishRequest = redis.NewScript(`
local raw=redis.call('HGET',KEYS[1],ARGV[1])
if raw then
 local e=cjson.decode(raw)
 local id='';if e.request then id=e.request.id end
 return redis.error_reply('board task already exists: '..ARGV[1]..' request='..id)
end
local t=redis.call('TIME');local now=t[1]*1000+math.floor(t[2]/1000)
local expires=0;if tonumber(ARGV[6])>0 then expires=now+tonumber(ARGV[6]) end
local id=redis.call('XADD',KEYS[2],'MAXLEN','~',ARGV[7],'*','from',ARGV[2],'target',ARGV[3],'type','directive','ref',ARGV[4],'command',ARGV[5],'task',ARGV[1],'expires_at',expires,'text_complete','yes')
local thread=ARGV[4];if thread=='' then thread=id end
local e={owner=ARGV[3],state='requested',updated=math.floor(now/1000),request={id=id,thread=thread,from=ARGV[2],target=ARGV[3],created_at=now,expires_at=expires,delivery='queued'}}
redis.call('HSET',KEYS[1],ARGV[1],cjson.encode(e))
return id`)

func (b *Bus) AcceptRequest(ctx context.Context, task, agent string) error {
	_, err := b.transitionRequest(ctx, task, agent, "accept", "")
	return err
}
func (b *Bus) BlockRequest(ctx context.Context, task, agent, reason string) error {
	if reason == "" {
		return fmt.Errorf("block reason required")
	}
	_, err := b.transitionRequest(ctx, task, agent, "block", reason)
	return err
}

// CompleteRequest publishes a response into the existing thread and marks done
// atomically. Acceptance is required; a generic reply alone never marks done.
func (b *Bus) CompleteRequest(ctx context.Context, task, agent, body string) (string, error) {
	if body == "" {
		return "", fmt.Errorf("completion response required")
	}
	return b.transitionRequest(ctx, task, agent, "done", body)
}
func (b *Bus) transitionRequest(ctx context.Context, task, agent, op, body string) (string, error) {
	if !ValidName(task) || !ValidName(agent) {
		return "", fmt.Errorf("invalid task or agent")
	}
	if err := ValidateText(body, CmdMaxRunes); err != nil {
		return "", err
	}
	return requestTransition.Run(ctx, b.r, []string{BoardKey(b.project), StreamKey(b.project, "cmd")}, task, agent, op, body, streamMaxLen).Text()
}

var requestTransition = redis.NewScript(`
local raw=redis.call('HGET',KEYS[1],ARGV[1]);if not raw then return redis.error_reply('unknown request') end
local e=cjson.decode(raw);local r=e.request
if not r then return redis.error_reply('board task is not a tracked request') end
if r.target~=ARGV[2] then return redis.error_reply('only the target agent may accept/block/complete this request') end
if e.state=='done' then
 if ARGV[3]=='done' then return r.response_id end
 return redis.error_reply('request already completed')
end
local t=redis.call('TIME');local now=t[1]*1000+math.floor(t[2]/1000)
local result=''
if ARGV[3]=='accept' then
 if not r.accepted_at then
  if r.expires_at and r.expires_at>0 and r.expires_at<=now then return redis.error_reply('request expired; cannot accept') end
  if #redis.call('XRANGE',KEYS[2],r.id,r.id)==0 then return redis.error_reply('request body missing; cannot accept') end
  r.accepted_at=now
 end
 e.state='accepted';r.blocked_reason=nil
elseif ARGV[3]=='block' then
 e.state='blocked';r.blocked_reason=ARGV[4]
else
 if not r.accepted_at then return redis.error_reply('request must be explicitly accepted before completion') end
 if e.state=='blocked' then return redis.error_reply('blocked request: accept to resume before completion') end
 result=redis.call('XADD',KEYS[2],'MAXLEN','~',ARGV[5],'*','from',ARGV[2],'target',r.from,'type','reply','ref',r.thread,'command',ARGV[4],'text_complete','yes')
 r.response_id=result;r.responded_at=now;e.state='done'
end
e.updated=math.floor(now/1000)
redis.call('HSET',KEYS[1],ARGV[1],cjson.encode(e))
return result`)

func (b *Bus) requestAvailability(ctx context.Context, entries map[string]BoardEntry) error {
	pipe := b.r.Pipeline()
	checks := make(map[string]*redis.XMessageSliceCmd)
	for task, e := range entries {
		if e.Request != nil {
			checks[task] = pipe.XRangeN(ctx, StreamKey(b.project, "cmd"), e.Request.ID, e.Request.ID, 1)
		}
	}
	if len(checks) == 0 {
		return nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	serverTime, err := b.r.Time(ctx).Result()
	if err != nil {
		return err
	}
	now := serverTime.UnixMilli()
	for task, check := range checks {
		r := entries[task].Request
		r.Availability = "retained"
		if len(check.Val()) == 0 {
			r.Availability = "missing"
		} else if r.ExpiresAt > 0 && now >= r.ExpiresAt {
			r.Availability = "expired"
		}
	}
	return nil
}
