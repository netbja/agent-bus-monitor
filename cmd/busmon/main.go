// busmon — live TUI dashboard for the Agent Bus over Redis Streams.
//
// Tails one project's streams ({project}:status|report|notify|cmd) and renders
// a btop-style screen of framed panels (spec:
// specs/2026-10-10-busmon-ui-redesign-design.md):
//
//	HEADER   project, pilot driver, account budget per provider, dispatcher
//	         (disp up/down) and the monitor's own link to the broker (bus).
//	AGENTS   a table sorted by urgency (blocked, waiting mail, working, idle…),
//	         one row per agent: declared state, age of that declaration, mail
//	         waiting, wake state, context as raw tokens (never a percentage).
//	         A scrollbar appears when the agents outgrow the frame.
//	ACTIVITY the feed of status/report/notify/cmd events in columns, with a day
//	         separator; live until the operator scrolls (then "paused · N new").
//	BOARD    the shared task board ({project}:board).
//	REQUESTS what is waiting on someone (tracked requests, unanswered threads).
//
// The layout is responsive: wide (≥120 columns) shows all four panels, medium
// (80–119) keeps Agents and puts the others behind tabs 1–3, narrow (<80)
// shows one view at a time (tabs 1–4). Tab / 1–4 move between panels, Enter
// opens the selected agent or message in an overlay, y copies (OSC52), /
// filters, i or @ writes on the bus, r/F2 lists requests, ?/F1 shows every key,
// q quits. Themes: inherited dark (default), light, and NO_COLOR.
//
// Project is required: -p/--project or AGENT_BUS_PROJECT. -H/--host overrides
// REDIS_HOST. Flags: flags.go, one table for parsing and -h.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
	guardian "github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/internal/agentcaller"
	"github.com/netbja/agent-bus-monitor/projfiles"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/redis/go-redis/v9"
)

// streamKinds are the four streams busmon tails into the ACTIVITY feed and that
// --reset purges.
var streamKinds = []string{"status", "report", "notify", "cmd"}

// resolveLimit picks the ACTIVITY backfill size: an explicit --limit wins, else
// AGENT_BUS_BUSMON_LIMIT, else 25. A non-numeric env value falls through to 25.
// The result is the number of most-recent merged lines to replay on launch;
// 0 (or negative) means "replay all retained history" (the pre-limit behavior).
func resolveLimit(flagSet bool, flagVal int, env string) int {
	if flagSet {
		return flagVal
	}
	if n, err := strconv.Atoi(strings.TrimSpace(env)); env != "" && err == nil {
		return n
	}
	return 25
}

