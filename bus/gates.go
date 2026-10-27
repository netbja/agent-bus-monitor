package bus

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/redis/go-redis/v9"
)

// ConductorAgent is the name the missions conductor writes as. Its review
// gates (opened on the reviewer) are closed by the conductor itself once it
// reads the verdict, so a reviewer's verdict never closes them.
const ConductorAgent = "conductor"

// GateMeta is a gate's value, "<challenger>|<text>", where text may start
// with "[subject <S>] " (foryou-gates: the subject the review is for, stored
// at challenge time so the reviewer's verdict on S closes the gate). A meta
// written before has no subject; its text is kept whole.
type GateMeta struct {
	Challenger, Subject, Text string
}

const gateSubjectTag = "[subject "

// ParseGateMeta splits a gate's meta. A text that does not start with a
// well-formed subject tag carries no subject.
func ParseGateMeta(meta string) GateMeta {
	challenger, text, _ := strings.Cut(meta, "|")
	g := GateMeta{Challenger: challenger, Text: text}
	if rest, ok := strings.CutPrefix(text, gateSubjectTag); ok {
		if subject, after, ok := strings.Cut(rest, "] "); ok && ValidGateSubject(subject) == nil {
			g.Subject, g.Text = subject, after
		}
	}
	return g
}

// FormatGateMeta is the meta OpenChallenge stores; subject "" writes the old
// form, so every existing reader keeps working.
func FormatGateMeta(challenger, subject, text string) string {
	if subject == "" {
		return challenger + "|" + text
	}
	return challenger + "|" + gateSubjectTag + subject + "] " + text
}

// ValidGateSubject: a verdict subject a gate can carry in its tag — no
// space, no ']', no control character, at most 128 runes.
func ValidGateSubject(s string) error {
	if s == "" || len([]rune(s)) > 128 {
		return fmt.Errorf("gate subject must be 1-128 characters")
	}
	for _, r := range s {
		if r == ']' || unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("gate subject %q: no space, ']' or control character", s)
		}
	}
	return nil
}

// AllGates returns every open gate of the project, gated agent → ref → meta.
func (b *Bus) AllGates(ctx context.Context) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	prefix := b.project + ":gate:"
	iter := b.r.Scan(ctx, 0, prefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		gated := strings.TrimPrefix(key, prefix)
		if !ValidName(gated) {
			continue
		}
		h, err := b.r.HGetAll(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		if len(h) > 0 {
			out[gated] = h
		}
	}
	return out, iter.Err()
}

// closeGateIf: 0 when the gate no longer holds ARGV[2] (closed or changed);
// else HDEL, and when ARGV[3] is set XADD that trace on the cmd stream.
var closeGateIf = redis.NewScript(`
if redis.call('HGET',KEYS[1],ARGV[1]) ~= ARGV[2] then return 0 end
redis.call('HDEL',KEYS[1],ARGV[1])
if ARGV[3] == '1' then
  local f = {'from',ARGV[5],'target',ARGV[6],'type','reply','ref',ARGV[1],'command',ARGV[7],'text_complete','yes','wake','no'}
  if ARGV[8] == '1' then table.insert(f,'human') table.insert(f,'yes') end
  redis.call('XADD',KEYS[2],'MAXLEN','~',ARGV[4],'*',unpack(f))
end
return 1`)

// CloseReviewGates closes, on reviewer's own gate, the gates a verdict by
// reviewer on author's work answers. With a ref: the gate named by ref, plus
// the gates of the same subject opened before it (gate-subject-close: a
// verdict on an older commit never closes the gate of a newer delta the
// reviewer has not read). Without a ref: every gate whose stored subject is
// subject. A self-verdict closes nothing, and a conductor's mission gate is
// left to the conductor. It returns the closed refs, sorted. Each close is
// compare-and-delete, so a gate re-opened under the same ref in between is
// kept.
func (b *Bus) CloseReviewGates(ctx context.Context, reviewer, author, subject, ref string) ([]string, error) {
	if reviewer == author {
		return nil, nil
	}
	open, err := b.OpenChallenges(ctx, reviewer)
	if err != nil {
		return nil, err
	}
	var opened map[string]string
	var anchor string
	if ref != "" && subject != "" {
		if opened, anchor, err = b.challengesOpened(ctx, reviewer, ref); err != nil {
			return nil, err
		}
	}
	var closed []string
	for r, meta := range open {
		g := ParseGateMeta(meta)
		if g.Challenger == ConductorAgent {
			continue
		}
		sameSubject := g.Subject != "" && g.Subject == subject
		switch {
		case ref != "" && r == ref:
		case ref == "" && sameSubject:
		case ref != "" && sameSubject && anchor != "":
			// No retained challenge: it left the stream before the anchor's,
			// so it was opened before it (the stream is trimmed oldest first).
			if at, ok := opened[r]; ok && !idLess(at, anchor) {
				continue
			}
		default:
			continue
		}
		n, err := closeGateIf.Run(ctx, b.r, []string{GateKey(b.project, reviewer), StreamKey(b.project, "cmd")},
			r, meta, "0").Int()
		if err != nil {
			return closed, err
		}
		if n == 1 {
			closed = append(closed, r)
		}
	}
	sort.Strings(closed)
	return closed, nil
}

// challengesOpened reads the cmd stream once: opened maps each ref of a
// challenge to reviewer to its first entry id, and anchor is the id of the
// first challenge carrying ref (to any target, so an author's gate anchors
// too), "" when none is retained.
func (b *Bus) challengesOpened(ctx context.Context, reviewer, ref string) (map[string]string, string, error) {
	key := StreamKey(b.project, "cmd")
	msgs, err := b.r.XRange(ctx, key, "-", "+").Result()
	if err != nil {
		return nil, "", err
	}
	opened := map[string]string{}
	anchor := ""
	for _, m := range msgs {
		e := ParseEntry(key, m.ID, toStringMap(m.Values))
		if e.Type != CmdChallenge || e.Ref == "" {
			continue
		}
		if e.Ref == ref && anchor == "" {
			anchor = e.ID
		}
		if _, seen := opened[e.Ref]; !seen && e.Target == reviewer {
			opened[e.Ref] = e.ID
		}
	}
	return opened, anchor, nil
}

// CloseGate closes one gate without a verdict, only while it still holds meta
// (what the caller read and judged), and leaves in the same script a wake=no
// reply on the gate's thread to the gated agent, from by (human=yes for a
// person): an audit trace that is not a verdict and never counts in a
// roll-up. Who may close is the caller's rule (the challenger or a person).
func (b *Bus) CloseGate(ctx context.Context, agent, ref, meta, by, note string, person bool) (bool, error) {
	if !ValidName(agent) || !ValidName(by) {
		return false, fmt.Errorf("invalid agent %q or closer %q", agent, by)
	}
	if ref == "" {
		return false, fmt.Errorf("gate ref required")
	}
	text := fmt.Sprintf("gate %s closed without a verdict by %s", ref, by)
	if note = strings.TrimSpace(note); note != "" {
		text += ": " + note
	}
	if err := ValidateText(text, CmdMaxRunes); err != nil {
		return false, err
	}
	human := "0"
	if person {
		human = "1"
	}
	n, err := closeGateIf.Run(ctx, b.r, []string{GateKey(b.project, agent), StreamKey(b.project, "cmd")},
		ref, meta, "1", streamMaxLen, by, agent, text, human).Int()
	return n == 1, err
}
