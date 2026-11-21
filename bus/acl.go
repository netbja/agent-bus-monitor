package bus

import (
	"cmp"
	"context"
	"sync"

	"fmt"
	"github.com/netbja/agent-bus-monitor/config"
	"slices"
	"sort"
	"strings"
)

// The broker's ACL users (broker-hardening spec, step 4): who connects with
// which user, what commands and which keys it may write. Not enabled yet:
// TestACLInventory checks every command the suite's production callers send
// against these rules (the writer inventory, step 4's first PR), and the
// table is corrected from that result before any user exists on a broker.

// The ACL users, one per role.
const (
	ACLAdmin      = "admin"      // ./setup.sh, backup and restore, run by the person
	ACLMaster     = "master"     // the agent whose identity is master
	ACLDispatcher = "dispatcher" // the services: dispatcher, conductor, holder
	ACLAgent      = "agent"      // every other agent's CLI
	ACLBusmon     = "busmon"     // busmon and the person's own CLI outside agents
	ACLPing       = "ping"       // the compose healthcheck
)

// ACLRoles is every user, in the spec's order.
var ACLRoles = []string{ACLAdmin, ACLMaster, ACLDispatcher, ACLAgent, ACLBusmon, ACLPing}

// aclMasterOnly are the kinds only master writes (spec §5 Q4, the person's
// answer: as proposed).
var aclMasterOnly = map[string]bool{
	"pilot": true, "plan": true, "plan:facts": true, "plan:ignore": true, "mergequeue:dropped": true,
}

// aclDispatcherOnly are the leases and records the services' fenced scripts
// own; master writes missionops too (ACLRules).
var aclDispatcherOnly = map[string]bool{
	"dispatch": true, "conductor": true, "ci": true, "mergequeue": true, "mergequeue:due": true,
	"missionops": true, "missionops:results": true, "run": true, "reboot": true,
}

// ACLRules is role's ACL SETUSER rules, without its password: its commands,
// then its keys. Writable kinds come from keyKinds, so a new kind is
// writable by every data role until it is listed as master- or
// dispatcher-only.
func ACLRules(role string) []string {
	data := []string{"+@all", "-@dangerous"}
	switch role {
	case ACLAdmin:
		return []string{"+@all", "-flushall", "-flushdb", "-shutdown", "-debug", "-module", "~*"}
	case ACLMaster:
		// missionops too: master queues the conductor's operations (ACL answer
		// d); the dispatcher's conductor reads and trims them
		return append(append(data, "+info", "%R~*"), aclWrites(aclExcept(aclDispatcherOnly, "missionops"))...)
	case ACLDispatcher:
		// mergequeue:dropped too: the merge queue's pass prunes a drop past
		// MergeDropKeep (the inventory: internal/dispatchsvc mergeQueueChore.pass)
		return append(append(data, "+info", "+config|get", "+memory|usage", "%R~*"), aclWrites(aclExcept(aclMasterOnly, "mergequeue:dropped"))...)
	case ACLAgent:
		return append(append(data, "%R~*"), aclWrites(aclMasterOnly, aclDispatcherOnly)...)
	case ACLBusmon:
		return append(data, "+info", "+config|get", "+memory|usage", "~*")
	case ACLPing:
		return []string{"-@all", "+ping"}
	}
	return nil
}

// aclWrites is a read-write pattern for every kind none of except lists,
// any project: "~*:board", "~*:gate:*". Not "%W~": Redis wants one pattern
// to grant every flag a key needs, and a script's keys need read and write,
// so "%R~*" beside "%W~*:board" would refuse every fenced script.
func aclWrites(except ...map[string]bool) []string {
	var out []string
	for kind := range keyKinds {
		skip := false
		for _, e := range except {
			skip = skip || e[kind]
		}
		if !skip {
			out = append(out, "~*:"+kind)
		}
	}
	sort.Strings(out)
	return out
}