// confirmReset asks for interactive confirmation before --reset purges the
// project's streams. It accepts y/yes (case-insensitive); anything else —
// including EOF from a piped or non-TTY stdin — declines, so a purge is never
// triggered by an unattended pipe.
func confirmReset(project string, in io.Reader) bool {
	fmt.Printf("Purge the 4 stream histories for project '%s' (status/report/notify/cmd)? [y/N] ", project)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// confirmDelete asks before --delete erases a project. It shows what is about to
// go — key count, agent count, how recently the project was active — because the
// slug alone is a poor thing to judge on: "demo-2574" and "demo" look equally
// disposable until you see one of them was busy a minute ago.
//
// Same accept rule as confirmReset: y/yes, and EOF from a pipe declines.
// The prompt goes to an injectable writer (confirmReset predates this and prints
// straight to stdout): what it says IS the safety feature here, so it is worth a
// test.
func confirmDelete(s bus.ProjectSummary, keys int, now time.Time, out io.Writer, in io.Reader) bool {
	fmt.Fprintf(out, "Delete ALL %d %s for project '%s' (%d agents, last activity %s)?\n",
		keys, plural(keys, "key"), s.Project, s.Agents, lastSeen(s.LastTS, now))
	fmt.Fprint(out, "This is a DEL, not --reset: streams, consumer groups, agents/usage/budget/verdicts, "+
		"pilot and gate leases all go. [y/N] ")
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// deleteProject implements `busmon --delete <project>`. A project with no keys is
// a no-op success: that is the typo signal (you asked to delete 31 keys and were
// told there are none) without making a repeated cleanup script fail. files
// (nil: both options off) is where the project's files are, with the options
// on: what they would remove is printed before the prompt, and removed only
// after a clean delete (projects-admin §5.5). No backup is made here, so no
// archive is exempt.
func deleteProject(host, project string, yes bool, in io.Reader, now time.Time, removeServices func(project string) ([]string, error), files func() (projfiles.Options, error)) error {
	client, err := bus.Connect(host)
	if err != nil {
		return fmt.Errorf("Redis connection failed: %w", err)
	}
	defer client.Close()
	ctx := context.Background()

	keys, err := bus.ProjectKeys(ctx, client, project)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		fmt.Fprintf(os.Stderr, "no keys for project '%s' — nothing to delete\n", project)
		return reportRemovedServices(project, removeServices)
	}

	summary, err := bus.ProjectInfo(ctx, client, project)
	if err != nil {
		return err
	}
	var fo projfiles.Options
	filesOK := false
	if files != nil {
		var plan projfiles.Plan
		fo, err = files()
		if err == nil {
			plan, err = projfiles.PlanFor(fo)
		}
		fmt.Print(filesPlanText(project, plan, err))
		filesOK = err == nil
	}
	if !yes && !confirmDelete(summary, len(keys), now, os.Stdout, in) {
		fmt.Println("Cancelled.")
		return nil
	}

	n, kinds, left, err := deleteProjectNow(ctx, client, project, removeServices)
	printRemovedServices(project, kinds)
	if err != nil {
		return err
	}
	if len(left) > 0 {
		return fmt.Errorf("%s", cameBack(project, n, left))
	}
	fmt.Printf("Deleted %d %s for project '%s'.\n", n, plural(int(n), "key"), project)
	if filesOK {
		d := projfiles.Apply(fo)
		if d.Template != "" {
			fmt.Printf("Removed %s\n", d.Template)
		}
		for _, w := range d.Worktrees {
			fmt.Printf("Removed worktree %s\n", w)
		}
		for _, a := range d.Archives {
			fmt.Printf("Removed %s\n", a)
		}
		if len(d.Errs) > 0 {
			return fmt.Errorf("deleted %s, but %d %s could not be removed: %w", project, len(d.Errs), plural(len(d.Errs), "file"), errors.Join(d.Errs...))
		}
	}
	return nil
}

// filesPlanText is what --delete prints before its prompt with an option on:
// the panel's lines (§5.5).
func filesPlanText(project string, p projfiles.Plan, err error) string {
	if err != nil {
		return "also deleted (settings): ? (" + err.Error() + "): no file will be removed\n"
	}
	var b strings.Builder
	b.WriteString("also deleted (settings):\n")
	for _, l := range p.Lines(project) {
		b.WriteString("  " + l + "\n")
	}
	if p.Empty() {
		b.WriteString("  nothing found on disk\n")
	}
	return b.String()
}

// cameBack is the delete's truth when keys were written after its script
// (projects-admin §5.3): never reported clean.
func cameBack(project string, n int64, left []string) string {
	shown := left
	if len(shown) > 3 {
		shown = shown[:3]
	}
	return fmt.Sprintf("deleted %d %s of %s, but %d %s came back (%s): something of %s still runs; stop it, then delete again",
		n, plural(int(n), "key"), project, len(left), plural(len(left), "key"), strings.Join(shown, ", "), project)
}

// deleteProjectNow is --delete's core with no terminal I/O (projects-admin
// §5.3), the one path busmon --delete and the Projects page share: the
// project's services on this machine first (stopped, they write nothing more,
// and no unit is left to start a project that no longer exists), then every
// {p}:* key in the script that refuses a live project (bus.DeleteProjectIfQuiet),
// then the keys written meanwhile. A removal that fails deletes nothing.
func deleteProjectNow(ctx context.Context, client *redis.Client, project string, removeServices func(string) ([]string, error)) (int64, []string, []string, error) {
	if !bus.ValidName(project) { // before any removal: the services' env file is a path built from the name (reviewer-codex)
		return 0, nil, nil, fmt.Errorf("invalid project name %q", project)
	}
	kinds, err := removeLocalServices(project, removeServices)
	if err != nil {
		return 0, nil, nil, err
	}
	// The last check and the DEL in one script; then the keys that came back.
	n, left, err := bus.DeleteProjectIfQuiet(ctx, client, project)
	return n, kinds, left, deleteErr(project, n, kinds, err)
}

// deleteErr says what a failed delete did and did not do: never "nothing
// deleted" once the script deleted (foureyes), and the units removed before.
func deleteErr(project string, n int64, kinds []string, err error) error {
	var refused *bus.DeleteRefusal
	var rescan *bus.RescanError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &rescan): // the keys are gone
		return fmt.Errorf("deleted %d %s of %s, then could not scan again: %v", n, plural(int(n), "key"), project, rescan.Err)
	case errors.Is(err, bus.ErrOutcomeUnknown):
		return fmt.Errorf("%s: %w; busmon --list shows what remains%s", project, err, removedNote(kinds))
	case errors.As(err, &refused):
		return fmt.Errorf("%s: %w (nothing deleted%s)", project, err, removedNote(kinds))
	}
	return fmt.Errorf("%s: %w (nothing deleted%s)", project, err, removedNote(kinds)) // the scan before the script: nothing sent
}

