package bus

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// restoreProject: KEYS = dispatch lease, conductor lease, the nd keys --force
// deletes, then the keys to restore; ARGV = nd, then one DUMP payload per key
// to restore. One script, so nothing interleaves: both leases absent; none of
// the archive's keys present unless --force deletes it (checked before any
// delete); each forced key DUMPed with its PTTL, then deleted; each archive
// key RESTOREd (no REPLACE) under pcall. On any error it deletes every key it
// wrote and puts back every key it deleted (REPLACE, its TTL kept; one a
// millisecond from expiring gets 1 ms, never none): the project is exactly as
// it was (foureyes). A put-back that fails is named in the error.
var restoreProject = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then return redis.error_reply('the dispatch lease is held: the project is live') end
if redis.call('EXISTS', KEYS[2]) == 1 then return redis.error_reply('the conductor lease is held: the project is live') end
local nd = tonumber(ARGV[1])
local forced = {}
for i = 1, nd do forced[KEYS[2 + i]] = true end
local first = 3 + nd
for i = first, #KEYS do
  if not forced[KEYS[i]] and redis.call('EXISTS', KEYS[i]) == 1 then
    return redis.error_reply('key exists: ' .. KEYS[i])
  end
end
local saved = {}
for i = 1, nd do
  local k = KEYS[2 + i]
  local payload = redis.call('DUMP', k)
  if payload then
    table.insert(saved, {k, payload, redis.call('PTTL', k)})
    redis.call('DEL', k)
  end
end
local written = {}
for i = first, #KEYS do
  local r = redis.pcall('RESTORE', KEYS[i], 0, ARGV[2 + i - first])
  if type(r) == 'table' and r.err then
    for _, k in ipairs(written) do redis.call('DEL', k) end
    local lost = {}
    for _, s in ipairs(saved) do
      local ttl = s[3]
      if ttl < 0 then ttl = 0 elseif ttl == 0 then ttl = 1 end
      local back = redis.pcall('RESTORE', s[1], ttl, s[2], 'REPLACE')
      if type(back) == 'table' and back.err then table.insert(lost, s[1]) end
    end
    local msg = 'restore ' .. KEYS[i] .. ': ' .. r.err
    if #lost > 0 then msg = msg .. '; NOT put back: ' .. table.concat(lost, ' ') end
    return redis.error_reply(msg)
  end
  table.insert(written, KEYS[i])
end
return #written`)

// RestoreProject writes keys (their DUMP payloads) into project in one
// script (backup spec, "All or nothing, per project"): refused while its
// dispatch or conductor lease is held, or when one of keys exists and is not
// in del; the keys in del (--force's, read by the caller) saved and deleted;
// then each RESTOREd. On any error every key it wrote is deleted and every
// key of del put back: the project is exactly as it was. Every key must be
// project's (the exact "project:" prefix), checked before the broker is
// touched.
func RestoreProject(ctx context.Context, r *redis.Client, project string, keys []DumpedKey, del []string) (int, error) {
	if !ValidName(project) {
		return 0, fmt.Errorf("invalid project %q", project)
	}
	prefix := project + ":"
	for _, k := range del {
		if !strings.HasPrefix(k, prefix) {
			return 0, fmt.Errorf("refused: %s is not a key of %s", k, project)
		}
	}
	for _, k := range keys {
		if !strings.HasPrefix(k.Key, prefix) {
			return 0, fmt.Errorf("refused: %s is not a key of %s", k.Key, project)
		}
	}
	if len(keys) == 0 && len(del) == 0 {
		return 0, nil
	}
	ks := append([]string{DispatchKey(project), ConductorKey(project)}, del...)
	args := []any{strconv.Itoa(len(del))}
	for _, k := range keys {
		ks = append(ks, k.Key)
		args = append(args, string(k.Dump))
	}
	n, err := restoreProject.Run(ctx, r, ks, args...).Int()
	if err != nil {
		return 0, fmt.Errorf("restore %s: %w", project, err)
	}
	return n, nil
}
