package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/netbja/agent-bus-monitor/config"
	"strconv"
	"strings"
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
	Commit            string `json:"commit,omitempty"`      // request done --commit (missions)
	Branch            string `json:"branch,omitempty"`      // request done --branch, request review --branch
	ReviewNote        string `json:"review_note,omitempty"` // request review's note (wip-limit §3.2)
	// ReviewBase is the base tip request review read (board-drift §3.2): a
	// head already in it is never judged merged. Absent: not judged.
	ReviewBase string `json:"review_base,omitempty"`
	// ReviewMore: request review --more, a step that is not the last (§3.4).
	ReviewMore bool `json:"review_more,omitempty"`
	// ReviewN counts the request's reviews: the merge queue's write applies
	// only to the review it read (reviewer-codex).
	ReviewN int `json:"review_n,omitempty"`
	// MergedSteps are the heads of the --more steps merged so far (§3.4).
	MergedSteps []string `json:"merged_steps,omitempty"`
	// ClosedOnMerge: the dispatcher closed it, its branch's head delivered
	// into the base (§3.3), with no reply.
	ClosedOnMerge *MergeClose `json:"closed_on_merge,omitempty"`
	Availability  string      `json:"availability,omitempty"` // derived, never persisted
	// ClosedUnaccepted says why a request was closed without an accept:
	// "body missing" (cmd retention removed it) or "expired" (request-lost-body).
	ClosedUnaccepted string `json:"closed_unaccepted,omitempty"`
	// Priority (request-priority, additive): "high" or "low", absent = normal;
	// set only by a person through SetRequestPriority, kept by every board
	// script's cjson round trip.
	Priority   string `json:"priority,omitempty"`
	PriorityBy string `json:"priority_by,omitempty"` // the person who set it
	PriorityAt int64  `json:"priority_at,omitempty"` // Redis TIME, Unix ms
	// RetroOf (self-improving loop, additive): on a retro's request, the task
	// it is the retro of. A retro is known by this field, never by its name.
	RetroOf string `json:"retro_of,omitempty"`
}

// ErrTaskExists: the board already holds a task of that name (Request).
var ErrTaskExists = errors.New("board task already exists")

// taskExists is the script's refusal, its message unchanged (it names the
// existing request's id), marked as ErrTaskExists.
type taskExists struct{ msg string }

func (e taskExists) Error() string        { return e.msg }
func (e taskExists) Is(target error) bool { return target == ErrTaskExists }

// ErrRunDraining refuses a new task to an agent while the run drains
// (run-budget §3).
var ErrRunDraining = errors.New("run is draining: no new task (agentbus run)")

// Request atomically publishes and records a named request. Reusing a task is
// rejected with its existing ID: retries must inspect it, not create new work.
// ttl is an action deadline; metadata is kept until explicit board removal.
// While the run drains (run-budget §3) it is refused (ErrRunDraining): only
// the person sends a new task then (RequestBy).
func (b *Bus) Request(ctx context.Context, task, from, target, ref, body string, ttl time.Duration) (string, error) {
	return b.RequestBy(ctx, task, from, target, ref, body, ttl, false)
}

// RequestBy is Request; person is true for the person's own request (a caller
// with no AGENT_BUS_AGENT, as deliver recognises them: defence in depth, not
// a boundary), which a draining run does not refuse.
func (b *Bus) RequestBy(ctx context.Context, task, from, target, ref, body string, ttl time.Duration, person bool) (string, error) {
	return b.requestBy(ctx, task, from, target, ref, body, ttl, person, "")
}