// removedNote names the units removed before a delete that did not happen.
func removedNote(kinds []string) string {
	if len(kinds) == 0 {
		return ""
	}
	return "; its " + strings.Join(kinds, " and ") + " " + plural(len(kinds), "service") + " here was removed"
}

// removeLocalServices stops and removes the project's services on this
// machine (units and environment file; logs and data stay).
func removeLocalServices(project string, removeServices func(string) ([]string, error)) ([]string, error) {
	if removeServices == nil {
		return nil, nil
	}
	kinds, err := removeServices(project)
	if err != nil {
		return nil, fmt.Errorf("removing the services of %s: %w (nothing deleted; agentbus --project %s service remove)", project, err, project)
	}
	return kinds, nil
}

// reportRemovedServices removes the services and says so (a project with no
// keys left still has its units removed).
func reportRemovedServices(project string, removeServices func(string) ([]string, error)) error {
	kinds, err := removeLocalServices(project, removeServices)
	if err != nil {
		return err
	}
	printRemovedServices(project, kinds)
	return nil
}

func printRemovedServices(project string, kinds []string) {
	if len(kinds) > 0 {
		fmt.Printf("Stopped and removed the %s %s of '%s' on this machine.\n", strings.Join(kinds, " and "), plural(len(kinds), "service"), project)
	}
}

// localServices removes a project's units under this machine's manager.
func localServices(project string) ([]string, error) {
	return removeUnits(project, os.Getenv, func() (service.Manager, error) { return localManager(os.Getenv) })
}

// localManager is this machine's service manager: nil without an error only
// when the person chose none (service.Local).
func localManager(getenv func(string) string) (service.Manager, error) {
	return service.Local(runtime.GOOS, service.CommandRunner(service.ManagerBin(runtime.GOOS, getenv)), getenv)
}

// removeUnits removes project's units under detect's manager, then its
// environment file; a manager that cannot be checked is an error, so the
// delete stops with nothing deleted (reviewer-codex).
func removeUnits(project string, getenv func(string) string, detect func() (service.Manager, error)) ([]string, error) {
	m, err := detect()
	if err != nil {
		return nil, fmt.Errorf("cannot check the services of %s on this machine (%w)", project, err)
	}
	if m == nil {
		return nil, nil
	}
	units, err := m.List()
	if err != nil {
		return nil, err
	}
	paths, err := service.PathsFrom(getenv)
	if err != nil {
		return nil, err
	}
	var done []string
	for _, u := range units {
		if u.Project != project {
			continue
		}
		if err := m.Remove(u.Project, u.Kind); err != nil {
			return done, err
		}
		done = append(done, u.Kind)
	}
	if err := os.Remove(paths.EnvFile(project)); err != nil && !os.IsNotExist(err) {
		return done, err
	}
	return done, nil
}

// plural is the usual English -s. Only ever fed regular nouns here.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// listProjects writes the `busmon --list` table to out. A broker with no
// projects reports on stderr and leaves out untouched, so `busmon --list | wc -l`
// counts projects and nothing else.
func listProjects(host string, out io.Writer, now time.Time) error {
	client, err := bus.Connect(host)
	if err != nil {
		return fmt.Errorf("Redis connection failed: %w", err)
	}
	defer client.Close()

	list, err := bus.Projects(context.Background(), client)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(os.Stderr, "no projects on this bus yet")
		return nil
	}
	_, err = io.WriteString(out, projectTable(list, now))
	return err
}

