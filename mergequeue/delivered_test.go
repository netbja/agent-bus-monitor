package mergequeue

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The git test of master-idle §3.1, on a disposable repository: whatever the
// forge did (a merge commit, a fast-forward, a squash, a rebase-merge), the
// approved head is delivered when the base holds its change.

type dRepo struct {
	t   *testing.T
	env []string
	dir string
}

func newDRepo(t *testing.T) *dRepo {
	t.Helper()
	r := &dRepo{t: t, env: gitEnv(t), dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	r.write("f", strings.Join(lines, "\n")+"\n")
	r.commit("base")
	return r
}

func (r *dRepo) git(args ...string) string { r.t.Helper(); return git(r.t, r.env, r.dir, args...) }

func (r *dRepo) write(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// setLine sets line n (1-based) of file f.
func (r *dRepo) setLine(f string, n int, s string) {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, f))
	if err != nil {
		r.t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	lines[n-1] = s
	r.write(f, strings.Join(lines, "\n")+"\n")
}

func (r *dRepo) commit(msg string) string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func (r *dRepo) tip(branch string) string { return r.git("rev-parse", branch) }

// branch makes branch from main with one commit per (line, text) change of f.
func (r *dRepo) branch(name string, changes ...[2]string) string {
	r.t.Helper()
	r.git("switch", "-q", "-c", name, "main")
	for _, c := range changes {
		var n int
		fmt.Sscan(c[0], &n)
		r.setLine("f", n, c[1])
		r.commit(name + " " + c[0])
	}
	r.git("switch", "-q", "main")
	return r.tip(name)
}

func (r *dRepo) judge(d *Delivery, heads ...string) map[string]bool {
	r.t.Helper()
	got, err := d.Delivered(context.Background(), r.dir, r.env, r.tip("main"), heads)
	if err != nil {
		r.t.Fatal(err)
	}
	return got
}

func TestDeliveredMergeAndFastForward(t *testing.T) {
	r := newDRepo(t)
	ff := r.branch("ff", [2]string{"3", "ff"})
	r.git("merge", "-q", "--ff-only", "ff")
	mc := r.branch("mc", [2]string{"9", "mc"})
	r.setLine("f", 15, "base moves")
	r.commit("unrelated")
	r.git("merge", "-q", "--no-ff", "-m", "merge mc", "mc")
	if got := r.judge(NewDelivery(), ff, mc); !got[ff] || !got[mc] {
		t.Fatalf("fast-forward and merge commit: %v", got)
	}
}

func TestDeliveredSquashAndRebaseMerge(t *testing.T) {
	r := newDRepo(t)
	sq := r.branch("sq", [2]string{"3", "sq a"}, [2]string{"12", "sq b"})
	rb := r.branch("rb", [2]string{"6", "rb a"}, [2]string{"18", "rb b"})
	open := r.branch("open", [2]string{"9", "never delivered"})
	r.setLine("f", 1, "base moves")
	r.commit("unrelated")
	r.git("merge", "-q", "--squash", "sq")
	r.commit("squash of sq")
	r.git("cherry-pick", "rb~1", "rb") // a rebase-merge replays each commit
	got := r.judge(NewDelivery(), sq, rb, open)
	if !got[sq] || !got[rb] || got[open] {
		t.Fatalf("squash %v, rebase-merge %v, open %v", got[sq], got[rb], got[open])
	}
}

// foureyes on r5: a base change within three lines of the branch's change
// after the fork gives another patch-id with context; -U0 still matches.
func TestDeliveredWithANearbyBaseChange(t *testing.T) {
	r := newDRepo(t)
	sq := r.branch("sq", [2]string{"10", "sq"})
	rb := r.branch("rb", [2]string{"14", "rb"})
	r.setLine("f", 8, "base near sq")
	r.setLine("f", 12, "base near rb")
	r.commit("base near both")
	r.git("merge", "-q", "--squash", "sq")
	r.commit("squash of sq")
	r.git("cherry-pick", "rb")
	if got := r.judge(NewDelivery(), sq, rb); !got[sq] || !got[rb] {
		t.Fatalf("near context: %v", got)
	}
}

// A squash that also resolved a conflict holds other lines: not seen
// (merge-queue drop covers it); an unrelated commit with the same message is
// no delivery.
func TestDeliveredNotByAResolvedSquashOrAMessage(t *testing.T) {
	r := newDRepo(t)
	sq := r.branch("sq", [2]string{"5", "sq"})
	msg := r.branch("msg", [2]string{"7", "msg"})
	r.setLine("f", 5, "base")
	r.commit("base takes line 5")
	r.setLine("f", 5, "resolved")
	r.commit("squash of sq, resolved")
	r.setLine("f", 2, "other")
	r.commit("msg 7") // msg's message, another change
	if got := r.judge(NewDelivery(), sq, msg); got[sq] || got[msg] {
		t.Fatalf("resolved squash %v, same message %v", got[sq], got[msg])
	}
}

// reviewer-codex on r6: a head holding a merge commit whose resolution the
// base lacks is not delivered by its ordinary commits alone.
func TestDeliveredNotWithoutAMergeResolution(t *testing.T) {
	r := newDRepo(t)
	side := r.branch("side", [2]string{"16", "side"})
	r.git("switch", "-q", "-c", "evil", "main")
	r.setLine("f", 4, "evil a")
	a := r.commit("evil a")
	r.git("merge", "-q", "--no-ff", "--no-commit", "side")
	r.write("w", "only in the merge\n")
	r.git("add", "w")
	r.git("commit", "-q", "-m", "merge side, with an extra file")
	head := r.tip("evil")
	r.git("switch", "-q", "main")
	r.git("cherry-pick", a, side)
	if got := r.judge(NewDelivery(), head); got[head] {
		t.Fatalf("a head with an unseen merge resolution reads delivered")
	}
}

// The residual risk the person accepted (§3.1), pinned: a one-commit head
// whose -U0 change equals an unrelated base commit's reads delivered; with
// a second, unmatched commit it does not.
func TestDeliveredResidualRisk(t *testing.T) {
	r := newDRepo(t)
	r.write("VERSION", "1.2.3\n")
	r.commit("version file")
	one := r.branch("one")
	r.git("switch", "-q", "one")
	r.write("VERSION", "1.2.4\n")
	one = r.commit("bump")
	r.git("switch", "-q", "-c", "two", "main")
	r.write("VERSION", "1.2.4\n")
	r.commit("bump too")
	r.setLine("f", 11, "two's own")
	two := r.commit("two's own change")
	r.git("switch", "-q", "main")
	r.write("VERSION", "1.2.4\n")
	r.commit("an unrelated bump")
	got := r.judge(NewDelivery(), one, two)
	if !got[one] || got[two] {
		t.Fatalf("one-commit twin %v (want true), two commits with one twin %v (want false)", got[one], got[two])
	}
}

// The cache (§3.1): a commit's patch-id is computed once; a new base commit
// is the only one computed on the next pass; a SHA that left every range is
// dropped; a new Delivery (a restart) computes again.
func TestDeliveredCache(t *testing.T) {
	r := newDRepo(t)
	h := r.branch("h", [2]string{"3", "h"})
	r.setLine("f", 19, "base 1")
	r.commit("base 1")
	r.setLine("f", 17, "base 2")
	r.commit("base 2")
	d := NewDelivery()
	r.judge(d, h)
	first := d.Computed()
	if first == 0 {
		t.Fatal("the first pass computed nothing")
	}
	r.judge(d, h)
	if n := d.Computed(); n != 0 {
		t.Fatalf("the second pass over the same base computed %d", n)
	}
	r.setLine("f", 15, "base 3")
	r.commit("base 3")
	r.judge(d, h)
	if n := d.Computed(); n != 1 {
		t.Fatalf("one new base commit: computed %d", n)
	}
	// A newer fork alone: base 1..3 leave the range (h's fork was before them).
	later := r.branch("later", [2]string{"7", "later"})
	size := d.size()
	r.judge(d, later)
	if d.size() >= size {
		t.Fatalf("the map kept SHAs out of every range: %d then %d", size, d.size())
	}
	d2 := NewDelivery()
	r.judge(d2, h)
	if d2.Computed() < first {
		t.Fatalf("a restart computed %d, the first pass %d", d2.Computed(), first)
	}
}

// The base's set is read once per pass for several heads.
func TestDeliveredReadsTheBaseOnce(t *testing.T) {
	r := newDRepo(t)
	a := r.branch("a", [2]string{"3", "a"})
	b := r.branch("b", [2]string{"6", "b"})
	c := r.branch("c", [2]string{"9", "c"})
	for i := range 5 {
		r.setLine("f", 12+i, fmt.Sprint("base ", i))
		r.commit(fmt.Sprint("base ", i))
	}
	d := NewDelivery()
	r.judge(d, a, b, c)
	// 5 base commits, and per head its one commit and its branch diff.
	if n := d.Computed(); n != 5+3+3 {
		t.Fatalf("computed %d patch-ids, want 11 (the base once)", n)
	}
}

// The dispatcher never runs the repository's code: an external diff or a
// textconv driver in its configuration never runs in any step.
func TestDeliveredRunsNoDiffDriver(t *testing.T) {
	r := newDRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "driver")
	os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o755)
	r.git("config", "diff.external", script)
	r.git("config", "diff.evil.textconv", script)
	r.git("config", "log.showSignature", "true")
	r.write(".gitattributes", "* diff=evil\n")
	r.commit("attributes")
	sq := r.branch("sq", [2]string{"3", "sq"})
	rb := r.branch("rb", [2]string{"6", "rb"})
	r.git("merge", "-q", "--squash", "sq")
	r.commit("squash")
	r.git("cherry-pick", "rb")
	if got := r.judge(NewDelivery(), sq, rb); !got[sq] || !got[rb] {
		t.Fatalf("with drivers configured: %v", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a diff driver ran")
	}
}

// A head this repository does not hold is not delivered, never an error: it
// is listed, and master drops it if nobody will push it.
func TestDeliveredAbsentHead(t *testing.T) {
	r := newDRepo(t)
	absent := strings.Repeat("ab", 20)
	if got := r.judge(NewDelivery(), absent); got[absent] {
		t.Fatal("an absent head reads delivered")
	}
}
