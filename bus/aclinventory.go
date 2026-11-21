package bus

import (
	"fmt"
	"regexp"
	"strings"
)

// The ACL writer inventory (broker-hardening spec, step 4): what each
// production caller sends, recorded by aclrecord.go (-tags aclrecord) from
// the test suite and checked against the users' ACLs by TestACLInventory.

// ACLRecord is one command shape a caller sent: its chain of production
// frames outside bus/ (innermost first, "pkg.Func" relative to the module),
// the agent identity the process ran as, and the shaped arguments.
type ACLRecord struct {
	Chain    []string `json:"chain"`
	Via      string   `json:"via,omitempty"` // the bus/ function it went through
	Identity string   `json:"identity,omitempty"`
	Args     []string `json:"args"`
}

// aclSubcommands are the commands whose second argument names the command
// ACL checks ("client setinfo", "config get").
var aclSubcommands = map[string]bool{
	"client": true, "config": true, "memory": true, "xgroup": true, "xinfo": true, "object": true,
	"script": true, "command": true, "function": true, "acl": true, "latency": true, "slowlog": true,
	"pubsub": true, "debug": true, "module": true, "cluster": true,
}

// aclKeywords are the arguments that place a command's keys or shape it
// (DRYRUN finds XREAD's keys after "streams"), as go-redis sends them.
var aclKeywords = map[string]bool{
	"streams": true, "group": true, "block": true, "count": true, "noack": true, "maxlen": true, "minid": true,
	"limit": true, "nomkstream": true, "mkstream": true, "entriesread": true, "justid": true, "idle": true,
	"retrycount": true, "force": true, "lastid": true, "withscores": true, "byscore": true, "bylex": true,
	"rev": true, "nx": true, "xx": true, "gt": true, "lt": true, "ch": true, "incr": true, "ex": true,
	"px": true, "exat": true, "pxat": true, "keepttl": true, "get": true, "match": true, "type": true,
	"replace": true, "absttl": true, "freq": true, "weights": true, "aggregate": true, "~": true, "=": true,
	"*": true, "$": true, ">": true, "-": true, "+": true,
}

var aclNumber = regexp.MustCompile(`^-?[0-9]+$`)

// ShapeArgs keeps a command's shape without its data, as much as ACL DRYRUN
// needs to find its keys: the command and its subcommand, lower case; a
// project key as "{p}:<kind>" (ClassOf's kind, a tail kind as "{p}:gate:*"),
// an unclassified key-like argument "{p}:?"; the keywords of aclKeywords; a
// number of one or two digits as sent (EVAL's key count), a longer one as 1;
// any other value "x". It is idempotent, so a recording is shaped again
// when its rules change.
func ShapeArgs(args []any) []string {
	out := make([]string, len(args))
	var cmd string
	for i, a := range args {
		s := argString(a)
		switch {
		case i == 0:
			cmd = strings.ToLower(s)
			out[i] = cmd
		case i == 1 && aclSubcommands[cmd]:
			out[i] = strings.ToLower(s)
		case strings.HasPrefix(s, "{p}:"):
			out[i] = s
		case keyShape(s) != "":
			out[i] = keyShape(s)
		case aclNumber.MatchString(s):
			out[i] = s
			if len(strings.TrimPrefix(s, "-")) > 2 {
				out[i] = "1"
			}
		case aclKeywords[strings.ToLower(s)]:
			out[i] = strings.ToLower(s)
		default:
			out[i] = "x"
		}
	}
	return out
}

// keyShape is "{p}:<kind>" for a key of a known kind under any valid
// project, "" otherwise.
func keyShape(s string) string {
	project, rest, ok := strings.Cut(s, ":")
	if !ok || !ValidName(project) || rest == "" {
		return ""
	}
	if _, kind := ClassOf(project, s); kind != "" {
		return "{p}:" + kind
	}
	if _, ok := keyKinds[rest+"*"]; ok && strings.HasSuffix(rest, ":") {
		return "{p}:" + rest // a tail kind's empty tail ("{p}:swap:", a key that never exists), as sent
	}
	if strings.ContainsAny(rest, " /\t\n") {
		return "" // a sentence or a URL, not a key
	}
	return "{p}:?"
}

func argString(a any) string {
	switch v := a.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	}
	return fmt.Sprint(a)
}