const (
	staleAfter = 10 * time.Minute

	feedCap = 500 // ACTIVITY lines retained for display + selection

	pollBudget     = 3 * time.Second // deadline for ALL of one tick's reads
	tailRetry      = 2 * time.Second // pause before resuming the feed after a broker error
	requestPoll    = 5               // ticks between follow-up view refreshes
	wakePoll       = 5               // ticks between dispatcher-badge reads (Obligation/Wake/DispatchAlive)
	openThreadScan = 1000            // cmd entries scanned for unanswered exchanges and waiting replies
)

// agentState is what busmon knows about one agent, with the three facts the old
// chip conflated kept apart:
//
//	state/message/stateAt — the last state the agent DECLARED, and when.
//	lastSeen              — the last time it published anything at all. A report
//	                        proves the process is alive; it does not refresh the
//	                        declared state, so the two stamps differ on purpose.
//	armed                 — a live subscribe lease. A lease, not a state.
type agentState struct {
	delivered  string          // its cmd group's last-delivered id (the Requests view's unread/read)
	pendingIDs map[string]bool // its pending list
	deliveryOK bool            // both read on the last dispatcher-facts tick
	state      string
	message    string
	stateAt    time.Time         // when the state above was declared
	answeredAt time.Time         // qualifying human reply after a waiting declaration; display-only
	forYou     bool              // idle, and the message names the person (forYouHint): a hint, never a state
	lastSeen   time.Time         // last activity of any kind on the bus
	gated      int               // open 4-eyes challenges (flag "gate N")
	gates      map[string]string // those challenges, ref → "<challenger>|<summary>" (replaced whole, never mutated)
	armed      bool              // a live subscribe lease exists (flag "armed")
	lag        int64             // unconsumed {p}:cmd entries of the agent's group (detail only)
	pane       string            // HERDR_PANE_ID from the agents hash (detail only)
	waiting    int64             // wake-relevant messages (pending + unread): the queue column
	stale      int               // addressed entries older than the dispatch horizon (flag "stale N")
	gap        bool              // entries may have been trimmed unread (detail only)
	wake       *bus.WakeRecord   // the dispatcher's wake record, nil when none

	pid           int                 // AgentSnapshot.PID (0 = not recorded)
	pidStart      uint64              // AgentSnapshot.PIDStart
	queueKnown    bool                // an Obligation read has succeeded at least once
	queueFailed   bool                // the latest attempted Obligation read failed (or the broker is down)
	queueAt       time.Time           // time of the last successful Obligation read
	ctxRaw        string              // UsageSnapshot.Ctx exactly as read
	ctxFill       *bus.ContextFill    // {p}:context, nil when not published (unknown)
	bg            *bus.Background     // its background work (progress-line §4), nil when none
	bgFailed      bool                // the latest read of it failed: the row says "… ?"
	registered    bool                // listed in {p}:agents (the conductor only reports: not an @all target)
	provider      string              // whose quota it draws on (AGENT_BUS_PROVIDER at launch)
	client        string              // its client (claude, codex, kimi…), from the launcher
	identSession  string              // the session proved with its process (the U page: counted or not)
	held          bool                // its provider is held by the dispatcher (quota)
	openAsk       int                 // unanswered questions of its open ask
	askID         string              // that ask's id
	paused        bool                // the project is paused ({p}:pause holds a record)
	obsBusy       bool                // the dispatcher saw its pane running a turn ({p}:observed)
	auto          bool                // its state was flipped by its client's turn hooks, not declared (auto-state)
	idleHintAfter time.Duration       // display.idle_hint_after: idle? after this long seen ready (0 = off)
	obsState      string              // herdr's status of the pane at that inspection
	obsAt         time.Time           // when ({p}:observed at, Redis TIME); zero = not observed
	clientUpdate  *bus.ClientUpdate   // a newer client installed than this process runs ({p}:observed update)
	obsSince      time.Time           // first reading of that state (an episode, v0.15)
	obsPrompt     bool                // its pane was seen blocked, fresh: its client asks something
	swapping      bool                // agentbus swap is replacing it ({p}:swap:<name>, v0.15)
	reloading     bool                // agentbus reload holds the same intent, kind reload (lifecycle part C)
	retiring      bool                // retire --forget holds the same intent, kind retire (pop-auto-retire)
	popDoneAt     time.Time           // the dispatcher judged this pop done for the episode declared then; zero = not done
	handoff       bool                // a pending declared hand-off ({p}:handoff, lifecycle part A)
	trustFolder   string              // the folder its client's prompt asks to trust, not trusted yet (first-run-trust); "" = none or unknown
	fallback      *bus.FallbackRecord // it runs on a fallback profile ({p}:fallback, v0.15.1)
	model         *bus.ModelVerdict   // the last roles doctor verdict ({p}:doctor, v0.15.1)
	judgedAt      time.Time           // the shell's snapshot time (clock); zero in shared's own state
}

