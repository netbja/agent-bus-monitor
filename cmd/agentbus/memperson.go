package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/history"
	"github.com/netbja/agent-bus-monitor/internal/agentcaller"
	"github.com/netbja/agent-bus-monitor/personmem"
	"github.com/netbja/agent-bus-monitor/service"
)

// agentbus mem person: the person's long-term memory on this host (spec
// 2026-11-21-person-memory-design r2, §4.2, §10 step 3). No project: the
// store is the host's; remember alone reads the broker, for the person's
// typed entry it quotes.

const memPersonUsage = `usage: agentbus mem person remember <topic> --type T --importance I --from <project>:<entry> --quote "<words>" [--answers <project>:<entry> --answers-quote "<words>"] [--supersedes <id> | --keep-both] [--kind K] [--relates <rel>:<topic>]...
       agentbus mem person recall [<words>...] [--type T] [--project P] [--all] [--limit N] [--json]
       agentbus mem person show <topic> | mem person list | mem person check
       agentbus mem person forget <id or topic> | mem person purge [--yes]   (the person's own acts)`

// memPersonDeps is what the verbs read: the environment (the store's place,
// AGENT_BUS_AGENT), the process ancestry (forget and purge refuse an
// agent's tree), the broker's entries, and the person's typed yes.
type memPersonDeps struct {
	Getenv   func(string) string
	Ancestor func(context.Context) (string, error)
	Reader   func(context.Context) (personmem.Reader, func(), error)
	In       io.Reader
}

// realMemPersonDeps reads the broker bus.Connect reaches (REDIS_URL, then
// REDIS_HOST) and each project's history archive on this host.
func realMemPersonDeps(getenv func(string) string) memPersonDeps {
	return memPersonDeps{Getenv: getenv, Ancestor: agentcaller.Ancestor, In: os.Stdin,
		Reader: func(ctx context.Context) (personmem.Reader, func(), error) {
			client, err := bus.ConnectContext(ctx, "")
			if err != nil {
				return nil, nil, err
			}
			dir := func(string) string { return "" }
			if paths, err := service.PathsFrom(getenv); err == nil {
				dir = func(project string) string { return history.Dir(paths.StateDir(project)) }
			}
			return personmem.BusReader{Client: client, HistoryDir: dir}, func() { client.Close() }, nil
		}}
}

// memPersonWait bounds one verb: the broker read, the store's lock and git.
var memPersonWait = time.Minute

// runMemPerson runs one verb. Exit 0 done, 1 refused or failed, 2 usage.
func runMemPerson(ctx context.Context, args []string, d memPersonDeps, stdout, stderr io.Writer) int {
	usage := func(why string) int {
		if why != "" {
			fmt.Fprintln(stderr, "mem person: "+why)
		}
		fmt.Fprintln(stderr, memPersonUsage)
		return 2
	}
	fail := func(err error) int { fmt.Fprintln(stderr, "error: "+err.Error()); return 1 }
	if len(args) == 0 {
		return usage("")
	}
	verb, rest := args[0], args[1:]
	ctx, cancel := context.WithTimeout(ctx, memPersonWait) // a writer behind a stuck one gives up (personmem's lock waits for ctx)
	defer cancel()
	s, err := personmem.Open(d.Getenv)
	if err != nil {
		return fail(err)
	}
	switch verb {
	case "remember":
		r, why := rememberRequest(rest)
		if why != "" {
			return usage(why)
		}
		rd, done, err := d.Reader(ctx)
		if err != nil {
			return fail(fmt.Errorf("the broker, for the entry quoted: %w", err))
		}
		defer done()
		o, err := s.Remember(ctx, rd, r)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "remembered %s in %s\n", o.ID, r.Topic)
	case "recall":
		return memRecall(s, rest, stdout, usage)
	case "show":
		if len(rest) != 1 {
			return usage("show takes one topic")
		}
		t, err := s.Load(rest[0])
		if err != nil {
			return fail(err)
		}
		fmt.Fprint(stdout, t.Format())
	case "list":
		if len(rest) != 0 {
			return usage("list takes nothing")
		}
		names, err := s.Topics()
		if err != nil {
			return fail(err)
		}
		if len(names) == 0 {
			fmt.Fprintln(stdout, "no topic yet ("+s.Dir+")")
		}
		for _, n := range names {
			if t, err := s.Load(n); err != nil {
				fmt.Fprintf(stdout, "%s  does not parse (agentbus mem person check)\n", n)
			} else {
				fmt.Fprintf(stdout, "%s  %d\n", n, len(t.Obs))
			}
		}
	case "check":
		if len(rest) != 0 {
			return usage("check takes nothing")
		}
		fs := s.Check()
		for _, f := range fs {
			fmt.Fprintln(stdout, "FAIL: "+f.String())
		}
		if len(fs) > 0 {
			return 1
		}
		names, _ := s.Topics()
		fmt.Fprintf(stdout, "PASS: %d topic(s) in %s\n", len(names), s.Dir)
	case "forget":
		if len(rest) != 1 {
			return usage("forget takes one observation id or one topic")
		}
		if why := agentcaller.Refuse(ctx, d.Getenv, d.Ancestor, "forgetting", "agentbus mem person forget"); why != "" {
			return fail(errors.New(why))
		}
		said, err := s.Forget(ctx, rest[0])
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, said)
	case "purge":
		rest, yes := extractBool(rest, "--yes")
		if len(rest) != 0 {
			return usage("purge takes only --yes")
		}
		if why := agentcaller.Refuse(ctx, d.Getenv, d.Ancestor, "a purge", "agentbus mem person purge"); why != "" {
			return fail(errors.New(why))
		}
		return memPurge(ctx, s, yes, d.In, stdout, stderr)
	default:
		return usage("no verb " + strconv.Quote(verb))
	}
	return 0
}

