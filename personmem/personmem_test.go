package personmem

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Spec 2026-11-21-person-memory-design r2, step 2: the person's facts as one
// Markdown file per topic in a host-level git repository, written only from
// the person's typed words (§4.3), ranked by recall (§3) and checked (§4.2).

// fakeReader holds entries by "<project>:<id>".
type fakeReader map[string]Entry

func (f fakeReader) Entry(_ context.Context, s Source) (Entry, bool, error) {
	e, ok := f[s.Project+":"+s.ID]
	return e, ok, nil
}

const (
	dirID    = "1795205976983-0" // the person's typed directive
	propID   = "1795205970000-0" // master's proposal
	ansID    = "1795205980000-0" // an ask's answer
	resumeID = "1795205990000-0" // resume-all's summary: human=yes, not typed
)

func reader() fakeReader {
	return fakeReader{
		"demo:" + dirID:    {Kind: "cmd", Type: "directive", From: "charlie", Body: "yes,  dev first\nand main only by promotion", Human: true, Typed: true},
		"demo:" + propID:   {Kind: "report", From: "master", Body: "PRs to dev?", Full: "Should PRs go to dev, with main taking only promotions?"},
		"demo:" + ansID:    {Kind: "cmd", Type: "reply", From: "charlie", Body: `Answer to "Language": Any code`, Human: true, Typed: true, Said: "Any code"},
		"demo:" + resumeID: {Kind: "cmd", Type: "directive", From: "charlie", Body: "3 agents resumed after a crash", Human: true},
		"demo:1-0":         {Kind: "cmd", Type: "directive", From: "master", Body: "do it"},
	}
}

// store is a Store under a fresh XDG_DATA_HOME: no test writes the real one.
func store(t *testing.T) *Store {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s, err := Open(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.Dir, os.Getenv("XDG_DATA_HOME")) || !strings.HasSuffix(s.Dir, filepath.Join("agentbus", "memory", "person")) {
		t.Fatalf("dir %s", s.Dir)
	}
	return s
}

func decision(topic, from, quote string) Request {
	return Request{Topic: topic, Type: "decision", Importance: "critical", From: Source{Project: "demo", ID: from}, Quote: quote, By: "charlie"}
}

