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
// to restore. One script, so nothing interleaves: both leases absent, the
// forced keys deleted, none of the archive's keys present, then each RESTOREd
// (no REPLACE) under pcall; any error deletes every key this script wrote.
var restoreProject = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then return redis.error_reply('the dispatch lease is held: the project is live') end
if redis.call('EXISTS', KEYS[2]) == 1 then return redis.error_reply('the conductor lease is held: the project is live') end
local nd = tonumber(ARGV[1])
for i = 1, nd do redis.call('DEL', KEYS[2 + i]) end
local first = 3 + nd
for i = first, #KEYS do
  if redis.call('EXISTS', KEYS[i]) == 1 then return redis.error_reply('key exists: ' .. KEYS[i]) end
end
local written = {}
for i = first, #KEYS do
  local r = redis.pcall('RESTORE', KEYS[i], 0, ARGV[2 + i - first])
  if type(r) == 'table' and r.err then
    for _, k in ipairs(written) do redis.call('DEL', k) end
    return redis.error_reply('restore ' .. KEYS[i] .. ': ' .. r.err)
  end
  table.insert(written, KEYS[i])
end
return #written`)

// RestoreProject writes keys (their DUMP payloads) into project in one
// script (backup spec, "All or nothing, per project"): refused while its
// dispatch or conductor lease is held; the keys in del (--force's, read by
// the caller) deleted first; then, if none of keys exists, each RESTOREd.
// On any error every key it wrote is deleted again: the project is as it
// was, except the keys del asked to delete. Every key must be project's
// (the exact "project:" prefix), checked before the broker is touched.
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