// aclVerbs are the agent CLI's functions whose user is not the caller's
// identity, each from its own guard or doc: the dispatcher's own steps the
// CLI holds, the person's verbs (refused in an agent's environment, so the
// person's CLI: busmon), backup and restore (admin), and the deliverers'.
var aclVerbs = map[string][]string{
	"cmd/agentbus.popEnd":       {ACLDispatcher}, // the dispatcher's own end of a done pop: only withPopEnd's dispatch config calls it
	"cmd/agentbus.serviceRun":   {ACLDispatcher}, // the services' runner
	"cmd/agentbus.runDispatch":  {ACLDispatcher},
	"cmd/agentbus.runBackup":    {ACLAdmin},  // refuses an agent's environment (backupcmd.go); list writes nothing
	"cmd/agentbus.runRestore":   {ACLAdmin},  // refuses an agent's environment (restorecmd.go)
	"cmd/agentbus.planPriority": {ACLBusmon}, // refuseAgentForPriority
	"cmd/agentbus.runRun":       {ACLBusmon}, // run reset, its one write, refuses an agent
	"cmd/agentbus.runCI":        {ACLBusmon}, // ci reset, its one write, refuses an agent
	"cmd/agentbus.runMergeDrop": {ACLMaster}, // deliverRoleCheck: the deliverers (master; docs is question e)
}

// aclExcept is set without kinds.
func aclExcept(set map[string]bool, kinds ...string) map[string]bool {
	out := map[string]bool{}
	for k, v := range set {
		out[k] = v && !slices.Contains(kinds, k)
	}
	return out
}

// ACLRoleOf is the users a recorded caller connects as (spec step 4,
// "Choosing the user"), from its chain of frames, innermost first; nil when
// nothing decides (a library called only from its own tests):
//  1. a process's own package anywhere in the chain, in this order: the
//     services (internal/dispatchsvc, whose backup chore runs backup/ as
//     dispatcher), busmon, setup, and backup/ with no CLI frame (the
//     CLI's backup and restore are its verbs, below);
//  2. the outermost agent CLI function aclVerbs names;
//  3. the service packages (dispatch, mission, holder) with no CLI frame
//     outside them: their own tests stand for the service;
//  4. the agent CLI: agent, and master too when it ran as master.
func ACLRoleOf(chain []string, identity string) []string {
	in := map[string]bool{}
	for _, f := range chain {
		in[pkgOf(f)] = true
	}
	switch { // the process first: the dispatcher's own backup chore connects as dispatcher
	case in["internal/dispatchsvc"]:
		return []string{ACLDispatcher}
	case in["cmd/busmon"]:
		return []string{ACLBusmon}
	case in["setup"], in["backup"] && !in["cmd/agentbus"]:
		return []string{ACLAdmin}
	}
	cli := in["cmd/agentbus"]
	for i := len(chain) - 1; i >= 0; i-- {
		if roles, ok := aclVerbFor(chain[i]); ok {
			return roles
		}
	}
	if cli {
		if identity == ACLMaster {
			return []string{ACLAgent, ACLMaster}
		}
		return []string{ACLAgent}
	}
	for _, f := range chain {
		switch pkgOf(f) {
		case "dispatch", "mission", "holder":
			return []string{ACLDispatcher}
		}
	}
	return nil
}

// aclVerbFor is frame's entry in aclVerbs, a method value's
// ("cmd/agentbus.restoreBroker.Restore") by its function.
func aclVerbFor(frame string) ([]string, bool) {
	for name, roles := range aclVerbs {
		if frame == name || strings.HasPrefix(frame, name+".") {
			return roles, true
		}
	}
	return nil, false
}

// pkgOf is a frame's package: "dispatch" of "dispatch.(*loop).put",
// "cmd/agentbus" of "cmd/agentbus.main".
func pkgOf(frame string) string {
	slash := strings.LastIndex(frame, "/")
	if dot := strings.Index(frame[slash+1:], "."); dot >= 0 {
		return frame[:slash+1+dot]
	}
	return frame
}

// ACLEntry is how a process came to the broker (step 4b): it, with the
// process's identity and ancestry, fixes the user it logs in as.
type ACLEntry int

