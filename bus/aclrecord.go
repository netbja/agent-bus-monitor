//go:build aclrecord

package bus

// The ACL writer inventory's recorder (broker-hardening spec, step 4): built
// only with -tags aclrecord, it appends one JSON line per new command shape
// a production caller sends to the file AGENTBUS_ACL_RECORD names. The run:
//
//	GOFLAGS=-tags=aclrecord AGENTBUS_ACL_RECORD=/tmp/acl.jsonl \
//	  AGENTBUS_TEST_REDIS_URL=unix:///tmp/agentbus-test/redis.sock go test ./... -count=1
//	go test ./bus -run TestACLInventory -acl-update=/tmp/acl.jsonl
//
// GOFLAGS carries the tag into the binaries the tests build. Only the shape
// is kept: a key is "{p}:<kind>", a short token (a keyword, a number) stays,
// any other value is "x", so no test text reaches the committed inventory.

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
)

// aclRecordEnv names the record file. A test-only name, like
// AGENTBUS_TEST_REDIS_URL: not a setting, so not in config/keys.go.
const aclRecordEnv = "AGENTBUS_ACL_RECORD"

const aclModule = "github.com/netbja/agent-bus-monitor/"

type aclRecorder struct {
	path string
	mu   sync.Mutex
	seen map[string]bool
}

var (
	aclRec     *aclRecorder
	aclRecOnce sync.Once
)

func installACLRecorder(c *redis.Client) {
	aclRecOnce.Do(func() {
		if p := os.Getenv(aclRecordEnv); p != "" {
			aclRec = &aclRecorder{path: p, seen: map[string]bool{}}
		}
	})
	if aclRec != nil {
		c.AddHook(aclRec)
	}
}

func (r *aclRecorder) DialHook(next redis.DialHook) redis.DialHook { return next }

func (r *aclRecorder) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		r.record([]redis.Cmder{cmd})
		return next(ctx, cmd)
	}
}

func (r *aclRecorder) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		r.record(cmds)
		return next(ctx, cmds)
	}
}

func (r *aclRecorder) record(cmds []redis.Cmder) {
	chain, via, ok := callerChain()
	if !ok {
		return // a test's own call, not a production caller's
	}
	who := os.Getenv("AGENT_BUS_AGENT")
	for _, c := range cmds {
		rec := ACLRecord{Chain: chain, Via: via, Identity: who, Args: ShapeArgs(c.Args())}
		line, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		r.mu.Lock()
		if !r.seen[string(line)] {
			r.seen[string(line)] = true
			if f, err := os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				f.Write(append(line, '\n')) // one write under O_APPEND: lines of concurrent processes do not interleave
				f.Close()
			}
		}
		r.mu.Unlock()
	}
}

// callerChain is the module's production frames outside bus/, innermost
// first, as "pkg.Func" relative to the module, up to the first test frame,
// and via, the outermost bus/ frame before them (the bus method called);
// false when there is none (a test or a test helper calling bus/ itself).
func callerChain() (chain []string, via string, ok bool) {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		name, inModule := strings.CutPrefix(f.Function, aclModule)
		switch {
		case !inModule:
		case strings.HasPrefix(name, "bus."):
			if len(chain) == 0 {
				via = trimClosure(name)
			}
		case strings.HasSuffix(f.File, "_test.go"), testHelper(name), inTest(name):
			return chain, via, len(chain) > 0
		default:
			chain = append(chain, trimClosure(name))
		}
		if !more {
			return chain, via, len(chain) > 0
		}
	}
}

// inTest: a function declared inside a test ("pkg.TestX.helper"), whatever
// file its frame names.
func inTest(name string) bool {
	_, fn, _ := strings.Cut(name[strings.LastIndex(name, "/")+1:], ".")
	return strings.HasPrefix(fn, "Test")
}

// testHelper: the packages tests seed the broker through.
func testHelper(name string) bool {
	for _, p := range []string{"internal/bustest.", "internal/cmdtest.", "termhost/fake.", "internal/testredis."} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// trimClosure drops a closure's suffix: "cmd/agentbus.main.func3" is main.
var closure = regexp.MustCompile(`(\.func\d+|\.gowrap\d+|\.deferwrap\d+)+(\.\d+)*$`)

func trimClosure(name string) string { return closure.ReplaceAllString(name, "") }