// requestBy is RequestBy; retroOf, when not "", marks the request as that
// task's retro (RequestInfo.RetroOf).
func (b *Bus) requestBy(ctx context.Context, task, from, target, ref, body string, ttl time.Duration, person bool, retroOf string) (string, error) {
	if !ValidName(task) || !ValidName(from) || !ValidName(target) {
		return "", fmt.Errorf("invalid task, sender or target")
	}
	if ttl < 0 || (ttl > 0 && ttl < time.Millisecond) {
		return "", fmt.Errorf("ttl must be zero or at least 1ms")
	}
	if err := ValidateText(body, CmdMaxRunes); err != nil {
		return "", err
	}
	// The run and the plan are read here, not in the script (ACL answer a,
	// broker-hardening step 4): any agent sends a request, and a script's
	// declared keys need write, which an agent has on neither. The drain
	// check is defence in depth, so a read just before the script is enough;
	// a run that starts draining in between lets one request through.
	if !person { // the person's own request is never refused for a drain
		if state, err := b.r.HGet(ctx, RunKey(b.project), "state").Result(); err != nil && !errors.Is(err, redis.Nil) {
			return "", err
		} else if state == RunDraining {
			return "", ErrRunDraining
		}
	}
	prio, by, err := b.planPriorityOf(ctx, task)
	if err != nil {
		return "", err
	}
	id, err := publishRequest.Run(ctx, b.r, []string{BoardKey(b.project), StreamKey(b.project, "cmd"), SwapKey(b.project, target)},
		task, from, target, ref, body, ttl.Milliseconds(), streamMaxLen, prio, by, retroOf).Text()
	if err != nil && strings.Contains(err.Error(), "RUNDRAINING") {
		return "", ErrRunDraining
	}
	if err != nil && strings.HasPrefix(err.Error(), ErrTaskExists.Error()+":") {
		return "", taskExists{msg: err.Error()}
	}
	return id, swapping(err, target)
}

// planPriorityOf is the priority a request for task starts at (views-redesign
// §4, step 4b): the plan task is the one whose board field names task, else
// the one with its id and no board field; its level is the person's (the
// plan facts, normal included), else the plan file's. No plan, or one that
// does not decode, is normal ("", ""); only a failed read is an error.
func (b *Bus) planPriorityOf(ctx context.Context, task string) (level, by string, err error) {
	raw, err := b.r.Get(ctx, PlanKey(b.project)).Result()
	if errors.Is(err, redis.Nil) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	var rec struct {
		File string `json:"file"`
	}
	var file struct {
		Tasks []struct {
			ID       *string `json:"id"`
			Board    string  `json:"board"`
			Priority string  `json:"priority"`
		} `json:"tasks"`
	}
	if json.Unmarshal([]byte(raw), &rec) != nil || json.Unmarshal([]byte(rec.File), &file) != nil {
		return "", "", nil
	}
	found := -1
	for i, t := range file.Tasks {
		if t.Board == task {
			found = i
			break
		}
	}
	if found < 0 {
		for i, t := range file.Tasks {
			if t.ID != nil && *t.ID == task && t.Board == "" {
				found = i
				break
			}
		}
	}
	if found < 0 || file.Tasks[found].ID == nil {
		return "", "", nil
	}
	t := file.Tasks[found]
	fraw, err := b.r.HGet(ctx, PlanFactsKey(b.project), *t.ID).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", "", err
	}
	var f PlanFact
	if err == nil && json.Unmarshal([]byte(fraw), &f) == nil {
		switch f.Priority {
		case PriorityHigh, PriorityLow:
			return f.Priority, f.PriorityBy, nil
		case PriorityNormal:
			return "", "", nil
		}
	}
	if t.Priority == PriorityHigh || t.Priority == PriorityLow {
		return t.Priority, "", nil
	}
	return "", "", nil
}

// publishRequest records a request: KEYS board, cmd, the target's swap;
// ARGV task, from, target, ref, body, ttl ms, maxlen, then the priority and
// who set it (planPriorityOf, read by the caller), the task a retro is of.
var publishRequest = redis.NewScript(`
if redis.call('EXISTS',KEYS[3])==1 then return redis.error_reply('SWAPPING') end
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
local prio,by=ARGV[8],ARGV[9]
if prio=='high' or prio=='low' then e.request.priority=prio;e.request.priority_at=now;if by~='' then e.request.priority_by=by end end
if ARGV[10]~='' then e.request.retro_of=ARGV[10] end
redis.call('HSET',KEYS[1],ARGV[1],cjson.encode(e))
return id`)

func (b *Bus) AcceptRequest(ctx context.Context, task, agent string) error {
	return b.AcceptRequestLimit(ctx, task, agent, 0)
}

// AcceptRequestLimit is AcceptRequest under a work-in-progress limit
// (wip-limit §3.3, board.wip_limit): a first accept is refused while agent
// holds limit or more other requests accepted, unless the person set this
// one high (priority high with priority_by). A resume (a request accepted
// before, back from review or blocked) is never refused, so resumes can
// take an agent above the limit; a first accept then waits until it is
// under again. 0 is no limit. The count is read in the script that writes.
func (b *Bus) AcceptRequestLimit(ctx context.Context, task, agent string, limit int) error {
	if limit < 0 {
		return fmt.Errorf("wip limit must be 0 or more")
	}
	_, err := b.transitionRequest(ctx, task, agent, "accept", "", "", "", strconv.Itoa(limit))
	return err
}

