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
	// WorkBranch is the branch the target works on, declared at request
	// accept --branch or by request branch (code-intel §7): one owner per
	// branch among open requests. Review's Branch is untouched.
	WorkBranch string `json:"work_branch,omitempty"`
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
	// OpenSteps (sibling-enforce §3): every --more step reviewed and not yet
	// judged merged or abandoned; without Parallel, at most one.
	OpenSteps []OpenStep `json:"open_steps,omitempty"`
	// Parallel: the sender's or the person's exception (sibling-enforce §3).
	Parallel *ParallelMark `json:"parallel,omitempty"`
	// AbandonedSteps: open steps closed unmerged by the sender or the person.
	AbandonedSteps []AbandonedStep `json:"abandoned_steps,omitempty"`
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
	// ReportedAt and ReportID: the last report that named this request
	// (report --task, board-drift §4), the note transition's, which changes
	// no state; busmon measures "no reply" from it when it is newer than the
	// accept (§7).
	ReportedAt int64  `json:"reported_at,omitempty"`
	ReportID   string `json:"report_id,omitempty"`
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
	return b.requestBy(ctx, task, from, target, ref, body, ttl, person, "", false)
}

// retroExists: the publish script found a retro of the same task (an open
// one, or any with openOnly false); nothing was written.
type retroExists struct{ name, owner string }

func (e retroExists) Error() string { return "a retro exists: " + e.name + " for " + e.owner }

