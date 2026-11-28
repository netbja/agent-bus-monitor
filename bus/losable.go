package bus

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Losable work (cleaning-up-agents §3.1 item 5) needs git, so only the
// dispatcher's merge-queue pass reads it; it writes what it found, fenced, to
// {p}:losable → <role> = {branches, stashes, at}. Every other reader takes a
// role's entry while it is fresh; older, absent or unreadable, item 5 is
// unknown (UnknownLosable), never "owes nothing".

// LosableKey is the hash of each role's losable work, as the last pass found it.
func LosableKey(project string) string { return project + ":losable" }

// LosableFresh is how long a pass's finding holds: three passes of the
// merge queue (every 5 minutes).
const LosableFresh = 15 * time.Minute

// losableTTL drops the hash an hour after a dispatcher's last write.
const losableTTL int64 = 3600000 // ms

// Losable is one role's losable work.
type Losable struct {
	Branches []string `json:"branches"` // branches it holds open on the board whose local tip is not on the remote
	Stashes  []string `json:"stashes"`  // stash messages that name the role
	At       int64    `json:"at"`       // Unix ms, Redis TIME of the pass's write
}

// losableReplace: 0 not the holder, else 1. The hash becomes exactly the
// given role/value pairs, each value a JSON object left open before "at",
// which Redis TIME closes; it expires after ARGV[2] ms.
var losableReplace = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[2])
if #ARGV > 2 then
  local t = redis.call('TIME')
  local ms = tostring(tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
  for i = 3, #ARGV, 2 do
    redis.call('HSET', KEYS[2], ARGV[i], ARGV[i+1] .. ms .. '}')
  end
  redis.call('PEXPIRE', KEYS[2], ARGV[2])
end
return 1`)

// FencedPutLosable replaces {p}:losable with found while token holds the
// dispatch lease; each entry's at is the broker's time. A role that is not a
// valid name is skipped.
func (b *Bus) FencedPutLosable(ctx context.Context, token string, found map[string]Losable) error {
	argv := []interface{}{token, losableTTL}
	for role, l := range found {
		if !ValidName(role) {
			continue
		}
		v, err := json.Marshal(struct {
			Branches []string `json:"branches"`
			Stashes  []string `json:"stashes"`
		}{orEmpty(l.Branches), orEmpty(l.Stashes)})
		if err != nil {
			return err
		}
		argv = append(argv, role, strings.TrimSuffix(string(v), "}")+`,"at":`)
	}
	n, err := losableReplace.Run(ctx, b.r, []string{DispatchKey(b.project), LosableKey(b.project)}, argv...).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotHolder
	}
	return nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// LosableRecord reads {p}:losable; a malformed entry is left out (its role
// then reads unknown).
func (b *Bus) LosableRecord(ctx context.Context) (map[string]Losable, error) {
	raw, err := b.r.HGetAll(ctx, LosableKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]Losable, len(raw))
	for role, v := range raw {
		var l Losable
		if json.Unmarshal([]byte(v), &l) == nil && l.At > 0 {
			out[role] = l
		}
	}
	return out, nil
}

// FreshLosable is role's entry when the pass wrote it less than LosableFresh
// before now, and not after it; false otherwise (item 5 unknown).
func FreshLosable(rec map[string]Losable, role string, now time.Time) (Losable, bool) {
	l, ok := rec[role]
	if !ok {
		return Losable{}, false
	}
	age := now.Sub(time.UnixMilli(l.At))
	if age < 0 || age >= LosableFresh {
		return Losable{}, false
	}
	return l, true
}
