package bus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// A subscriber lease is transport mutual exclusion, not agent liveness. The
// random token fences cleanup and ACK from an interrupted/expired subscriber.
const receiverLease = 30 * time.Second

// ReceiverKey holds only the upgraded transport fence; legacy Arm cannot overwrite it.
func ReceiverKey(project, agent string) string { return project + ":receiver:" + agent }

var renewReceiver = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
return redis.call('PEXPIRE', KEYS[1], ARGV[2])`)
var releaseReceiver = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`)

// ErrReceiverBusy means another subscriber held the agent's receiver lease for
// the whole bounded wait of a drain. Nothing was read, output or acknowledged.
var ErrReceiverBusy = errors.New("receiver busy: another subscriber holds the receiver lease")

// drainLeaseWait bounds DrainCmd's wait for a contended receiver lease. A
// variable only so tests can shorten it.
var drainLeaseWait = 5 * time.Second

// receiver is a held receiver lease plus its renewal goroutine. ctx is
// cancelled when the lease is lost or the holder closes it.
type receiver struct {
	b       *Bus
	lease   string
	token   string
	ctx     context.Context
	cancel  context.CancelFunc
	renewal chan error
}

// holdReceiver acquires the receiver lease with a SETNX loop and starts its
// renewal. wait <= 0 waits as long as ctx allows (contention is not an error
// wake for the one-shot subscribe); wait > 0 gives up with ErrReceiverBusy.
func (b *Bus) holdReceiver(ctx context.Context, agent, consumer string, wait time.Duration) (*receiver, error) {
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := consumer + ":" + hex.EncodeToString(tokenBytes)
	lease := ReceiverKey(b.project, agent)
	var giveUp time.Time
	if wait > 0 {
		giveUp = time.Now().Add(wait)
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ok, err := b.r.SetNX(ctx, lease, token, receiverLease).Result()
		if err != nil {
			return nil, err
		}
		if ok {
			break
		}
		if wait > 0 && !time.Now().Before(giveUp) {
			return nil, ErrReceiverBusy
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	rctx, cancel := context.WithCancel(ctx)
	rc := &receiver{b: b, lease: lease, token: token, ctx: rctx, cancel: cancel, renewal: make(chan error, 1)}
	go rc.renew()
	return rc, nil
}

func (rc *receiver) renew() {
	ticker := time.NewTicker(receiverLease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-rc.ctx.Done():
			return
		case <-ticker.C:
			n, e := renewReceiver.Run(rc.ctx, rc.b.r, []string{rc.lease}, rc.token, receiverLease.Milliseconds()).Int()
			if e != nil || n != 1 {
				if e == nil {
					e = fmt.Errorf("subscriber lease lost")
				}
				rc.renewal <- e
				rc.cancel()
				return
			}
		}
	}
}

// close stops renewal, then releases the lease only if this holder still owns it.
func (rc *receiver) close() {
	rc.cancel()
	c, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = releaseReceiver.Run(c, rc.b.r, []string{rc.lease}, rc.token).Err()
}

func (rc *receiver) leaseError(err error) error {
	select {
	case e := <-rc.renewal:
		return e
	default:
		if rc.ctx.Err() != nil {
			return rc.ctx.Err()
		}
		// Socket deadlines may fire just before the context timer publishes Err.
		// Preserve the one-shot idle outcome instead of returning an error wake.
		var timeout net.Error
		if deadline, ok := rc.ctx.Deadline(); ok && !time.Now().Before(deadline) && errors.As(err, &timeout) && timeout.Timeout() {
			return context.DeadlineExceeded
		}
		return err
	}
}

// transportErr marks a deliverOne error that may stem from losing the lease
// or the context; the holder maps it through leaseError. Other errors pass as-is.
type transportErr struct{ err error }

func (t transportErr) Error() string { return t.err.Error() }

func (rc *receiver) mapErr(err error) error {
	if t, ok := err.(transportErr); ok {
		return rc.leaseError(t.err)
	}
	return err
}

// WatchCmdDelivery recovers the PEL before new entries, one entry at a time.
// Returning an error from deliver leaves the message pending. A nil callback
// error permits XACK, which means complete output, NOT agent acceptance. A
// recovered delivery may have been output already: deduplicate by project/ID.
// Missing/expired entries carry no executable payload and Delivery explains why.
func (b *Bus) WatchCmdDelivery(ctx context.Context, agent, consumer, floor string, deliver func(Event) (bool, error)) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	if floor != "" && floor != "0" && !validStreamID(floor) {
		return fmt.Errorf("invalid cursor %q", floor)
	}
	// Contention is not an error wake: stay inside the caller's idle window.
	rc, err := b.holdReceiver(ctx, agent, consumer, 0)
	if err != nil {
		return err
	}
	defer rc.close()
	ctx = rc.ctx
	stream := StreamKey(b.project, "cmd")
	createAt := floor
	if createAt == "" {
		createAt = "$"
	}
	if err := b.r.XGroupCreateMkStream(ctx, stream, agent, createAt).Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	for {
		if ctx.Err() != nil {
			return rc.leaseError(ctx.Err())
		}
		done := false
		handled, err := b.deliverOne(ctx, agent, consumer, rc.token, floor, time.Second, func(e Event) error {
			d, err := deliver(e)
			done = d
			return err
		})
		if err != nil {
			return rc.mapErr(err)
		}
		if handled && done {
			return nil
		}
	}
}