// ReviewRequest marks an accepted request review (wip-limit §3.2): a step
// or a PR waits on its reviewers, so it is not in flight. By its target
// only, from accepted only; it records branch and note when given and
// writes no stream entry (the review's own challenge tells the reviewers).
// accept resumes it, done and block pass from it.
func (b *Bus) ReviewRequest(ctx context.Context, task, agent, branch, note string) error {
	return b.ReviewRequestOpts(ctx, task, agent, ReviewOpts{Branch: branch, Note: note})
}

// MergeClose is how a request closed on merge: the head and the base.
type MergeClose struct {
	Head string `json:"head"`
	Base string `json:"base"`
}

// ReviewOpts are request review's facts (board-drift §3.2, §3.4): the
// branch, the head reviewed (Commit) and the base tip read (ReviewBase), and
// More for a step that is not the last. Each review records its own: a field
// not given is cleared, never the last review's.
type ReviewOpts struct {
	Branch, Note, Commit, ReviewBase string
	More                             bool
}

// ReviewRequestOpts is ReviewRequest with the close-on-merge facts.
func (b *Bus) ReviewRequestOpts(ctx context.Context, task, agent string, o ReviewOpts) error {
	if o.Branch != "" && !config.ValidBranchName(o.Branch) {
		return fmt.Errorf("invalid branch %q", o.Branch)
	}
	for _, h := range []string{o.Commit, o.ReviewBase} {
		if h != "" && !ValidHead(h) {
			return fmt.Errorf("invalid commit %q", h)
		}
	}
	more := ""
	if o.More {
		more = "1"
	}
	_, err := b.transitionRequest(ctx, task, agent, "review", o.Note, o.Commit, o.Branch, "0", o.ReviewBase, more)
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
	return b.CompleteRequestAt(ctx, task, agent, body, "", "")
}

// CompleteRequestAt is CompleteRequest that also records the delivered commit
// and branch (missions). A repeated done keeps the first commit and branch.
func (b *Bus) CompleteRequestAt(ctx context.Context, task, agent, body, commit, branch string) (string, error) {
	if body == "" {
		return "", fmt.Errorf("completion response required")
	}
	return b.transitionRequest(ctx, task, agent, "done", body, commit, branch)
}
func (b *Bus) transitionRequest(ctx context.Context, task, agent, op, body string, extra ...string) (string, error) {
	commit, branch, limit, base, more := "", "", "0", "", ""
	if len(extra) >= 2 {
		commit, branch = extra[0], extra[1]
	}
	if len(extra) >= 3 {
		limit = extra[2]
	}
	if len(extra) == 5 { // review: the base tip read, --more
		base, more = extra[3], extra[4]
	}
	if !ValidName(task) || !ValidName(agent) {
		return "", fmt.Errorf("invalid task or agent")
	}
	if err := ValidateText(body, CmdMaxRunes); err != nil {
		return "", err
	}
	out, err := requestTransition.Run(ctx, b.r, []string{BoardKey(b.project), StreamKey(b.project, "cmd")}, task, agent, op, body, streamMaxLen, commit, branch, limit, base, more).Text()
	return out, requestError(err)
}

// ErrRequestRefused: the request's own state refuses the transition (unknown,
// not a tracked request, another target, done, expired, its body gone, not
// accepted, blocked). Retrying changes nothing; any other error may heal.
var ErrRequestRefused = errors.New("request refused")

// requestRefusals are the request script's own error replies.
var requestRefusals = []string{"unknown request", "board task is not a tracked request", "only the target agent may",
	"request already completed", "request expired", "request body missing", "request must be explicitly accepted", "blocked request",
	"wip limit reached", "only an accepted request goes to review"}

// refusal is a script refusal, its message unchanged.
type refusal struct{ msg string }

func (r refusal) Error() string        { return r.msg }
func (r refusal) Is(target error) bool { return target == ErrRequestRefused }

// requestError marks the script's own refusals as ErrRequestRefused.
func requestError(err error) error {
	if err == nil {
		return nil
	}
	for _, p := range requestRefusals {
		if strings.HasPrefix(err.Error(), p) {
			return refusal{msg: err.Error()}
		}
	}
	return err
}

