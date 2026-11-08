package bus

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// The Projects page's delete (spec 2026-11-08-projects-admin-page-design.md
// §5.3): the last check and the DEL are one script, so no dispatcher,
// conductor or service can start between them; a second scan then says what
// came back, since a writer outside the script cannot be fenced.

// ServiceFreshFor: a service record younger than this, in a live state, is a
// running service, on whatever host (a service writes one every 5 s).
const ServiceFreshFor = 30 * time.Second

// DeleteRefusal is why the delete refused: What is "dispatch" or
// "conductor" (a lease held), or a service kind whose fresh record names Host.
type DeleteRefusal struct {
	What, Host string
}

func (e *DeleteRefusal) Error() string {
	switch {
	case e.What == "dispatch" || e.What == "conductor":
		return "a " + e.What + " holds its lease"
	case e.Host != "":
		return "its " + e.What + " service runs on " + e.Host
	}
	return "its " + e.What + " service runs"
}

// quietCheck is the shared check, KEYS[1..3] = dispatch, conductor, services,
// ARGV[1] = the freshness in ms: nil when quiet, else {what, host}.
const quietCheck = `
local function busy(dispatch, conductor, services, fresh)
  if redis.call('EXISTS', dispatch) == 1 then return {'dispatch', ''} end
  if redis.call('EXISTS', conductor) == 1 then return {'conductor', ''} end
  if redis.call('TYPE', services).ok ~= 'hash' then return nil end
  local t = redis.call('TIME')
  local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  local all = redis.call('HGETALL', services)
  for i = 1, #all, 2 do
    if all[i] ~= 'installed' then
      local ok, rec = pcall(cjson.decode, all[i + 1])
      if ok and type(rec) == 'table' then
        local st, at = rec.state, tonumber(rec.at)
        if at and now - at < fresh and st ~= 'stopped' and st ~= 'failed' and st ~= 'gave-up' then
          local host = rec.host
          if type(host) ~= 'string' then host = '' end
          return {all[i], host}
        end
      end
    end
  end
  return nil
end
`

// projectQuiet reads the check alone.
var projectQuiet = redis.NewScript(quietCheck + `
local b = busy(KEYS[1], KEYS[2], KEYS[3], tonumber(ARGV[1]))
if b then return b end
return {}
`)

// deleteIfQuiet: KEYS[1..3] as above, then the keys to delete.
var deleteIfQuiet = redis.NewScript(quietCheck + `
local b = busy(KEYS[1], KEYS[2], KEYS[3], tonumber(ARGV[1]))
if b then return {0, b[1], b[2]} end
local n = 0
for i = 1, #KEYS do n = n + redis.call('DEL', KEYS[i]) end
return {1, n}
`)

// Test seams: what runs between the scan and the script, and after it.
var beforeQuietScript, afterQuietScript func()

func quietKeys(project string) []string {
	return []string{DispatchKey(project), ConductorKey(project), ServicesKey(project)}
}

// ProjectQuiet is nil when no dispatcher or conductor lease is held and no
// service published a live record within ServiceFreshFor, by Redis TIME, on
// any host; else the DeleteRefusal.
func ProjectQuiet(ctx context.Context, r *redis.Client, project string) error {
	if !ValidName(project) {
		return fmt.Errorf("invalid project %q (want %s)", project, nameRE)
	}
	res, err := projectQuiet.Run(ctx, r, quietKeys(project), ServiceFreshFor.Milliseconds()).StringSlice()
	if err != nil {
		return err
	}
	if len(res) == 2 {
		return &DeleteRefusal{What: res[0], Host: res[1]}
	}
	return nil
}

// DeleteProjectIfQuiet deletes every {project}:* key in one script that
// first refuses (a *DeleteRefusal, nothing deleted) while the project is
// live; then scans again and returns the keys written meanwhile (left), so a
// delete is never reported clean when something of the project still runs.
func DeleteProjectIfQuiet(ctx context.Context, r *redis.Client, project string) (int64, []string, error) {
	keys, err := ProjectKeys(ctx, r, project)
	if err != nil {
		return 0, nil, err
	}
	named := quietKeys(project)
	all := append([]string{}, named...)
	for _, k := range keys {
		if k != named[0] && k != named[1] && k != named[2] {
			all = append(all, k)
		}
	}
	if beforeQuietScript != nil {
		beforeQuietScript()
	}
	res, err := deleteIfQuiet.Run(ctx, r, all, ServiceFreshFor.Milliseconds()).Slice()
	if err != nil {
		return 0, nil, err
	}
	if len(res) == 3 {
		what, _ := res[1].(string)
		host, _ := res[2].(string)
		return 0, nil, &DeleteRefusal{What: what, Host: host}
	}
	n, _ := res[1].(int64)
	if afterQuietScript != nil {
		afterQuietScript()
	}
	left, err := ProjectKeys(ctx, r, project)
	return n, left, err
}
