package personmem

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/mission"
)

// Limits (spec §4.3.5).
const (
	MaxQuote = 600 // runes, health.SafeText's bound
	MaxObs   = 200 // per topic: past that, the person splits it
)

// ErrRefused wraps every refusal of Remember: nothing was written.
var ErrRefused = errors.New("not remembered")

func refuse(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrRefused}, a...)...)
}

// Store is the person's memory on this host.
type Store struct {
	Dir string
	git mission.Git
}

// Open is the store at ${XDG_DATA_HOME:-~/.local/share}/agentbus/memory/person
// (spec §4.1); nothing is created until the first write.
func Open(getenv func(string) string) (*Store, error) {
	dir := filepath.Join(config.DataHome(getenv), "agentbus", "memory", "person")
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("no home to keep the memory in (%s)", dir)
	}
	return &Store{Dir: dir, git: mission.Git{Repo: dir}}, nil
}

// Topics are the topics on disk, sorted; none when the store does not exist.
func (s *Store) Topics() ([]string, error) {
	es, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		if name, ok := strings.CutSuffix(e.Name(), ".md"); ok && e.Type().IsRegular() {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) path(topic string) string { return filepath.Join(s.Dir, topic+".md") }

// Load reads one topic; os.ErrNotExist when it has no file.
func (s *Store) Load(topic string) (*Topic, error) {
	if !ValidTopic(topic) {
		return nil, fmt.Errorf("%q is not a topic name", topic)
	}
	b, err := os.ReadFile(s.path(topic))
	if err != nil {
		return nil, err
	}
	t, err := Parse(string(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.path(topic), err)
	}
	return t, nil
}

// loadAll reads every topic that parses, with each one's error.
func (s *Store) loadAll() (map[string]*Topic, map[string]error) {
	names, _ := s.Topics()
	ts, errs := map[string]*Topic{}, map[string]error{}
	for _, n := range names {
		t, err := s.Load(n)
		if err != nil {
			errs[n] = err
			continue
		}
		ts[n] = t
	}
	return ts, errs
}

// Request is one remember (spec §4.2): an observation and its filing.
type Request struct {
	Topic, Type, Importance string
	From                    Source // the person's typed entry
	Busmon                  bool   // instead of From: typed in busmon's Memory page, by By at At
	At                      time.Time
	By                      string // the person (busmon's source)
	Quote                   string
	Answers                 *Source // the proposal replied to, and its words
	AnswersQuote            string
	Supersedes              string
	KeepBoth                bool
	Kind                    string
	Relates                 []Relation
}

// Remember adds one observation after the checks of spec §4.3, as one git
// commit; the same source and quote again is the same observation (no
// commit). A refusal wraps ErrRefused and writes nothing; it names the
// field, never a secret's value.
func (s *Store) Remember(ctx context.Context, rd Reader, r Request) (Observation, error) {
	if err := r.validate(); err != nil {
		return Observation{}, err
	}
	o := Observation{Type: r.Type, Importance: r.Importance, Quote: strings.TrimSpace(r.Quote), Supersedes: r.Supersedes}
	var err error
	if o, err = r.source(ctx, rd, o); err != nil {
		return Observation{}, err
	}
	for _, f := range []struct{ name, v string }{{"the quote", o.Quote}, {"the question", o.Question}, {"the topic", r.Topic}} {
		if health.Sensitive(f.v) {
			return Observation{}, refuse("%s holds a credential shape: nothing written", f.name)
		}
	}
	for _, rel := range r.Relates {
		if health.Sensitive(rel.Topic) {
			return Observation{}, refuse("a relation holds a credential shape: nothing written")
		}
	}

	// One writer at a time, from the read to the commit: busmon's page and
	// the CLI may write together, and neither may lose the other's line.
	unlock, err := s.lock()
	if err != nil {
		return Observation{}, err
	}
	defer unlock()
	// Every file must parse: a hand-edited one is never written over, and
	// ids and supersedes are the whole memory's (reviewer-codex).
	all, errs := s.loadAll()
	if len(errs) > 0 {
		n := slices.Sorted(maps.Keys(errs))[0]
		return Observation{}, refuse("%s.md does not parse (%v): fix it first (agentbus mem person check)", n, errs[n])
	}
	t := all[r.Topic]
	if t == nil {
		t = &Topic{Topic: r.Topic, Kind: or(r.Kind, "other")}
	}
	base := o.ID
	for n := 2; ; n++ { // the same source and words, in any topic: already there
		x, ok := findObs(all, o.ID)
		if !ok {
			break
		}
		if x.Quote == o.Quote {
			return x, nil
		}
		o.ID = base + "-" + strconv.Itoa(n) // another statement of the same entry
	}
	if len(t.Obs) >= MaxObs {
		return Observation{}, refuse("%s holds %d observations, the most a topic keeps: split it", r.Topic, MaxObs)
	}
	if o.Supersedes != "" && !hasObs(all, o.Supersedes) {
		return Observation{}, refuse("supersedes %s: no such observation", o.Supersedes)
	}
	if o.Type == "decision" && o.Supersedes == "" && !r.KeepBoth {
		if old := activeDecision(t, all); old != "" {
			return Observation{}, refuse("%s already holds the decision %s: name --supersedes %s or --keep-both, after asking the person which holds", r.Topic, old, old)
		}
	}
	if r.Kind != "" {
		t.Kind = r.Kind
	}
	for _, rel := range r.Relates {
		if !slices.Contains(t.Relations, rel) {
			t.Relations = append(t.Relations, rel)
		}
	}
	t.Obs = append(t.Obs, o)
	if err := s.write(ctx, t, "remember "+r.Topic+": "+o.ID); err != nil {
		return Observation{}, err
	}
	return o, nil
}

// validate checks the filing and the quote's shape.
func (r Request) validate() error {
	switch {
	case !ValidTopic(r.Topic):
		return refuse("the topic must match %s", topicRE)
	case !slices.Contains(Types, r.Type):
		return refuse("the type is one of %s", strings.Join(Types, ", "))
	case !slices.Contains(Importances, r.Importance):
		return refuse("the importance is one of %s", strings.Join(Importances, ", "))
	case r.Kind != "" && !slices.Contains(Kinds, r.Kind):
		return refuse("the kind is one of %s", strings.Join(Kinds, ", "))
	case r.Supersedes != "" && r.KeepBoth:
		return refuse("--supersedes and --keep-both say opposite things")
	}
	for _, rel := range r.Relates {
		if !slices.Contains(RelTypes, rel.Type) || !ValidTopic(rel.Topic) {
			return refuse("a relation is one of %s, then a topic name", strings.Join(RelTypes, ", "))
		}
	}
	return quoteShape("the quote", r.Quote)
}

func quoteShape(name, q string) error {
	q = strings.TrimSpace(q)
	switch {
	case q == "":
		return refuse("%s is empty", name)
	case len([]rune(q)) > MaxQuote:
		return refuse("%s is %d characters, at most %d", name, len([]rune(q)), MaxQuote)
	case strings.ContainsFunc(q, func(c rune) bool { return unicode.IsControl(c) || unicode.Is(unicode.Cf, c) }):
		return refuse("%s holds a control character", name)
	}
	return nil
}

// source fills the observation's id, said, and question from where the
// person's words come from (spec §4.3.1, §4.3.2).
func (r Request) source(ctx context.Context, rd Reader, o Observation) (Observation, error) {
	if r.Busmon {
		at := r.At.UTC()
		if !validPerson(r.By) {
			return o, refuse("busmon names no person")
		}
		o.ID = at.Format("20060102") + "-b" + strconv.FormatInt(at.UnixMilli(), 10)
		o.Said = at.Format("2006-01-02") + " by " + r.By + " from busmon"
		return o, nil
	}
	if rd == nil {
		return o, refuse("no reader of the bus")
	}
	e, ok, err := rd.Entry(ctx, r.From)
	switch {
	case err != nil:
		return o, fmt.Errorf("reading %s: %w", r.From, err)
	case !ok:
		return o, refuse("no entry %s in its stream or its history", r.From)
	case !e.Typed:
		return o, refuse("%s is not text the person typed (no typed=yes): only the person's own words are remembered", r.From)
	}
	text := e.Body
	if e.Said != "" { // an ask's answer: the answer alone, never "Answer to <question>"
		text = e.Said
	}
	if !strings.Contains(collapse(text), collapse(o.Quote)) {
		return o, refuse("the quote is not in the person's text of %s", r.From)
	}
	at := entryTime(r.From.ID)
	o.ID = at.Format("20060102") + "-" + r.From.ID
	o.Said = fmt.Sprintf("%s by %s, project %s, entry %s", at.Format("2006-01-02"), e.From, r.From.Project, r.From.ID)
	switch {
	case r.Answers != nil:
		if err := quoteShape("the question", r.AnswersQuote); err != nil {
			return o, err
		}
		q, ok, err := rd.Entry(ctx, *r.Answers)
		switch {
		case err != nil:
			return o, fmt.Errorf("reading %s: %w", *r.Answers, err)
		case !ok:
			return o, refuse("no entry %s in its stream or its history", *r.Answers)
		case !strings.Contains(collapse(or(q.Full, q.Body)), collapse(r.AnswersQuote)):
			return o, refuse("the --answers quote is not in the question's text of %s", *r.Answers)
		}
		o.Answers = r.Answers.String() + " by " + q.From
		o.Question = strings.TrimSpace(r.AnswersQuote)
	case e.Said != "":
		o.Question = askHeader(e.Body)
	}
	return o, nil
}

// askHeader is the question an answer's line names: Answer to "<header>": ...
func askHeader(body string) string {
	rest, ok := strings.CutPrefix(body, "Answer to ")
	if !ok {
		return ""
	}
	q, err := strconv.QuotedPrefix(rest)
	if err != nil {
		return ""
	}
	h, _ := strconv.Unquote(q)
	return h
}

func validPerson(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) })
}