// shared is every piece of bus state the polling goroutines publish to the UI.
// One mutex covers all of it: the writers are two background goroutines, the
// reader is the shell's poll command (takeSnapshot, under the lock), and the whole snapshot is
// small enough that finer locking would only buy bugs.
type shared struct {
	mu sync.Mutex
	// The swap ledger read for the notices (busmon-notice): the cursor, and
	// what the polls read since the last pollMsg took it (takeSwapNews).
	swapCur        swapCursor
	swapNews       []bus.SwapEvent
	swapLost       bool
	person         string // busmon's identity, for the "for you" hint; constant
	agents         map[string]*agentState
	pilot          string                        // pilot-lease driver, "" = autonomous
	update         string                        // a newer release tag found by checkForUpdate, "" = none
	budgets        map[string]bus.BudgetSnapshot // account scope, per provider
	dispatch       string                        // "", "up" or "down"
	missions       string                        // "" (no conductor, nothing loaded), "up" or "down", or a service state
	svcInst        *bus.ServicesInstall          // the {p}:services marker (nil: no services)
	pause          *bus.PauseRecord              // the project pause (nil: not paused)
	pauseFailed    bool                          // the last pause read failed: pause holds the kept value
	holds          map[string]int64              // provider → reset (Unix s) of its quota hold
	asks           []bus.Ask                     // agents' questions for people
	personGates    map[string]string             // gates on the person (ref → meta), when the person is no agent
	pace           paceTracker                   // per provider window: rate of use, display only
	svcRecs        map[string]bus.ServiceRecord  // each service's last state
	board          map[string]bus.BoardEntry
	requests       []request            // tracked requests read from the board
	mergeQ         bus.MergeQueueRecord // the merge queue's cache (merge-queue), as last read
	mergeQRead     bool                 // the reader can read it: a row may say it is unknown
	mergeQErr      string               // the last read failed: why
	requestsReadAt time.Time            // when the read that gave requests began (a failed read keeps both)
	threads        []openThread         // cmd exchanges with no answer in retained history
	health         health               // the MONITOR's own link to the broker
	guardianHealth map[string]guardian.Record
	guardianNotes  map[string]guardian.Note
	guardianNow    time.Time     // Redis TIME at the last good read
	guardianNowAt  time.Time     // the local clock at that read: guardianNow advances from it
	configNote     string        // non-fatal user configuration issue
	idleHint       time.Duration // display.idle_hint_after, for every agent's idle? flag (auto-state)
	cmdHistory     []bus.Event   // bounded retained cmd sample used for waiting/reply correlation
	cmdOldest      time.Time     // oldest event in the retained sample; zero when none
	cmdHistoryOK   bool          // last bounded cmd read succeeded
	lastPollAt     time.Time     // last fully successful snapshot tick
	plan           planState     // the batch's plan (P), derived in pollOnce
	tok            tokState      // the U page's tokens and account history (tokens_view.go)
	tokOpen        bool          // the U page is open: only then are they read (a keyspace SCAN per read)
	tokWasOpen     bool          // it was open at the last poll: a read is due on each opening
	tokAttempt     int           // the tick of the last read attempt, success or not
	// trustOf is the folder a gated client asks about for a prompting agent
	// on this host (first-run-trust, trust_foryou.go); nil: no trust line.
	trustOf func(client string, pid int, start uint64) string
}

