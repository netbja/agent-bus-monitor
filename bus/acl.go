package bus

import (
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
// own.
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
		return append(append(data, "+info", "%R~*"), aclWrites(aclDispatcherOnly)...)
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
	"cmd/agentbus.popEnd":       {ACLDispatcher}, // the dispatcher's own end of a done pop (popend.go)
	"cmd/agentbus.serviceRun":   {ACLDispatcher}, // the services' runner
	"cmd/agentbus.runDispatch":  {ACLDispatcher},
	"cmd/agentbus.runBackup":    {ACLAdmin},
	"cmd/agentbus.runRestore":   {ACLAdmin},
	"cmd/agentbus.planPriority": {ACLBusmon}, // refuseAgentForPriority
	"cmd/agentbus.runRun":       {ACLBusmon}, // run reset: only the person
	"cmd/agentbus.runCI":        {ACLBusmon}, // ci reset: only the person
	"cmd/agentbus.runMergeDrop": {ACLMaster}, // deliverRoleCheck: the deliverers (master; docs is a question)
	"cmd/agentbus.planLoad":     {ACLMaster}, // the plan is master's to load (spec §5 Q4)
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
//  1. a process's own package anywhere in the chain: the services
//     (internal/dispatchsvc), busmon, setup and backup;
//  2. the outermost agent CLI function aclVerbs names;
//  3. the service packages (dispatch, mission, holder) with no CLI frame
//     outside them: their own tests stand for the service;
//  4. the agent CLI: agent, and master too when it ran as master.
func ACLRoleOf(chain []string, identity string) []string {
	cli := false
	for _, f := range chain {
		switch pkgOf(f) {
		case "internal/dispatchsvc":
			return []string{ACLDispatcher}
		case "cmd/busmon":
			return []string{ACLBusmon}
		case "setup", "backup":
			return []string{ACLAdmin}
		case "cmd/agentbus":
			cli = true
		}
	}
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