func commits(t *testing.T, s *Store) int {
	t.Helper()
	out, err := exec.Command("git", "-C", s.Dir, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range strings.TrimSpace(string(out)) {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestRememberWritesATopicFileAndACommit(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	r := decision("merge-policy", dirID, "yes, dev first") // whitespace collapsed: "yes,  dev first" holds it
	r.Answers, r.AnswersQuote = &Source{Project: "demo", ID: propID}, "main taking only promotions"
	r.Kind, r.Relates = "practice", []Relation{{Type: "applies-to", Topic: "project-demo"}}
	o, err := s.Remember(ctx, reader(), r)
	if err != nil {
		t.Fatal(err)
	}
	if o.ID != "20261120-"+dirID || o.Question != "main taking only promotions" || !strings.Contains(o.Answers, "demo:"+propID+" by master") {
		t.Fatalf("observation %+v", o)
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, "merge-policy.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"topic: merge-policy", "kind: practice", "  - applies-to: project-demo", "# merge-policy",
		"- id: 20261120-" + dirID, "  type: decision", "  importance: critical", `  quote: "yes, dev first"`,
		"  said: 2026-11-20 by charlie, project demo, entry " + dirID, `  question: "main taking only promotions"`} {
		if !strings.Contains(string(b), w) {
			t.Errorf("file lacks %q:\n%s", w, b)
		}
	}
	if commits(t, s) != 1 {
		t.Fatalf("%d commits, want 1", commits(t, s))
	}
	// The same entry and quote again: one observation, no commit.
	if _, err := s.Remember(ctx, reader(), r); err != nil {
		t.Fatal(err)
	}
	tp, _ := s.Load("merge-policy")
	if len(tp.Obs) != 1 || commits(t, s) != 1 {
		t.Fatalf("stored twice: %d observations, %d commits", len(tp.Obs), commits(t, s))
	}
	// The file reads back as written.
	if again := tp.Format(); again != string(b) {
		t.Fatalf("round trip:\n%s\n---\n%s", b, again)
	}
}

// Only the person's typed words: an agent's entry, a composed one
// (human=yes without typed=yes) and a quote not in the text are refused,
// and nothing is written.
func TestRememberRefusesWhatThePersonDidNotType(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	for _, c := range []struct {
		name string
		r    Request
		want string
	}{
		{"an agent's entry", decision("x", "1-0", "do it"), "not text the person typed"},
		{"a composed entry", decision("x", resumeID, "3 agents resumed"), "not text the person typed"},
		{"a missing entry", decision("x", "9-9", "yes"), "no entry demo:9-9"},
		{"a quote not in the text", decision("x", dirID, "dev last"), "not in the person's text"},
		{"the question part of an answer", decision("x", ansID, "Language"), "not in the person's text"},
		{"an oversized quote", decision("x", dirID, strings.Repeat("a", 601)), "at most 600"},
		{"a bad topic", decision("Bad Topic", dirID, "yes"), "topic"},
		{"a bad type", Request{Topic: "x", Type: "guess", Importance: "minor", From: Source{"demo", dirID}, Quote: "yes", By: "charlie"}, "type"},
		{"a bad importance", Request{Topic: "x", Type: "fact", Importance: "0.9", From: Source{"demo", dirID}, Quote: "yes", By: "charlie"}, "importance"},
	} {
		_, err := s.Remember(ctx, reader(), c.r)
		if err == nil || !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if ts, _ := s.Topics(); len(ts) != 0 || commits(t, s) != 0 {
		t.Fatalf("a refusal wrote: %v, %d commits", ts, commits(t, s))
	}
}

// An ask's answer: the quote comes from said, and the ask's question header
// is stored as the question when --answers is not given.
func TestRememberAnAnswer(t *testing.T) {
	s := store(t)
	o, err := s.Remember(context.Background(), reader(), Request{Topic: "language", Type: "preference", Importance: "standard",
		From: Source{"demo", ansID}, Quote: "Any code", By: "charlie"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Question != "Language" || o.Quote != "Any code" {
		t.Fatalf("%+v", o)
	}
}

// No secret is written: a credential-shaped quote, question or relation is
// refused, naming the field and never the value.
func TestRememberRefusesASecret(t *testing.T) {
	s := store(t)
	rd := reader()
	rd["demo:5-0"] = Entry{Kind: "cmd", Type: "directive", Body: "use password=hunter2-long", Human: true, Typed: true}
	rd["demo:6-0"] = Entry{Kind: "report", From: "master", Body: "token: abcdefgh12345"}
	ctx := context.Background()
	_, err := s.Remember(ctx, rd, decision("x", "5-0", "password=hunter2-long"))
	if err == nil || !strings.Contains(err.Error(), "the quote") || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("secret quote: %v", err)
	}
	r := decision("x", dirID, "yes, dev first")
	r.Answers, r.AnswersQuote = &Source{"demo", "6-0"}, "token: abcdefgh12345"
	if _, err := s.Remember(ctx, rd, r); err == nil || !strings.Contains(err.Error(), "the question") {
		t.Fatalf("secret question: %v", err)
	}
	r = decision("x", dirID, "yes, dev first")
	r.Answers, r.AnswersQuote = &Source{"demo", "7-7"}, "x"
	if _, err := s.Remember(ctx, rd, r); err == nil || !strings.Contains(err.Error(), "no entry demo:7-7") {
		t.Fatalf("missing answers entry: %v", err)
	}
	r.Answers, r.AnswersQuote = &Source{"demo", propID}, "not there"
	if _, err := s.Remember(ctx, rd, r); err == nil || !strings.Contains(err.Error(), "not in the question's text") {
		t.Fatalf("answers quote not in its body: %v", err)
	}
	if commits(t, s) != 0 {
		t.Fatal("a refusal committed")
	}
}

// A second active decision on a topic needs --supersedes or --keep-both;
// the refusal names the old one.
func TestRememberDecisionThatMayReplaceAnother(t *testing.T) {
	s := store(t)
	rd := reader()
	rd["demo:7-0"] = Entry{Kind: "cmd", Type: "directive", Body: "main first after all", Human: true, Typed: true}
	ctx := context.Background()
	first, err := s.Remember(ctx, rd, decision("merge-policy", dirID, "yes, dev first"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remember(ctx, rd, decision("merge-policy", "7-0", "main first")); err == nil || !strings.Contains(err.Error(), first.ID) {
		t.Fatalf("second decision: %v", err)
	}
	r := decision("merge-policy", "7-0", "main first")
	r.Supersedes = "nope"
	if _, err := s.Remember(ctx, rd, r); err == nil || !strings.Contains(err.Error(), "supersedes") {
		t.Fatalf("dangling supersedes: %v", err)
	}
	r.Supersedes = first.ID
	if _, err := s.Remember(ctx, rd, r); err != nil {
		t.Fatal(err)
	}
	if commits(t, s) != 2 {
		t.Fatalf("%d commits", commits(t, s))
	}
}

// The person typing in busmon is a source too, said "from busmon".
func TestRememberFromBusmon(t *testing.T) {
	s := store(t)
	o, err := s.Remember(context.Background(), nil, Request{Topic: "notifications", Type: "preference", Importance: "minor",
		Busmon: true, At: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC), Quote: "no sound at night", By: "charlie"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(o.Said, "by charlie from busmon") || !strings.HasPrefix(o.ID, "20261121-b") {
		t.Fatalf("%+v", o)
	}
}

// recall: exact topic, partial topic, type, then quote; ties by importance,
// then the newest; superseded hidden unless all.
func TestRecallOrder(t *testing.T) {
	s := store(t)
	rd := reader()
	ctx := context.Background()
	add := func(topic, typ, imp, id, body string) Observation {
		t.Helper()
		rd["demo:"+id] = Entry{Kind: "cmd", Type: "directive", Body: body, Human: true, Typed: true}
		o, err := s.Remember(ctx, rd, Request{Topic: topic, Type: typ, Importance: imp, From: Source{"demo", id}, Quote: body, By: "charlie", KeepBoth: true})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	inQuote := add("tools", "fact", "critical", "100-0", "the dev branch first")
	exact := add("dev", "fact", "minor", "200-0", "anything")
	partial := add("dev-flow", "fact", "minor", "300-0", "anything else")
	newer := add("tools", "fact", "critical", "400-0", "dev again, newer")
	got := s.Recall(Query{Words: "dev"})
	var ids []string
	for _, o := range got {
		ids = append(ids, o.ID)
	}
	want := []string{exact.ID, partial.ID, newer.ID, inQuote.ID}
	if strings.Join(ids, " ") != strings.Join(want, " ") {
		t.Fatalf("order %v, want %v", ids, want)
	}
	// Same tier: importance before newness (a critical old fact outranks a
	// minor newer one).
	minorNew := add("style", "fact", "minor", "600-0", "short replies")
	critOld := add("style", "fact", "critical", "50-0", "short answers please")
	if got := s.Recall(Query{Words: "short"}); len(got) != 2 || got[0].ID != critOld.ID || got[1].ID != minorNew.ID {
		t.Fatalf("importance tie-break: %v", got)
	}
	// Superseded: hidden without all.
	rd["demo:500-0"] = Entry{Kind: "cmd", Type: "directive", Body: "dev replaced", Human: true, Typed: true}
	r := Request{Topic: "dev", Type: "fact", Importance: "minor", From: Source{"demo", "500-0"}, Quote: "dev replaced", By: "charlie", Supersedes: exact.ID}
	if _, err := s.Remember(ctx, rd, r); err != nil {
		t.Fatal(err)
	}
	for _, o := range s.Recall(Query{Words: "dev"}) {
		if o.ID == exact.ID {
			t.Fatal("a superseded observation was recalled")
		}
	}
	found := false
	for _, o := range s.Recall(Query{Words: "dev", All: true}) {
		found = found || o.ID == exact.ID
	}
	if !found || len(s.Recall(Query{Words: "dev", Limit: 2})) != 2 || len(s.Recall(Query{Type: "decision"})) != 0 {
		t.Fatal("all, limit or type filter")
	}
	if len(s.Recall(Query{Project: "other"})) != 0 || len(s.Recall(Query{Project: "demo"})) == 0 {
		t.Fatal("project filter")
	}
}

// check names a dangling supersedes, a bad id and a secret a hand edit
// added.
func TestCheckFindsHandEdits(t *testing.T) {
	s := store(t)
	if _, err := s.Remember(context.Background(), reader(), decision("merge-policy", dirID, "yes, dev first")); err != nil {
		t.Fatal(err)
	}
	if fs := s.Check(); len(fs) != 0 {
		t.Fatalf("clean store: %v", fs)
	}
	path := filepath.Join(s.Dir, "merge-policy.md")
	b, _ := os.ReadFile(path)
	text := string(b) + "\n- id: not-an-id\n  type: fact\n  importance: minor\n  said: 2026-11-20 by charlie from busmon\n  quote: \"api_key=abcdef123456\"\n  supersedes: 20200101-1-0\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(findingTexts(s.Check()), "\n")
	for _, w := range []string{"bad id", "supersedes 20200101-1-0", "credential"} {
		if !strings.Contains(all, w) {
			t.Errorf("check lacks %q:\n%s", w, all)
		}
	}
	if strings.Contains(all, "abcdef123456") {
		t.Error("check printed the secret")
	}
}

func findingTexts(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.String())
	}
	return out
}

// reviewer-codex on fae1939c: ids are the memory's, not a topic's. The same
// source and quote in another topic is the same observation; another quote
// of the same entry gets -2 whatever its topic; check finds an id in two
// files.
func TestIdsAreGlobal(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	rd := reader()
	a, err := s.Remember(ctx, rd, Request{Topic: "merge-policy", Type: "fact", Importance: "minor", From: Source{"demo", dirID}, Quote: "yes, dev first", By: "charlie"})
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.Remember(ctx, rd, Request{Topic: "branches", Type: "fact", Importance: "minor", From: Source{"demo", dirID}, Quote: "yes, dev first", By: "charlie"})
	if err != nil || same.ID != a.ID || commits(t, s) != 1 {
		t.Fatalf("same source and quote in another topic: %+v %v, %d commits", same, err, commits(t, s))
	}
	if ts, _ := s.Topics(); len(ts) != 1 {
		t.Fatalf("a topic was made for a duplicate: %v", ts)
	}
	other, err := s.Remember(ctx, rd, Request{Topic: "branches", Type: "fact", Importance: "minor", From: Source{"demo", dirID}, Quote: "main only by promotion", By: "charlie"})
	if err != nil || other.ID != a.ID+"-2" {
		t.Fatalf("another quote of the entry in another topic: %+v %v", other, err)
	}
	// A hand edit copying an id into another file is found.
	b, _ := os.ReadFile(filepath.Join(s.Dir, "merge-policy.md"))
	if err := os.WriteFile(filepath.Join(s.Dir, "copy.md"), []byte(strings.ReplaceAll(string(b), "merge-policy", "copy")), 0o600); err != nil {
		t.Fatal(err)
	}
	if all := strings.Join(findingTexts(s.Check()), "\n"); !strings.Contains(all, "also in") {
		t.Fatalf("an id in two files not found:\n%s", all)
	}
}

// A topic file that no longer parses (a hand edit) is never written over:
// remember refuses, and the file stays byte for byte.
func TestRememberKeepsAnUnparsableTopic(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	if _, err := s.Remember(ctx, reader(), decision("merge-policy", dirID, "yes, dev first")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir, "merge-policy.md")
	broken := "---\ntopic: merge-policy\nkind: other\n---\n# merge-policy\n\nhand-written prose\n"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	rd := reader()
	rd["demo:8-0"] = Entry{Kind: "cmd", Type: "directive", Body: "keep it", Human: true, Typed: true}
	_, err := s.Remember(ctx, rd, Request{Topic: "merge-policy", Type: "fact", Importance: "minor", From: Source{"demo", "8-0"}, Quote: "keep it", By: "charlie"})
	if err == nil || !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("remember over an unparsable topic: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != broken {
		t.Fatalf("the file changed:\n%s", b)
	}
}

// The person's memory is private (foureyes on fae1939c): the directories
// remember makes are 0700, a topic file 0600.
func TestStoreIsPrivate(t *testing.T) {
	s := store(t)
	if _, err := s.Remember(context.Background(), reader(), decision("merge-policy", dirID, "yes, dev first")); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{s.Dir, filepath.Dir(s.Dir), filepath.Dir(filepath.Dir(s.Dir))} {
		if st, err := os.Stat(d); err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, st.Mode().Perm(), err)
		}
	}
	if st, err := os.Stat(filepath.Join(s.Dir, "merge-policy.md")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("topic file: %v %v", st.Mode().Perm(), err)
	}
}

// A topic holds at most MaxObs observations; the next is refused (spec
// §4.3.5: the person splits the topic).
func TestTopicBound(t *testing.T) {
	s := store(t)
	tp := &Topic{Topic: "big", Kind: "other"}
	for i := range MaxObs {
		tp.Obs = append(tp.Obs, Observation{ID: "20261120-" + strconv.Itoa(1000+i) + "-0", Type: "fact", Importance: "minor", Said: "2026-11-20 by charlie from busmon", Quote: "q" + strconv.Itoa(i)})
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "big.md"), []byte(tp.Format()), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Remember(context.Background(), reader(), Request{Topic: "big", Type: "fact", Importance: "minor", From: Source{"demo", dirID}, Quote: "yes, dev first", By: "charlie"})
	if err == nil || !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "split it") {
		t.Fatalf("observation %d: %v", MaxObs+1, err)
	}
}

// The quote's case is kept: "Yes, Dev First" is not the person's "yes, dev
// first".
func TestQuoteKeepsCase(t *testing.T) {
	s := store(t)
	if _, err := s.Remember(context.Background(), reader(), decision("x", dirID, "Yes, Dev First")); err == nil || !strings.Contains(err.Error(), "not in the person's text") {
		t.Fatalf("case-changed quote: %v", err)
	}
}

// Two writers at once (busmon's page and the CLI) lose nothing: remember
// holds the store's lock from its read to its commit.
func TestRememberConcurrently(t *testing.T) {
	s := store(t)
	rd := reader()
	const n = 8
	for i := range n {
		rd["demo:"+strconv.Itoa(2000+i)+"-0"] = Entry{Kind: "cmd", Type: "directive", Body: "fact " + strconv.Itoa(i), Human: true, Typed: true}
	}
	errs := make(chan error, n)
	for i := range n {
		go func() {
			_, err := s.Remember(context.Background(), rd, Request{Topic: "many", Type: "fact", Importance: "minor",
				From: Source{"demo", strconv.Itoa(2000+i) + "-0"}, Quote: "fact " + strconv.Itoa(i), By: "charlie"})
			errs <- err
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	tp, err := s.Load("many")
	if err != nil || len(tp.Obs) != n || commits(t, s) != n {
		t.Fatalf("%d observations, %d commits: %v", len(tp.Obs), commits(t, s), err)
	}
}

// A store whose directories already exist with a looser mode is made
// private on the next write: person/ and memory/ 0700, the store's own
// (reviewer-codex on fe93e0bf; agentbus/ is shared and left as it is, a
// 0700 person/ already closes everything under it).
func TestStoreTightensAnExistingDirectory(t *testing.T) {
	s := store(t)
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{s.Dir, filepath.Dir(s.Dir)} {
		if err := os.Chmod(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Remember(context.Background(), reader(), decision("merge-policy", dirID, "yes, dev first")); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{s.Dir, filepath.Dir(s.Dir)} {
		if st, err := os.Stat(d); err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, st.Mode().Perm(), err)
		}
	}
}