// DrainCmd empties the agent's queue — the PEL first, then every unread entry —
// and returns as soon as both are empty. It applies no floor: whatever the group
// still holds is processed. Entries addressed elsewhere are acknowledged in this
// agent's group without output. Entries older than horizon are delivered with
// Stale set, never skipped; AgeS is measured against Redis TIME. Delivery,
// ACK and error semantics are WatchCmdDelivery's. The group is created at the
// horizon floor when missing. A receiver lease contended for drainLeaseWait
// yields ErrReceiverBusy. The count is entries output and acknowledged.
func (b *Bus) DrainCmd(ctx context.Context, agent, consumer string, horizon time.Duration, deliver func(Event) error) (int, error) {
	if !ValidName(agent) {
		return 0, fmt.Errorf("invalid agent %q", agent)
	}
	if err := b.EnsureGroup(ctx, agent, horizon); err != nil {
		return 0, err
	}
	rc, err := b.holdReceiver(ctx, agent, consumer, drainLeaseWait)
	if err != nil {
		return 0, err
	}
	defer rc.close()
	ctx = rc.ctx
	now, err := b.ServerTime(ctx)
	if err != nil {
		return 0, rc.leaseError(err)
	}
	nowMs := now.UnixMilli()
	cutoff := nowMs - horizon.Milliseconds()
	n := 0
	for {
		if ctx.Err() != nil {
			return n, rc.leaseError(ctx.Err())
		}
		delivered := false
		handled, err := b.deliverOne(ctx, agent, consumer, rc.token, "", -1, func(e Event) error {
			ms, _ := splitID(e.ID)
			e.Stale = ms < cutoff
			if e.AgeS = (nowMs - ms) / 1000; e.AgeS < 0 {
				e.AgeS = 0
			}
			delivered = true
			return deliver(e)
		})
		if err != nil {
			return n, rc.mapErr(err)
		}
		if !handled {
			return n, nil
		}
		if delivered {
			n++
		}
	}
}

// deliverOne processes at most one entry of the agent's group: the oldest PEL
// entry if any, else the next unread one (waiting up to block; block < 0 never
// blocks). handled=false means nothing was available. A handled entry was
// either skipped and ACKed (other target, at/below floor) or passed to deliver
// and, on a nil callback error, finished (metadata + XACK behind the fence).
// Errors that may come from a lost lease are wrapped in transportErr.
func (b *Bus) deliverOne(ctx context.Context, agent, consumer, token, floor string, block time.Duration, deliver func(Event) error) (bool, error) {
	stream := StreamKey(b.project, "cmd")
	lease := ReceiverKey(b.project, agent)
	// XPENDING includes IDs whose bodies have been trimmed. XAUTOCLAIM would
	// silently remove those IDs on some Redis versions, losing the gap evidence.
	pending, err := b.r.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: stream, Group: agent, Start: "-", End: "+", Count: 1}).Result()
	if err != nil {
		return false, transportErr{err}
	}
	var m redis.XMessage
	recovered := len(pending) > 0
	attempt := int64(1)
	if recovered {
		p := pending[0]
		attempt = p.RetryCount + 1
		// Inspect and claim atomically so Redis cannot auto-remove a tombstone
		// between checking its body and reclaiming it. A failed gap output stays pending.
		raw, err := claimRetained.Run(ctx, b.r, []string{stream, lease}, agent, consumer, p.ID, token).Slice()
		if err != nil {
			return false, transportErr{err}
		}
		m.ID = p.ID
		if len(raw) > 0 {
			fields, ok := raw[0].([]interface{})
			if !ok || len(fields) != 2 {
				return false, fmt.Errorf("invalid claim response")
			}
			vals, ok := fields[1].([]interface{})
			if !ok {
				return false, fmt.Errorf("invalid claim fields")
			}
			m.Values = make(map[string]interface{}, len(vals)/2)
			for i := 0; i+1 < len(vals); i += 2 {
				m.Values[fmt.Sprint(vals[i])] = vals[i+1]
			}
		}
	} else {
		// go-redis omits BLOCK for a negative duration: a non-blocking read.
		res, err := b.r.XReadGroup(ctx, &redis.XReadGroupArgs{Group: agent, Consumer: consumer, Streams: []string{stream, ">"}, Count: 1, Block: block}).Result()
		if errors.Is(err, redis.Nil) {
			return false, nil
		}
		if err != nil {
			return false, transportErr{err}
		}
		if len(res) == 0 || len(res[0].Messages) == 0 {
			return false, nil
		}
		m = res[0].Messages[0]
	}
	e := ParseEntry(stream, m.ID, toStringMap(m.Values))
	e.Recovered = recovered
	e.Attempt = attempt
	e.Delivery = "uncertain"
	if len(m.Values) == 0 {
		e.Delivery = "missing"
		// Target and task disappeared with the body. Recover only known board
		// metadata; otherwise leave target unknown, never invent a recipient.
		entries, err := b.r.HGetAll(ctx, BoardKey(b.project)).Result()
		if err != nil {
			return false, err
		}
		for task, raw := range entries {
			var entry BoardEntry
			if json.Unmarshal([]byte(raw), &entry) == nil && entry.Request != nil && entry.Request.ID == e.ID {
				r := entry.Request
				e.Task = task
				e.Target = r.Target
				e.From = r.From
				e.Ref = r.Thread
				e.ExpiresAt = r.ExpiresAt
				break
			}
		}
	}
	if e.Delivery != "missing" && e.ExpiresAt > 0 {
		now, err := b.r.Time(ctx).Result()
		if err != nil {
			return false, err
		}
		if now.UnixMilli() >= e.ExpiresAt {
			e.Delivery = "expired"
			e.Message = ""
		}
	}
	// The v1 floor is an explicit skip, not an acceptance signal. Other targets
	// are acknowledged only in THIS agent's group; their groups remain intact.
	if (e.Target != agent && !(e.Delivery == "missing" && e.Target == "")) || !aboveFloor(floor, e.ID) {
		if err := b.finishDelivery(ctx, agent, token, e, false); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := b.recordAttempt(ctx, agent, token, e); err != nil {
		return false, err
	}
	if err := deliver(e); err != nil {
		return false, err
	}
	if err := b.finishDelivery(ctx, agent, token, e, true); err != nil {
		return false, transportErr{err}
	}
	return true, nil
}

