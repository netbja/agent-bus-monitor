package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/internal/agentcaller"
)

const askUsage = "usage: ask <agent> [--file questions.json | < questions.json] [--replace <id>] [--task <plan-task>] | ask <agent> --withdraw <id> [--question <qid> [--note <text>]]"

// askFile is what an agent writes: a title and its questions. Any other field
// (an id, a state, an answer) is ignored: only Answer stores an answer.
type askFile struct {
	Title     string            `json:"title"`
	Questions []bus.AskQuestion `json:"questions"`
}

// runAsk stores an agent's questions for a person and declares it waiting.
func runAsk(ctx context.Context, b *bus.Bus, args []string, stdin io.Reader, getenv func(string) string, out, errw io.Writer) int {
	args, file := extractFlag(args, "--file")
	args, replaces := extractFlag(args, "--replace")
	args, withdraw := extractFlag(args, "--withdraw")
	args, question := extractFlag(args, "--question")
	args, note := extractFlag(args, "--note")
	args, task := extractFlag(args, "--task")
	if len(args) != 1 || !bus.ValidName(args[0]) || (question != "" && withdraw == "") {
		fmt.Fprintln(errw, askUsage)
		return 2
	}
	agent := args[0]
	if agent == bus.SystemSender || agent == bus.RetroSender { // agentbus's own asks (the trust reminders, a retro's) are not an agent's to ask or close (reviewer-codex, retro §4)
		fmt.Fprintf(errw, "ask: %q is reserved for agentbus's own asks\n", agent)
		return 2
	}
	if question != "" {
		if note == "" {
			note = "answered outside the question panel"
		}
		left, err := b.WithdrawQuestion(ctx, agent, withdraw, question, note)
		if err != nil {
			fmt.Fprintf(errw, "ask: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "withdrawn %s question %s (%d still open)\n", withdraw, question, left)
		return 0
	}
	if withdraw != "" {
		if err := b.Withdraw(ctx, agent, withdraw); err != nil {
			fmt.Fprintf(errw, "ask: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "withdrawn %s\n", withdraw)
		return 0
	}
	if err := askerCheck(ctx, b, getenv, agent); err != nil {
		fmt.Fprintf(errw, "ask: %v\n", err)
		return 2
	}
	var src io.Reader = stdin
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			fmt.Fprintf(errw, "ask: %v\n", err)
			return 1
		}
		defer f.Close()
		src = f
	}
	var in askFile
	if err := json.NewDecoder(io.LimitReader(src, 1<<20)).Decode(&in); err != nil {
		fmt.Fprintf(errw, "ask: reading the questions: %v\n", err)
		return 1
	}
	def := bus.AskDef{From: agent, Title: in.Title, Task: task, Questions: in.Questions}
	id, state, err := b.Ask(ctx, def, replaces)
	if err != nil {
		fmt.Fprintf(errw, "ask: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "%s %s\n", id, state)
	if state == "open" { // best effort: the ask is the record, waiting only shows it
		open := len(def.Questions)
		if asks, err := b.Asks(ctx); err == nil {
			for _, a := range asks {
				if a.ID == id {
					open = a.Open()
				}
			}
		}
		msg := fmt.Sprintf("%d question(s): %s", open, def.Title)
		if _, err := b.Status(ctx, agent, "waiting", msg, statusIdent(ctx)); err != nil {
			fmt.Fprintf(errw, "ask: stored, but declaring waiting failed: %v\n", err)
		}
	}
	return 0
}

// askerCheck refuses an asker that is no agent (ask-refuses-person): ask
// <agent> names the ASKING agent, and "agentbus ask <person>" once stored an
// ask from the person and declared them waiting, an agent to busmon and the
// dispatcher. Refused: the person's recorded name (identity.as)
// unless the caller is an agent of that name, a human sender (busmon's
// human=yes entries) that no agent entry holds, and any name other than the
// caller's own (AGENT_BUS_AGENT; none in the person's shell, and never
// anonymous, which agentIdentity reads as none). Withdrawing is not checked,
// so a stray ask can still be closed.
func askerCheck(ctx context.Context, b *bus.Bus, getenv func(string) string, name string) error {
	const fix = "ask <your agent name>; the person is the one who answers"
	self := agentIdentity(getenv)
	if person := settingsIdentity(getenv); name == person && self != name {
		return fmt.Errorf("%q is the person (identity.as), not an agent: %s (in busmon)", name, fix)
	}
	snaps, err := b.Agents(ctx)
	if err != nil {
		return err
	}
	_, registered := snaps[name]
	_, humans, err := b.CmdTargetsAndHumanSenders(ctx)
	if err != nil {
		return err
	}
	if !registered && slices.Contains(humans, name) {
		return fmt.Errorf("%q wrote from busmon as a person, and is no agent: %s (in busmon)", name, fix)
	}
	if name != self {
		// The asker is the caller (reviewer-codex): a registered name is not
		// enough, or the person's shell or another agent could ask as it.
		if self == "" {
			return fmt.Errorf("asking as %q needs that agent's own identity (AGENT_BUS_AGENT=%s, in its terminal); a person never asks: %s", name, name, fix)
		}
		return fmt.Errorf("you are %q, not %q: an agent asks only as itself: %s", self, name, fix)
	}
	return nil
}

// runAsks lists the asks and what each question got.
func runAsks(ctx context.Context, b *bus.Bus, args []string, out, errw io.Writer) int {
	args, openOnly := extractBool(args, "--open")
	_, asJSON := extractBool(args, "--json")
	asks, err := b.Asks(ctx)
	if err != nil {
		fmt.Fprintf(errw, "asks: %v\n", err)
		return 1
	}
	if openOnly {
		kept := asks[:0]
		for _, a := range asks {
			if a.State == "open" {
				kept = append(kept, a)
			}
		}
		asks = kept
	}
	if asJSON {
		b, _ := json.MarshalIndent(asks, "", "  ")
		fmt.Fprintln(out, string(b))
		return 0
	}
	if len(asks) == 0 {
		fmt.Fprintln(out, "no ask")
		return 0
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, a := range asks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s (%s ago)\n", a.ID, a.From, a.State, a.Title, roundDur(time.Since(time.UnixMilli(a.CreatedAt))))
		for _, q := range a.Questions {
			ans := "open"
			if q.Answer != nil {
				ans = q.Answer.Value() + " (" + q.Answer.By + ")"
			}
			fmt.Fprintf(tw, "\t  %s\t%s\t%s\n", q.ID, q.Header, ans)
		}
	}
	tw.Flush()
	return 0
}

// runAnswer answers one question as a person, without busmon. ancestor is
// the process-tree check (agentcaller.Ancestor; nil = not checked).
func runAnswer(ctx context.Context, b *bus.Bus, args []string, getenv func(string) string, ancestor func(context.Context) (string, error), out, errw io.Writer) int {
	args, choice := extractFlag(args, "--choice")
	args, as := extractFlag(args, "--as")
	args, isText := extractBool(args, "--text")
	if len(args) < 2 || (choice == "") == (!isText) {
		fmt.Fprintln(errw, "usage: answer <ask-id> <question> (--choice <label> | --text <words...>) [--as <person>]")
		return 2
	}
	// An answer is the person's (ask-refuses-person): never an agent's, with
	// or without --as, so no agent answers in the person's name.
	if a := agentIdentity(getenv); a != "" || getenv("AGENTBUS_TERM_ID") != "" {
		fmt.Fprintf(errw, "answer: refused from an agent (%s): answers are the person's, in busmon or the person's own shell\n", strings.TrimSpace(a+" "+getenv("AGENTBUS_TERM_ID")))
		return 2
	}
	// Both variables can be unset by an agent: the process tree is checked
	// too (retro §5: a retro's approval is the person's typed answer, so the
	// answer's guard must hold). An unreadable ancestry refuses.
	if why := agentcaller.Refuse(ctx, getenv, ancestor, "an answer", "agentbus answer"); why != "" {
		fmt.Fprintf(errw, "answer: %s\n", why)
		return 2
	}
	by := as
	if by == "" {
		by = getenv(config.EnvName("identity.as")) // raw, lower-cased below, as always
	}
	if by == "" {
		by = "pilot"
	}
	by = strings.ToLower(by)
	text := strings.Join(args[2:], " ")
	id, left, err := b.Answer(ctx, args[0], args[1], by, choice, text)
	if err != nil {
		fmt.Fprintf(errw, "answer: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "answered %s/%s; %d question(s) still open\n", id, args[1], left)
	return 0
}