// collapse is s with every run of white space one space: quotes compare
// this way, case kept (spec §4.3.2).
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func hasObs(all map[string]*Topic, id string) bool {
	_, ok := findObs(all, id)
	return ok
}

// findObs is the observation id, in any topic.
func findObs(all map[string]*Topic, id string) (Observation, bool) {
	for _, t := range all {
		for _, o := range t.Obs {
			if o.ID == id {
				return o, true
			}
		}
	}
	return Observation{}, false
}

// superseded are the ids some observation supersedes.
func superseded(all map[string]*Topic) map[string]bool {
	out := map[string]bool{}
	for _, t := range all {
		for _, o := range t.Obs {
			if o.Supersedes != "" {
				out[o.Supersedes] = true
			}
		}
	}
	return out
}

// activeDecision is t's newest decision nothing supersedes ("" none).
func activeDecision(t *Topic, all map[string]*Topic) string {
	gone := superseded(all)
	for i := len(t.Obs) - 1; i >= 0; i-- {
		if o := t.Obs[i]; o.Type == "decision" && !gone[o.ID] {
			return o.ID
		}
	}
	return ""
}

// private makes the store's directories, and keeps them 0700 even when they
// existed with a looser mode (reviewer-codex): person/ and memory/ are the
// store's own; agentbus/ is shared and left as it is, a 0700 person/
// already closing everything under it.
func (s *Store) private() error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	for _, d := range []string{s.Dir, filepath.Dir(s.Dir)} {
		if err := os.Chmod(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// lock takes the store's lock, waiting for another writer: an exclusive
// flock on .lock in the store (made 0700, the file 0600; git leaves it
// untracked).
func (s *Store) lock() (func(), error) {
	if err := s.private(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// write saves t and commits it: the repository is made on the first write
// (private: 0700, the files 0600), each write one commit.
func (s *Store) write(ctx context.Context, t *Topic, msg string) error {
	if err := s.private(); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(s.Dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := s.git.Run(ctx, "init", "-q"); err != nil {
			return err
		}
	}
	tmp := s.path(t.Topic) + ".tmp"
	if err := os.WriteFile(tmp, []byte(t.Format()), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path(t.Topic)); err != nil {
		os.Remove(tmp)
		return err
	}
	if _, err := s.git.Run(ctx, "add", "--", t.Topic+".md"); err != nil {
		return err
	}
	_, err := s.git.Run(ctx, "-c", "user.name=agentbus", "-c", "user.email=agentbus@localhost", "-c", "commit.gpgsign=false",
		"commit", "-q", "--no-verify", "-m", msg, "--", t.Topic+".md")
	return err
}
