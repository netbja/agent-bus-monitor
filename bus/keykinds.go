package bus

import "strings"

// KeyClass is what a backup does with a project key (backup spec, "Record
// keys and runtime keys"): a record is restored, a runtime key is archived
// but restored only with --with-runtime.
type KeyClass int

const (
	// KeyUnknown is a key under the project's prefix that bus/ never writes
	// (a foreign suffix on a shared broker): archived like a runtime key,
	// never restored by default.
	KeyUnknown KeyClass = iota
	KeyRecord
	KeyRuntime
)

func (c KeyClass) String() string {
	switch c {
	case KeyRecord:
		return "record"
	case KeyRuntime:
		return "runtime"
	}
	return "unknown"
}

// keyKinds classifies every key kind bus/ writes, "{p}:<kind>", a ":*" kind
// for a dynamic tail ("gate:*" is {p}:gate:<agent>).
// TestEveryWrittenKeyKindIsClassified fails on a kind written in bus/ and
// missing here, so a new key is classified when it is added.
//
// The leases, intents and observations (pilot, dispatch, conductor,
// receiver:*, armed:*, swap:*, observed, context, health:daily) all expire,
// so a backup skips them anyway; they are runtime for the day one does not.
// missionops and its results are the conductor's operator queue: replaying
// it would re-run old operations, so runtime.
var keyKinds = map[string]KeyClass{
	"status": KeyRecord, "report": KeyRecord, "notify": KeyRecord, "cmd": KeyRecord,
	"verdicts": KeyRecord, "board": KeyRecord, "asks": KeyRecord, "ideas": KeyRecord,
	"plan": KeyRecord, "plan:facts": KeyRecord, "plan:ignore": KeyRecord, "gate:*": KeyRecord, "swaps": KeyRecord,
	"health": KeyRecord, "health:log": KeyRecord, "health:notes": KeyRecord, "quota:hist": KeyRecord,
	"change:*": KeyRecord, "missions:*": KeyRecord,
	"resume": KeyRecord, // how each agent resumes: it must outlive a reboot (herdr exit 4.7)
	"events": KeyRecord, // the terminal timeline (herdr exit 6.5): history, like the activity streams
	// self-repair's crash loops and resets (step 7): a crash loop holds until a
	// person or master resets it, so it outlives a restore as the ledger does.
	"selfrepair": KeyRecord,
	// a head master withdrew (master-idle §3.1): a decision, kept by a
	// restore so the guard does not ask again; pruned after 72 h.
	"mergequeue:dropped": KeyRecord,

	"services": KeyRuntime, "agents": KeyRuntime, "wake:*": KeyRuntime, "handoff": KeyRuntime,
	"checkback": KeyRuntime, // an agent's own reminder (master-idle §4): what is in flight now
	"run":       KeyRuntime, // the run budget's count (run-budget §2): a lost one starts a new run with a new baseline
	"held":      KeyRuntime, // what a held agent had (run-budget §4): beside {p}:quota's hold, which is runtime too
	"hostops":   KeyRuntime, // the relay's directives by id (herdr exit §4.2): accepted, then their outcome
	"quota":     KeyRuntime, "pause": KeyRuntime, "fallback": KeyRuntime, "usage": KeyRuntime,
	"budget": KeyRuntime, "tokens": KeyRuntime, "sessions": KeyRuntime, "doctor": KeyRuntime,
	"pilot": KeyRuntime, "dispatch": KeyRuntime, "conductor": KeyRuntime, "receiver:*": KeyRuntime,
	"armed:*": KeyRuntime, "swap:*": KeyRuntime, "observed": KeyRuntime, "context": KeyRuntime,
	"health:daily": KeyRuntime, "notified": KeyRuntime, "mergequeue": KeyRuntime, "ci": KeyRuntime, "mergequeue:due": KeyRuntime, "missionops": KeyRuntime, "missionops:results": KeyRuntime,
	"seen": KeyRuntime, "background:*": KeyRuntime, "bgtasks:*": KeyRuntime,
	"reboot": KeyRuntime, // the dispatcher's verdict on this boot (reboot-resume §3): judged again after a restore
}

// ClassOf classifies key as project's: its class and the kind that matched
// ("" and KeyUnknown for a key not under "project:" or of no known kind). An
// exact kind wins over a ":*" one ("health:log" is not "health" nor a tail),
// and a ":*" kind takes one ValidName segment only ("gate:coder", never
// "gate:coder:x").
func ClassOf(project, key string) (KeyClass, string) {
	rest, ok := strings.CutPrefix(key, project+":")
	if !ok || rest == "" {
		return KeyUnknown, ""
	}
	if c, ok := keyKinds[rest]; ok {
		return c, rest
	}
	// A tail is one segment, a valid name (an agent, a role, a change), as
	// bus/ writes it: a nested or malformed one is foreign (reviewer-codex).
	if head, tail, ok := strings.Cut(rest, ":"); ok && ValidName(tail) {
		if c, ok := keyKinds[head+":*"]; ok {
			return c, head + ":*"
		}
	}
	return KeyUnknown, ""
}