// requestBy is RequestBy; retroOf, when not "", marks the request as that
// task's retro (RequestInfo.RetroOf), and the same script refuses it when the
// board holds a retro of that task already (open ones only with openOnly):
// the check and the write are one, so concurrent sends make one retro.
func (b *Bus) requestBy(ctx context.Context, task, from, target, ref, body string, ttl time.Duration, person bool, retroOf string, openOnly bool) (string, error) {
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
		task, from, target, ref, body, ttl.Milliseconds(), streamMaxLen, prio, by, retroOf, openOnly).Text()
	if err != nil && strings.Contains(err.Error(), "RUNDRAINING") {
		return "", ErrRunDraining
	}
	if err != nil && strings.HasPrefix(err.Error(), "RETROEXISTS ") {
		f := strings.Fields(err.Error())
		if len(f) == 3 {
			return "", retroExists{name: f[1], owner: f[2]}
		}
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
// who set it (planPriorityOf, read by the caller), the task a retro is of
// and whether only an open retro of it refuses this one ('1').
var publishRequest = redis.NewScript(`
if redis.call('EXISTS',KEYS[3])==1 then return redis.error_reply('SWAPPING') end
if ARGV[10]~='' then
 local all=redis.call('HGETALL',KEYS[1])
 for i=1,#all,2 do
  local ok,o=pcall(cjson.decode,all[i+1])
  if ok and type(o)=='table' and type(o.request)=='table' and o.request.retro_of==ARGV[10] and (ARGV[11]~='1' or o.state~='done') then
   return redis.error_reply('RETROEXISTS '..all[i]..' '..tostring(o.request.target))
  end
 end
end
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
	return b.AcceptRequestBranch(ctx, task, agent, limit, "")
}

// AcceptRequestBranch is AcceptRequestLimit that also declares the work
// branch (code-intel §7), "" none (a work branch declared before is kept).
// A branch another agent's open request (accepted, review or blocked)
// declares, as its work branch or its review branch, refuses the whole
// accept, in the same script that reads the board.
func (b *Bus) AcceptRequestBranch(ctx context.Context, task, agent string, limit int, branch string) error {
	if limit < 0 {
		return fmt.Errorf("wip limit must be 0 or more")
	}
	if branch != "" && !config.ValidBranchName(branch) {
		return fmt.Errorf("invalid branch %q", branch)
	}
	_, err := b.transitionRequest(ctx, task, agent, "accept", "", "", branch, strconv.Itoa(limit))
	return err
}

// SetWorkBranch is request branch (code-intel §7): the target records its
// work branch later, while the request is accepted, under AcceptRequestBranch's
// one-owner rule.
func (b *Bus) SetWorkBranch(ctx context.Context, task, agent, branch string) error {
	if !config.ValidBranchName(branch) {
		return fmt.Errorf("invalid branch %q", branch)
	}
	_, err := b.transitionRequest(ctx, task, agent, "branch", "", "", branch)
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
	// Steps: the open steps the CLI judged merged (sibling-enforce §3.1).
	Steps StepsJudged
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
	judged, n, err := o.Steps.args()
	if err != nil {
		return err
	}
	_, err = b.transitionRequest(ctx, task, agent, "review", o.Note, o.Commit, o.Branch, "0", o.ReviewBase, more, judged, n)
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
	return b.CompleteRequestSteps(ctx, task, agent, body, commit, branch, StepsJudged{})
}

// CompleteRequestSteps is CompleteRequestAt with the open steps the CLI
// judged merged: the target's done is refused while one stays listed
// (sibling-enforce §3.1).
func (b *Bus) CompleteRequestSteps(ctx context.Context, task, agent, body, commit, branch string, steps StepsJudged) (string, error) {
	if body == "" {
		return "", fmt.Errorf("completion response required")
	}
	judged, n, err := steps.args()
	if err != nil {
		return "", err
	}
	return b.transitionRequest(ctx, task, agent, "done", body, commit, branch, "0", "", "", judged, n)
}

// CheckReportTask is report --task's check, before anything is published
// (board-drift §4): the note's refusals (an unknown task, one that is not a
// tracked request, a request whose target is not agent, a done one) and,
// with done (report --done), done's too (not accepted, blocked). It writes
// nothing.
func (b *Bus) CheckReportTask(ctx context.Context, task, agent string, done bool) error {
	return b.note(ctx, task, agent, "", done)
}

// NoteRequest is the note transition (board-drift §4): request.reported_at
// and request.report_id on task, with no state change, no stream entry and
// no Updated change (the last transition stays the Board's CHANGED). Its
// refusals are CheckReportTask's without done: never the WIP limit, an
// expired request or a lost body, since a note changes no state.
func (b *Bus) NoteRequest(ctx context.Context, task, agent, reportID string) error {
	if !streamIDPattern.MatchString(reportID) {
		return fmt.Errorf("invalid report id %q", reportID)
	}
	return b.note(ctx, task, agent, reportID, false)
}

// note runs the script's note: reportID "" checks only.
func (b *Bus) note(ctx context.Context, task, agent, reportID string, done bool) error {
	if !ValidName(task) || !ValidName(agent) {
		return fmt.Errorf("invalid task or agent")
	}
	d := ""
	if done {
		d = "1"
	}
	_, err := requestTransition.Run(ctx, b.r, []string{BoardKey(b.project), StreamKey(b.project, "cmd")}, task, agent, "note", reportID, streamMaxLen, "", "", "0", "", "", d).Text()
	return requestError(err)
}

func (b *Bus) transitionRequest(ctx context.Context, task, agent, op, body string, extra ...string) (string, error) {
	commit, branch, limit, base, more := "", "", "0", "", ""
	if len(extra) >= 2 {
		commit, branch = extra[0], extra[1]
	}
	if len(extra) >= 3 {
		limit = extra[2]
	}
	if len(extra) >= 5 { // review: the base tip read, --more
		base, more = extra[3], extra[4]
	}
	judged, readN := "", ""
	if len(extra) == 7 { // review and done: the open steps judged, the review_n read
		judged, readN = extra[5], extra[6]
	}
	if !ValidName(task) || !ValidName(agent) {
		return "", fmt.Errorf("invalid task or agent")
	}
	if err := ValidateText(body, CmdMaxRunes); err != nil {
		return "", err
	}
	out, err := requestTransition.Run(ctx, b.r, []string{BoardKey(b.project), StreamKey(b.project, "cmd")}, task, agent, op, body, streamMaxLen, commit, branch, limit, base, more, "", judged, readN).Text()
	return out, requestError(err)
}

// ErrRequestRefused: the request's own state refuses the transition (unknown,
// not a tracked request, another target, done, expired, its body gone, not
// accepted, blocked). Retrying changes nothing; any other error may heal.
var ErrRequestRefused = errors.New("request refused")

// requestRefusals are the request script's own error replies.
var requestRefusals = []string{"unknown request", "board task is not a tracked request", "only the target agent may",
	"request already completed", "request expired", "request body missing", "request must be explicitly accepted", "blocked request",
	"wip limit reached", "only an accepted request goes to review",
	// sibling-enforce §3: the open steps' refusals, and parallel's and abandon's.
	"step ", "a step is open on ", "request changed since its steps were judged",
	"only the request's sender or the person may", "no such open step",
	// code-intel §7: one owner per work branch.
	"branch ", "only an accepted request records a work branch"}

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
local function steps_rest(r)
 local open=r.open_steps;if type(open)~='table' then open={} end
 local judged={};local jr=ARGV[12] or ''
 if jr~='' then judged=cjson.decode(jr) end
 if #judged>0 and (tonumber(r.review_n) or 0)~=tonumber(ARGV[13]) then return 'request changed since its steps were judged: run it again' end
 local js={};for _,j in ipairs(judged) do js[j.branch]=true end
 local rest={};for _,s in ipairs(open) do if not js[s.branch] then rest[#rest+1]=s end end
 return rest,judged,open
end
local function steps_apply(r,judged,rest,open)
 local listed={};for _,s in ipairs(open) do listed[s.branch]=true end
 local ms=r.merged_steps;if type(ms)~='table' then ms={} end
 local have={};for _,h in ipairs(ms) do have[h]=true end
 for _,j in ipairs(judged) do
  if listed[j.branch] and not have[j.head] then ms[#ms+1]=j.head;have[j.head]=true end
 end
 if #ms>0 then r.merged_steps=ms end
 if #rest>0 then r.open_steps=rest else r.open_steps=nil end
end
local function branch_owner(b)
 local all=redis.call('HGETALL',KEYS[1])
 for i=1,#all,2 do
  local ok,o=pcall(cjson.decode,all[i+1])
  if ok and type(o)=='table' and type(o.request)=='table' and o.request.target~=ARGV[2] and (o.state=='accepted' or o.state=='review' or o.state=='blocked') and (o.request.work_branch==b or o.request.branch==b) then return o.request.target.."'s request "..all[i] end
 end
 return nil
end
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
if ARGV[3]=='note' then
 if ARGV[11]=='1' then
  if not r.accepted_at then return redis.error_reply('request must be explicitly accepted before completion') end
  if e.state=='blocked' then return redis.error_reply('blocked request: accept to resume before completion') end
 end
 if ARGV[4]=='' then return '' end
 r.reported_at=now;r.report_id=ARGV[4]
 redis.call('HSET',KEYS[1],ARGV[1],cjson.encode(e))
 return ''
end
if ARGV[3]=='branch' then
 if e.state~='accepted' then return redis.error_reply('only an accepted request records a work branch') end
 local owner=branch_owner(ARGV[7])
 if owner then return redis.error_reply('branch '..ARGV[7]..' belongs to '..owner) end
 r.work_branch=ARGV[7];e.updated=math.floor(now/1000)
 redis.call('HSET',KEYS[1],ARGV[1],cjson.encode(e))
 return ''
end
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
 if ARGV[7]~='' then
  local owner=branch_owner(ARGV[7])
  if owner then return redis.error_reply('branch '..ARGV[7]..' belongs to '..owner) end
  r.work_branch=ARGV[7]
 end
 e.state='accepted';r.blocked_reason=nil
elseif ARGV[3]=='review' then
 if e.state~='accepted' then return redis.error_reply('only an accepted request goes to review') end
 local rest,judged,open=steps_rest(r)
 if type(rest)=='string' then return redis.error_reply(rest) end
 if #rest>0 and ARGV[7]=='' then
  local s=rest[1].branch
  return redis.error_reply('a step is open on '..s..': name the branch (--branch '..s..' for '..s..' itself)')
 end
 if type(r.parallel)~='table' then
  for _,s in ipairs(rest) do
   if s.branch~=ARGV[7] then return redis.error_reply('step '..s.branch..' of '..ARGV[1]..' is not merged yet: sibling steps run in series (merge-conflicts §3); take another task, or ask '..(r.from or '')..' for agentbus request parallel '..ARGV[1]) end
  end
 end
 steps_apply(r,judged,rest,open)
 e.state='review'
 if ARGV[7]~='' then r.branch=ARGV[7] end
 if ARGV[4]~='' then r.review_note=ARGV[4] end
 r.commit=nil;r.review_base=nil;r.review_more=nil;r.review_n=(tonumber(r.review_n) or 0)+1
 if ARGV[6]~='' then r.commit=ARGV[6] end
 if ARGV[9]~='' then r.review_base=ARGV[9] end
 if ARGV[10]=='1' then r.review_more=true end
 if r.branch then
  local steps=r.open_steps;if type(steps)~='table' then steps={} end
  local kept={}
  for _,s in ipairs(steps) do if s.branch~=r.branch then kept[#kept+1]=s end end
  if ARGV[10]=='1' and ARGV[9]~='' then kept[#kept+1]={branch=r.branch,commit=r.commit} end
  if #kept>0 then r.open_steps=kept else r.open_steps=nil end
 end
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
 if not bysender then
  local rest,judged,open=steps_rest(r)
  if type(rest)=='string' then return redis.error_reply(rest) end
  if #rest>0 then return redis.error_reply('step '..rest[1].branch..' of '..ARGV[1]..' is not merged yet: done once it merges, or ask '..(r.from or '')..' to abandon it') end
  steps_apply(r,judged,rest,open)
 end
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
