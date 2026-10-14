package bus

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// v0.12: dispatch and missions run as user services (systemd --user, launchd),
// not as herdr tabs. Their state lives in {p}:services so busmon can show it
// from any machine: one JSON field per service kind, plus the "installed"
// marker written by `agentbus up`.

// ServicesInstalledField is the marker field: without it a service record is
// never written and a service exits ("project gone"), so a deleted project is
// not resurrected by a service that outlived it.
const ServicesInstalledField = "installed"

// ServicesKey is the {p}:services hash.
func ServicesKey(project string) string { return project + ":services" }

// ServicesInstall is what `agentbus up` recorded: which services, under which
// manager, on which host.
type ServicesInstall struct {
	Kinds   []string `json:"kinds"`
	Manager string   `json:"manager"`
	Host    string   `json:"host"`
	At      int64    `json:"at,omitempty"`
}

// ServiceRecord is one service's last published state. At is Redis TIME in
// unix milliseconds, stamped by the write script.
type ServiceRecord struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	At     int64  `json:"at,omitempty"`
	PID    int    `json:"pid,omitempty"`
	Host   string `json:"host,omitempty"`
	Error  string `json:"error,omitempty"`
	Log    string `json:"log,omitempty"`
}

// stampedHSet writes ARGV[2] (a JSON object) into field ARGV[1] with "at" set
// from Redis TIME; ARGV[3] == "1" requires the key to exist already.
var stampedHSet = redis.NewScript(`
if ARGV[3] == '1' and redis.call('EXISTS', KEYS[1]) == 0 then return 0 end
local rec = cjson.decode(ARGV[2])
local t = redis.call('TIME')
rec['at'] = t[1]*1000 + math.floor(t[2]/1000)
redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(rec))
return 1`)

func (b *Bus) stampedHSet(ctx context.Context, field string, v any, mustExist bool) (bool, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return false, err
	}
	flag := "0"
	if mustExist {
		flag = "1"
	}
	n, err := stampedHSet.Run(ctx, b.r, []string{ServicesKey(b.project)}, field, string(raw), flag).Int()
	return n == 1, err
}

// MarkServicesInstalled writes the marker; only `agentbus up` calls it.
func (b *Bus) MarkServicesInstalled(ctx context.Context, inst ServicesInstall) error {
	_, err := b.stampedHSet(ctx, ServicesInstalledField, inst, false)
	return err
}

// PutServiceState records one service's state. It writes nothing, and returns
// false, when the marker is gone: the project was deleted (or never set up).
func (b *Bus) PutServiceState(ctx context.Context, kind string, rec ServiceRecord) (bool, error) {
	if !ValidName(kind) || kind == ServicesInstalledField {
		return false, fmt.Errorf("invalid service kind %q", kind)
	}
	rec.Error = SanitizeReportMessage(rec.Error)
	rec.Detail = SanitizeReportMessage(rec.Detail)
	return b.stampedHSet(ctx, kind, rec, true)
}

// ServicesInstalled reports whether the marker is present.
func (b *Bus) ServicesInstalled(ctx context.Context) (bool, error) {
	return b.r.HExists(ctx, ServicesKey(b.project), ServicesInstalledField).Result()
}

// ServiceStates returns the marker (nil when absent) and every service record.
// A field that does not decode is skipped.
func (b *Bus) ServiceStates(ctx context.Context) (*ServicesInstall, map[string]ServiceRecord, error) {
	all, err := b.r.HGetAll(ctx, ServicesKey(b.project)).Result()
	if err != nil {
		return nil, nil, err
	}
	var inst *ServicesInstall
	recs := make(map[string]ServiceRecord, len(all))
	for k, v := range all {
		if k == ServicesInstalledField {
			var i ServicesInstall
			if json.Unmarshal([]byte(v), &i) == nil {
				inst = &i
			}
			continue
		}
		var r ServiceRecord
		if json.Unmarshal([]byte(v), &r) == nil {
			recs[k] = r
		}
	}
	return inst, recs, nil
}

// ClearServices removes the marker and every record (service remove).
func (b *Bus) ClearServices(ctx context.Context) error {
	return b.r.Del(ctx, ServicesKey(b.project)).Err()
}