func validStreamID(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
		if _, err := strconv.ParseUint(p, 10, 63); err != nil {
			return false
		}
	}
	return true
}

// Metadata and XACK are committed together, behind the same receiver fence.
// No request fields are created for ordinary commands or missing legacy bodies.
var deliveryRecord = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return redis.error_reply('subscriber lease lost') end
local task = ARGV[4]
local raw = nil
if task ~= '' and ARGV[5] ~= 'skip' then raw = redis.call('HGET', KEYS[3], task) end
if raw then
 local e = cjson.decode(raw)
 local r = e.request
 if r and r.id == ARGV[3] and r.target == ARGV[2] then
  local t = redis.call('TIME'); local now = t[1]*1000 + math.floor(t[2]/1000)
  if ARGV[5] == 'attempt' then
   r.attempts = (r.attempts or 0)+1
   if ARGV[7] == '1' then r.duplicate_possible = true end
   r.delivery = ARGV[6]
  elseif ARGV[5] == 'finish' then
   r.delivery = ARGV[6]
   if ARGV[6] == 'output_written' then r.output_written_at = now end
  end
  redis.call('HSET', KEYS[3], task, cjson.encode(e))
 end
end
if ARGV[5] ~= 'attempt' then return redis.call('XACK', KEYS[2], ARGV[2], ARGV[3]) end
return 1`)

func (b *Bus) recordAttempt(ctx context.Context, agent, token string, e Event) error {
	return b.deliveryUpdate(ctx, agent, token, e, "attempt", e.Delivery)
}
func (b *Bus) finishDelivery(ctx context.Context, agent, token string, e Event, output bool) error {
	mode, delivery := "skip", e.Delivery
	if output {
		mode = "finish"
		if delivery == "uncertain" {
			delivery = "output_written"
		}
	}
	return b.deliveryUpdate(ctx, agent, token, e, mode, delivery)
}
func (b *Bus) deliveryUpdate(ctx context.Context, agent, token string, e Event, mode, delivery string) error {
	recovered := "0"
	if e.Recovered {
		recovered = "1"
	}
	return deliveryRecord.Run(ctx, b.r, []string{ReceiverKey(b.project, agent), StreamKey(b.project, "cmd"), BoardKey(b.project)}, token, agent, e.ID, e.Task, mode, delivery, recovered).Err()
}

// Keep missing bodies in the PEL until the caller successfully writes the gap.
var claimRetained = redis.NewScript(`
if redis.call('GET',KEYS[2])~=ARGV[4] then return redis.error_reply('subscriber lease lost') end
local body=redis.call('XRANGE',KEYS[1],ARGV[3],ARGV[3])
if #body==0 then return {} end
return redis.call('XCLAIM',KEYS[1],ARGV[1],ARGV[2],0,ARGV[3])`)