func main() {
	opts, set, err := parseFlags(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		os.Exit(2)
	}
	if opts.version {
		fmt.Println("busmon version " + buildinfo.Version)
		return
	}
	flags := settingsFlags{
		LimitSet: set["limit"], Limit: opts.limit, Theme: ifSet(set["theme"], opts.theme), As: ifSet(set["as"], opts.as),
	}
	settings := loadBusmonSettings(os.Getenv, flags)
	// --pick chooses the project, so like --list it must run before the
	// project-required exit, and before --reset and --delete: a picker never
	// purges or deletes, whatever else is on the line. It needs only the
	// theme, so a bad --as (it writes nothing) cannot stop it.
	if opts.pick {
		os.Exit(runPicker(opts.host, settings.Theme))
	}
	if err := settings.validateIdentity(); err != nil {
		fmt.Fprintf(os.Stderr, "error: --as: %v\n", err)
		os.Exit(1)
	}
	limit := settings.Limit
	ctxAlert = bus.ContextAlert(os.Getenv)
	if _, err := ResolveTheme(settings.Theme, "", "", false, 1<<24); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	// --list answers "which projects are on this bus?", so it MUST run before the
	// project-required exit below — that error is the very thing it exists to
	// resolve. It also wins over --reset: a read-only query never purges.
	if opts.list {
		if err := listProjects(opts.host, os.Stdout, time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// --delete names its own target, so like --list it must not require --project.
	// It also sits above --reset: purging a project's history on the way to
	// deleting the project would be work done twice and a confirmation asked twice.
	if why := refuseDestructive(context.Background(), opts, os.Getenv, agentcaller.Ancestor); why != "" {
		fmt.Fprintf(os.Stderr, "error: %s\n", why)
		os.Exit(1)
	}
	if opts.delete != "" {
		var files func() (projfiles.Options, error) // projects.delete_files and delete_backups (projects-admin §5.5)
		if settings.DeleteFiles || settings.DeleteBackups {
			host, _ := os.Hostname()
			files = func() (projfiles.Options, error) {
				o, err := projectFiles(opts.delete, os.Getenv, host)
				o.Files, o.Backups = settings.DeleteFiles, settings.DeleteBackups
				return o, err
			}
		}
		if err := deleteProject(opts.host, opts.delete, opts.yes, os.Stdin, time.Now(), localServices, files); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	project := opts.project
	if project == "" {
		project = os.Getenv("AGENT_BUS_PROJECT")
	}
	if project == "" {
		fmt.Fprint(os.Stderr, noProjectText())
		os.Exit(1)
	}
	// busmon writes as a person, never as an agent: --as, AGENT_BUS_HUMAN, else "pilot".
	self, err := humanIdentity(settings.Identity, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: --as: "+err.Error())
		os.Exit(1)
	}

	client, err := bus.Connect(opts.host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: Redis connection failed: %v\n", err)
		os.Exit(1)
	}
	b, err := bus.Open(client, project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	// --reset purges the project's streams before the TUI starts (terminal still
	// in normal mode for the confirmation prompt). XTRIM clears history but keeps
	// consumer groups + armed/pilot/gate leases.
	if opts.reset {
		if !opts.yes && !confirmReset(project, os.Stdin) {
			fmt.Println("Cancelled.")
			os.Exit(0)
		}
		removed, err := b.Purge(ctx, streamKinds)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: purge failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Purged: %d entries removed.\n", removed)
	}

	_, noColor := os.LookupEnv("NO_COLOR")
	t, err := ResolveTheme(settings.Theme, "", os.Getenv("COLORFGBG"), noColor, terminalColors())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	th = t

	// Everything the background goroutines publish to the shell. Empty until
	// the first tick: an unknown budget stays blank rather than implying 0%
	// used. The monitor's link starts healthy because bus.Connect just pinged it.
	st := &shared{
		person:     self,
		agents:     map[string]*agentState{},
		budgets:    map[string]bus.BudgetSnapshot{},
		board:      map[string]bus.BoardEntry{},
		health:     health{ok: true, since: time.Now()},
		configNote: settings.Note,
		idleHint:   settings.IdleHint,
		trustOf:    localTrustOf(project, os.Getenv),
	}
	cachePath := busmonCachePath(project)
	cache, cacheErr := loadBusmonCache(cachePath, project)
	if cacheErr != nil {
		st.configNote = joinNote(st.configNote, "local busmon cache unavailable")
	}
	tour, tourErr := loadTourMarker(tourMarkerPath()) // the machine's, not the project's: the panels are the same everywhere
	if tourErr != nil {
		st.configNote = joinNote(st.configNote, "tour marker unreadable")
	}
	var prior time.Time
	if cache != nil && cache.LastVisit > 0 {
		prior = time.UnixMilli(cache.LastVisit)
	}
	if settings.UpdateCheck {
		go checkForUpdate(ctx, st) // at most once a day; silent offline (updates.check, AGENT_BUS_NO_UPDATE_CHECK=1 turns it off)
	}

	// The Elm shell (busmon Charm spec): Update drives everything, View draws
	// the model; the poll runs in its commands, the tail is fed in below.
	shell := newShell(shellConfig{ctx: ctx, liveFrom: time.Now(), reader: b, w: b, st: st, project: project, self: self, attachDir: attachmentsDir(),
		settings: settings, cache: cache, cachePath: cachePath, priorVisit: prior, tour: tour,
		docker: realSvcRunner, about: realAboutSources(os.Getenv), brokerHost: opts.host, histDir: historyDirOf(project),
		getenv: os.Getenv, flags: flags, retheme: func(name string) (*Theme, error) { // as at the start, with the page's choice
			return ResolveTheme(name, "", os.Getenv("COLORFGBG"), noColor, terminalColors())
		},
		sleepRunner: realSleepRunner(agentbusBeside()), sleepLog: realSleepLog(os.Getenv), sleepWait: realSleepWait(os.Getenv),
		admin: adminSourceFor(client, os.Getenv), files: atSourceFor(project, os.Getenv)})
	prog := tea.NewProgram(shell, tea.WithContext(ctx))

	// Backfill, then live-tail. With a limit, fetch the last N merged entries up
	// front (fatal on error, while the terminal is still ours) and resume the
	// live tail from the per-stream cursors Recent returns — no replay, no "$"
	// gap. limit <= 0 keeps the original behavior: replay all retained history.
	cursors := map[string]string{}
	var backfill []bus.Event
	if limit > 0 {
		recent, cur, err := b.Recent(ctx, streamKinds, limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: backfill failed: %v\n", err)
			os.Exit(1)
		}
		backfill = recent
		for k, v := range cur {
			cursors[k] = v
		}
	}
	handle := func(e bus.Event) {
		cursors[bus.StreamKey(project, e.Kind)] = e.ID
		prog.Send(tailMsg{events: []bus.Event{e}}) // ingested in Update (ingestEvent)
	}
	go func() {
		if len(backfill) > 0 {
			prog.Send(tailMsg{events: backfill, backfill: true})
		}
		replayAll := limit <= 0 // no --limit: replay everything once…
		for {
			var err error
			if replayAll {
				replayAll = false
				err = b.Tail(ctx, "0", streamKinds, handle)
			} else {
				err = b.TailFrom(ctx, cursors, streamKinds, handle) // …then always from where we stopped
			}
			if ctx.Err() != nil {
				return
			}
			// Without this retry the feed dies quietly when the broker goes
			// away; handle keeps `cursors` current, so the resumed tail neither
			// replays nor skips.
			if err != nil {
				st.setHealth(err)
			}
			time.Sleep(tailRetry)
		}
	}()

	final, err := prog.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if fm, ok := final.(Model); ok && fm.exitNote != "" { // the open project deleted (projects-admin §5.2.3)
		fmt.Println(fm.exitNote)
	}
}

// terminalColors is the terminal's colour depth for ResolveTheme: true colour,
// 256, 16, or none (not a terminal, or NO_COLOR-like environments).
func terminalColors() int {
	switch colorprofile.Detect(os.Stdout, os.Environ()) {
	case colorprofile.TrueColor:
		return 1 << 24
	case colorprofile.ANSI256:
		return 256
	case colorprofile.ANSI:
		return 16
	}
	return 0
}

// noProjectText is what busmon says without a project (stranger-test): how to
// name one, and how to find one.
func noProjectText() string {
	return "error: no project: busmon --project <p> (or AGENT_BUS_PROJECT)\n" +
		"  busmon --list   the broker's projects\n" +
		"  busmon --pick   choose one and open it\n"
}

// refuseDestructive refuses --delete and --reset to an agent caller
// (projects-admin §5.4): the shared rule agentbus suspend and resume run.
// Defence in depth, not a boundary: a double fork with the variable cleared
// escapes it.
func refuseDestructive(ctx context.Context, o busmonOpts, getenv func(string) string, ancestor func(context.Context) (string, error)) string {
	switch {
	case o.delete != "":
		return agentcaller.Refuse(ctx, getenv, ancestor, "deleting a project", "busmon")
	case o.reset:
		return agentcaller.Refuse(ctx, getenv, ancestor, "purging a project's streams", "busmon")
	}
	return ""
}