// rememberRequest reads remember's command line; why is a usage error.
func rememberRequest(args []string) (personmem.Request, string) {
	var r personmem.Request
	var from, answers, typ, imp string
	args, typ = extractFlag(args, "--type")
	args, imp = extractFlag(args, "--importance")
	args, from = extractFlag(args, "--from")
	args, r.Quote = extractFlag(args, "--quote")
	args, answers = extractFlag(args, "--answers")
	args, r.AnswersQuote = extractFlag(args, "--answers-quote")
	args, r.Supersedes = extractFlag(args, "--supersedes")
	args, r.KeepBoth = extractBool(args, "--keep-both")
	args, r.Kind = extractFlag(args, "--kind")
	for {
		var rel string
		if args, rel = extractFlag(args, "--relates"); rel == "" {
			break
		}
		typ, topic, ok := strings.Cut(rel, ":")
		if !ok || typ == "" || topic == "" {
			return r, "--relates takes <rel>:<topic>"
		}
		r.Relates = append(r.Relates, personmem.Relation{Type: typ, Topic: topic})
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") || typ == "" || imp == "" || from == "" || r.Quote == "" {
		return r, "remember takes a topic, --type, --importance, --from and --quote"
	}
	if r.Supersedes != "" && r.KeepBoth {
		return r, "--supersedes or --keep-both, one of them"
	}
	if (answers == "") != (r.AnswersQuote == "") {
		return r, "--answers and --answers-quote go together"
	}
	src, err := personmem.ParseSource(from)
	if err != nil {
		return r, "--from takes <project>:<entry> (" + err.Error() + ")"
	}
	r.Topic, r.Type, r.Importance, r.From = args[0], typ, imp, src
	if answers != "" {
		a, err := personmem.ParseSource(answers)
		if err != nil {
			return r, "--answers takes <project>:<entry> (" + err.Error() + ")"
		}
		r.Answers = &a
	}
	return r, ""
}

// memRecall prints recall's hits, one a line or as JSON.
func memRecall(s *personmem.Store, args []string, stdout io.Writer, usage func(string) int) int {
	var q personmem.Query
	var limit string
	var asJSON bool
	args, q.Type = extractFlag(args, "--type")
	args, q.Project = extractFlag(args, "--project")
	args, q.All = extractBool(args, "--all")
	args, limit = extractFlag(args, "--limit")
	args, asJSON = extractBool(args, "--json")
	if limit != "" {
		n, err := strconv.Atoi(limit)
		if err != nil || n < 0 {
			return usage("--limit takes a number")
		}
		q.Limit = n
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			return usage("recall: no flag " + a)
		}
	}
	q.Words = strings.Join(args, " ")
	hits := s.Recall(q)
	if asJSON {
		type hitJSON struct {
			Topic      string `json:"topic"`
			ID         string `json:"id"`
			Type       string `json:"type"`
			Importance string `json:"importance"`
			Said       string `json:"said"`
			Quote      string `json:"quote"`
			Answers    string `json:"answers,omitempty"`
			Question   string `json:"question,omitempty"`
			Supersedes string `json:"supersedes,omitempty"`
		}
		out := make([]hitJSON, 0, len(hits))
		for _, h := range hits {
			out = append(out, hitJSON{h.Topic, h.ID, h.Type, h.Importance, h.Said, h.Quote, h.Answers, h.Question, h.Supersedes})
		}
		b, _ := json.Marshal(out)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, h := range hits {
		fmt.Fprintf(stdout, "%s  %s  %s %s  %q\n", h.Topic, h.ID, h.Type, h.Importance, h.Quote)
	}
	return 0
}

// memPurge says what goes and asks for a typed yes (--yes skips it; an
// agent never reaches here).
func memPurge(ctx context.Context, s *personmem.Store, yes bool, in io.Reader, stdout, stderr io.Writer) int {
	n, err := s.Commits(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "error: "+err.Error())
		return 1
	}
	if n == 0 {
		fmt.Fprintln(stdout, "nothing to purge: the person's memory has no history yet")
		return 0
	}
	if !yes {
		fmt.Fprintf(stdout, "purge rebuilds the person's memory (%s) as one commit of its current files: %d commits go, and with them every forgotten text. Type yes to purge: ", s.Dir, n)
		line, _ := bufio.NewReader(in).ReadString('\n')
		if strings.TrimSpace(line) != "yes" {
			fmt.Fprintln(stdout, "\nnot purged")
			return 1
		}
	}
	gone, err := s.Purge(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "error: "+err.Error())
		return 1
	}
	fmt.Fprintf(stdout, "purged: %d commits became one\n", gone)
	return 0
}