var requestTransition = redis.NewScript(requestTransitionSrc)

// requestTransitionSrc is the script's source: its error_reply literals are
// the refusals requestError marks (a test holds the list to them).
const requestTransitionSrc = `
local raw=redis.call('HGET',KEYS[1],ARGV[1]);if not raw then return redis.error_reply('unknown request') end
local e=cjson.decode(raw);local r=e.request
if not r then return redis.error_reply('board task is not a tracked request') end
local bysender=false
if r.target~=ARGV[2] then
 if ARGV[3]~='done' or r.from~=ARGV[2] or r.accepted_at then return redis.error_reply('only the target agent may accept/block/complete this request') end
 bysender=true
end
if e.state=='done' then
 if ARGV[3]=='done' then return r.response_id or '' end
 return redis.error_reply('request already completed')
end
local t=redis.call('TIME');local now=t[1]*1000+math.floor(t[2]/1000)
local result=''
if ARGV[3]=='accept' then
 if not r.accepted_at then
  if r.expires_at and r.expires_at>0 and r.expires_at<=now then return redis.error_reply('request expired; cannot accept (its target or sender closes it: agentbus request done <task> <why>)') end
  if #redis.call('XRANGE',KEYS[2],r.id,r.id)==0 then return redis.error_reply('request body missing; cannot accept (its target or sender closes it: agentbus request done <task> <why>)') end
  local lim=tonumber(ARGV[8]) or 0
  if lim>0 and not (r.priority=='high' and r.priority_by and r.priority_by~='') then
   local held={}
   local all=redis.call('HGETALL',KEYS[1])
   for i=1,#all,2 do
    local ok,o=pcall(cjson.decode,all[i+1])
    if ok and type(o)=='table' and o.state=='accepted' and type(o.request)=='table' and o.request.target==ARGV[2] then held[#held+1]=all[i] end
   end
   if #held>=lim then
    table.sort(held)
    return redis.error_reply('wip limit reached for '..ARGV[2]..' ('..#held..' of '..lim..' accepted: '..table.concat(held,', ')..'): finish one, or mark it review (agentbus request review <task>) or blocked; then accept '..ARGV[1])
   end
  end
  r.accepted_at=now
 end
 e.state='accepted';r.blocked_reason=nil
elseif ARGV[3]=='review' then
 if e.state~='accepted' then return redis.error_reply('only an accepted request goes to review') end
 e.state='review'
 if ARGV[7]~='' then r.branch=ARGV[7] end
 if ARGV[4]~='' then r.review_note=ARGV[4] end
 r.commit=nil;r.review_base=nil;r.review_more=nil;r.review_n=(tonumber(r.review_n) or 0)+1
 if ARGV[6]~='' then r.commit=ARGV[6] end
 if ARGV[9]~='' then r.review_base=ARGV[9] end
 if ARGV[10]=='1' then r.review_more=true end
elseif ARGV[3]=='block' then
 if e.state~='blocked' or r.blocked_reason~=ARGV[4] then
  redis.call('XADD',KEYS[2],'MAXLEN','~',ARGV[5],'*','from',ARGV[2],'target',r.from,'type','blocked','ref',r.thread,'command',ARGV[4],'task',ARGV[1],'text_complete','yes')
 end
 e.state='blocked';r.blocked_reason=ARGV[4]
else
 if not r.accepted_at then
  local why=''
  if #redis.call('XRANGE',KEYS[2],r.id,r.id)==0 then why='body missing'
  elseif r.expires_at and r.expires_at>0 and r.expires_at<=now then why='expired' end
  if why=='' then return redis.error_reply('request must be explicitly accepted before completion') end
  r.closed_unaccepted=why
 elseif e.state=='blocked' then return redis.error_reply('blocked request: accept to resume before completion') end
 local to=r.from
 if bysender then to=r.target end
 result=redis.call('XADD',KEYS[2],'MAXLEN','~',ARGV[5],'*','from',ARGV[2],'target',to,'type','reply','ref',r.thread,'command',ARGV[4],'text_complete','yes')
 r.response_id=result;r.responded_at=now;e.state='done'
 if ARGV[6]~='' then r.commit=ARGV[6] end
 if ARGV[7]~='' then r.branch=ARGV[7] end
end
e.updated=math.floor(now/1000)
redis.call('HSET',KEYS[1],ARGV[1],cjson.encode(e))
return result`

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
