package bus

import (
	"encoding/json"
	"errors"
	"fmt"

	"context"

	"github.com/redis/go-redis/v9"
)

// TypedApproval is the one check of an approval (onboarding §8.4, spec
// docs/superpowers/specs/2026-11-27-project-onboarding-design.md), the body
// RetroApproved had: question qid of ask askID was answered choice by the
// person. While the ask's record is kept, its answer to qid must exist, not
// be withdrawn, be choice, and not be the asker's own (the asker never
// approves itself); the last reply entry with typed=yes, that ref and that
// qid, from the cmd stream then the archive (SetApprovalArchive), must say
// choice. It returns that entry's id, or why there is none; a failed read is
// an error, never a yes.
func (b *Bus) TypedApproval(ctx context.Context, askID, qid, choice string) (string, error) {
	kept, err := b.r.HExists(ctx, AsksKey(b.project), askID).Result()
	if err != nil {
		return "", err
	}
	if kept {
		def, err := b.askDef(ctx, askID)
		if err != nil {
			return "", err
		}
		raw, err := b.r.HGet(ctx, AsksKey(b.project), askID+"|a|"+qid).Result()
		if errors.Is(err, redis.Nil) {
			return "", fmt.Errorf("%s %s is not answered", askID, qid)
		}
		if err != nil {
			return "", err
		}
		var a AskAnswer
		if json.Unmarshal([]byte(raw), &a) != nil || a.Withdrawn || a.Choice != choice {
			return "", fmt.Errorf("%s %s was answered %q, not %s", askID, qid, a.Value(), choice)
		}
		if a.By == def.From {
			return "", fmt.Errorf("%s %s was answered by its asker %s", askID, qid, def.From)
		}
	}
	e, ok, err := b.typedReply(ctx, askID, qid)
	if err != nil {
		return "", err
	}
	if !ok || e.Said != choice {
		return "", fmt.Errorf("no typed %s from the person to %s %s", choice, askID, qid)
	}
	return e.ID, nil
}