const (
	ACLEntryCLI     ACLEntry = iota // agentbus, any command but the ones below
	ACLEntryService                 // agentbus service run, the holder service
	ACLEntryBusmon                  // busmon
	ACLEntryAdmin                   // ./setup.sh, agentbus backup and restore
)

// ACLUserFor is the user a process logs in as once broker.acl is on (spec
// step 4b): the services as dispatcher when neither an identity nor a
// registered agent among their ancestors says otherwise (else the CLI's
// rule); setup, backup and restore as admin,
// from a person's shell only (refused when identity is set or ancestor, the
// registered agent among the process's ancestors, is not ""); busmon and the
// CLI by identity, after the process-tree check: a process under a
// registered agent other than master is agent whatever it claims, master's
// own tree and master's identity are master, the person's shell is busmon.
func ACLUserFor(entry ACLEntry, identity, ancestor string) (string, error) {
	switch entry {
	case ACLEntryService:
		// The service manager's launch has no registered agent among its
		// ancestors and no identity (the services' env file drops
		// AGENT_BUS_AGENT, service/env.go): only it is dispatcher. Any other is
		// whoever the CLI's rule names: an agent running service run by hand,
		// or its child that outlived it with its identity (reviewer-codex).
		if identity == "" && ancestor == "" {
			return ACLDispatcher, nil
		}
	case ACLEntryAdmin:
		if identity != "" || ancestor != "" {
			return "", fmt.Errorf("the admin user is the person's: not from an agent (%s)", cmp.Or(identity, ancestor))
		}
		return ACLAdmin, nil
	}
	switch {
	case ancestor != "" && ancestor != ACLMaster: // under another agent: never more than agent
		return ACLAgent, nil
	case identity == ACLMaster || ancestor == ACLMaster && identity == "":
		return ACLMaster, nil
	case identity != "":
		return ACLAgent, nil
	}
	return ACLBusmon, nil
}

// connectEntry is the process's entry for Connect's user (SetConnectEntry):
// the CLI's, with no ancestry check, until main says otherwise.
var connectEntry struct {
	sync.Mutex
	entry    ACLEntry
	ancestor func(context.Context) (string, error)
}

// SetConnectEntry is main's statement of how this process came to the
// broker (step 4b): Connect then logs in, while broker.acl is on, as the user
// ACLUserFor names from entry, AGENT_BUS_AGENT and ancestor, the registered
// agent among the process's ancestors ("" none, or no ancestry on this host;
// an error: unknown). nil ancestor: none known.
func SetConnectEntry(entry ACLEntry, ancestor func(context.Context) (string, error)) {
	connectEntry.Lock()
	defer connectEntry.Unlock()
	connectEntry.entry, connectEntry.ancestor = entry, ancestor
}

// resolvingAncestry marks the context of the ancestry's own Connect
// (agentcaller.Ancestor reads the agents through Connect): connectUser then
// answers agent, which reads every key, instead of asking for the ancestry
// again.
type resolvingAncestry struct{}

// connectUser is the user Connect logs in as: "" while broker.acl is off.
// An ancestry that cannot be read makes the CLI and busmon agent (the least
// rights) and refuses the admin entry.
func connectUser(ctx context.Context, getenv func(string) string) (string, error) {
	if config.Machine(getenv).String("broker.acl") != "on" {
		return "", nil
	}
	connectEntry.Lock()
	entry, ancestorOf := connectEntry.entry, connectEntry.ancestor
	connectEntry.Unlock()
	if ctx.Value(resolvingAncestry{}) != nil {
		return ACLAgent, nil // the ancestry's own read of the agents: never asks again
	}
	ancestor := ""
	if ancestorOf != nil {
		a, err := ancestorOf(context.WithValue(ctx, resolvingAncestry{}, true))
		switch {
		case err != nil && entry == ACLEntryAdmin:
			return "", fmt.Errorf("the admin user is the person's: cannot check this process's ancestry: %w", err)
		case err != nil:
			return ACLAgent, nil
		}
		ancestor = a
	}
	return ACLUserFor(entry, getenv("AGENT_BUS_AGENT"), ancestor)
}
