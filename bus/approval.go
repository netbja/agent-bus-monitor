package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Approvals (v0.17, spec docs/superpowers/specs/2026-10-17-role-tool-rules-design.md
// §5): a person's agreement to pop a role marked approval = "person". It is an
// ask with one fixed question, spawn-<role>, kept outside the one-open-ask
// rule: the field approval:<agent>:<role> of {p}:asks holds its id. Spending it
// writes <id>|u|spawn-<role> (spawn:<ms> or swap:<ms>); a swap's hand-over to
// agent-spawn writes <id>|s|spawn-<role>. A procedure gate, not a security
// boundary: Redis cannot tell busmon from an agent.

const (
	ApprovalAnswer     = "Approve"
	ApprovalRefuse     = "Refuse"
	ApprovalSwapWindow = 3 * time.Minute
	DefaultApprovalTTL = 30 * time.Minute
)

// ApprovalQuestion is the question id of role's approval.
func ApprovalQuestion(role string) string { return "spawn-" + role }

// ApprovalMode says what ApprovalUse does with a usable approval.
type ApprovalMode string

const (
	SpendSpawn ApprovalMode = "spawn"   // agent-spawn spends it
	SpendSwap  ApprovalMode = "swap"    // swap spends it before it closes a pane
	CheckOnly  ApprovalMode = "check"   // read, spend nothing
	BySwap     ApprovalMode = "by-swap" // agent-spawn takes a swap's hand-over, once
)

// ErrNoApproval: no usable approval; the message says why.
var ErrNoApproval = errors.New("no usable approval")

// approvalCreate: KEYS asks, cmd. ARGV maxlen, from, title, def, qid, role,
// retention ms. Returns the new id; an open approval for the same role is
// replaced (nothing carried).
var approvalCreate = redis.NewScript(sweepLua + `
local now = nowms()
sweep(KEYS[1], now)
local idx = 'approval:' .. ARGV[2] .. ':' .. ARGV[6]
local old = redis.call('HGET', KEYS[1], idx)
local id = redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[1], '*', 'from', ARGV[2], 'target', 'people',
  'type', 'ask', 'command', ARGV[3], 'wake', 'no', 'text_complete', 'yes')
redis.call('HSET', KEYS[1], id, ARGV[4], id .. '|q', ARGV[5], id .. '|s', 'open', idx, id)
if old and redis.call('HGET', KEYS[1], old .. '|s') == 'open' then
  redis.call('HSET', KEYS[1], old .. '|s', 'replaced', old .. '|r', id, old .. '|c', '',
    old .. '|e', tostring(now + tonumber(ARGV[7])))
end
return id`)

// approvalUse: KEYS asks, agents. ARGV from, role, mode, swap id, ttl ms,
// swap window ms. Returns the ask id or an APPROVAL error reply.
var approvalUse = redis.NewScript(sweepLua + `
local now = nowms()
local function no(why) return redis.error_reply('APPROVAL ' .. why) end
local qid = 'spawn-' .. ARGV[2]
local mode = ARGV[3]
local id = redis.call('HGET', KEYS[1], 'approval:' .. ARGV[1] .. ':' .. ARGV[2])
if not id or not redis.call('HGET', KEYS[1], id .. '|s') then return no('no approval asked') end
local u = redis.call('HGET', KEYS[1], id .. '|u|' .. qid)
if mode == 'by-swap' then
  if id ~= ARGV[4] or not u or string.sub(u, 1, 5) ~= 'swap:' then return no('not a swap hand-over') end
  if now - tonumber(string.sub(u, 6)) > tonumber(ARGV[6]) then return no('expired') end
  if redis.call('HEXISTS', KEYS[1], id .. '|s|' .. qid) == 1 then return no('already used') end
  redis.call('HSET', KEYS[1], id .. '|s|' .. qid, tostring(now))
  return id
end
local raw = redis.call('HGET', KEYS[1], id .. '|a|' .. qid)
if not raw then return no('not answered') end
local a = cjson.decode(raw)
if a['withdrawn'] then return no('withdrawn') end
if a['choice'] ~= 'Approve' then return no('refused') end
if u then return no('already used') end
if now - tonumber(a['at']) > tonumber(ARGV[5]) then return no('expired') end
if redis.call('HEXISTS', KEYS[2], a['by']) == 1 then return no('answered by an agent') end
if mode == 'spawn' or mode == 'swap' then
  redis.call('HSET', KEYS[1], id .. '|u|' .. qid, mode .. ':' .. now)
end
return id`)

// ApprovalAsk asks a person to approve popping role for from's project. It
// returns the ask id; busmon shows it like any ask.
func (b *Bus) ApprovalAsk(ctx context.Context, from, role, reason string) (string, error) {
	if !ValidName(from) || !ValidName(role) {
		return "", fmt.Errorf("invalid agent or role name")
	}
	def := AskDef{From: from, Title: "Pop " + role + "?", Questions: []AskQuestion{{
		ID: ApprovalQuestion(role), Header: "Pop " + role, Question: "Pop " + role + " for " + b.project + "?",
		Context: strings.TrimSpace(reason),
		Options: []AskOption{{Label: ApprovalAnswer, Recommended: true}, {Label: ApprovalRefuse}},
	}}}
	if err := ValidateAskDef(def); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(def)
	id, err := approvalCreate.Run(ctx, b.r, []string{AsksKey(b.project), StreamKey(b.project, "cmd")},
		streamMaxLen, from, def.Title, string(raw), ApprovalQuestion(role), role, askRetention.Milliseconds()).Text()
	return id, askErr(err)
}

// ApprovalUse finds from's approval to pop role and, for SpendSpawn and
// SpendSwap, spends it; BySwap accepts a swap's spend of swapID once. It
// returns the ask id, or ErrNoApproval wrapped with the reason.
func (b *Bus) ApprovalUse(ctx context.Context, from, role string, mode ApprovalMode, swapID string, ttl time.Duration) (string, error) {
	if !ValidName(from) || !ValidName(role) {
		return "", fmt.Errorf("invalid agent or role name")
	}
	switch mode {
	case SpendSpawn, SpendSwap, CheckOnly, BySwap:
	default:
		return "", fmt.Errorf("unknown approval mode %q", mode)
	}
	id, err := approvalUse.Run(ctx, b.r, []string{AsksKey(b.project), AgentsKey(b.project)},
		from, role, string(mode), swapID, ttl.Milliseconds(), ApprovalSwapWindow.Milliseconds()).Text()
	if err != nil {
		if i := strings.Index(err.Error(), "APPROVAL "); i >= 0 {
			return "", fmt.Errorf("%w: %s", ErrNoApproval, err.Error()[i+len("APPROVAL "):])
		}
		return "", err
	}
	return id, nil
}
