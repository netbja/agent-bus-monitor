package dispatchsvc

import (
	"context"
	"fmt"
	"strconv"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

// holderDownMark is the {p}:quota field that holds the episode the person
// was told of a holder that does not answer (holder hardening H3).
const holderDownMark = "holder-down"

// holderNotice is the health loop's hook after each stored holder record
// (HealthConfig.HolderNotify). A holder that does not answer took every
// agent with it, sentinel and master included, so the person is told: once
// per episode, the episode being the fail's since (Redis time), its mark in
// Redis so a restarted dispatcher does not ring twice. An unknown pass keeps
// the episode and its mark (as the stall guard's); ok or warn ends it.
func holderNotice(b *bus.Bus) func(ctx context.Context, token string, _ health.Record) error {
	return func(ctx context.Context, token string, _ health.Record) error {
		recs, err := b.Health(ctx)
		if err != nil {
			return err
		}
		marks, err := b.QuotaMarks(ctx)
		if err != nil {
			return err
		}
		rec := recs["holder"]
		_, told := marks[holderDownMark]
		switch {
		case rec.Status == health.OK || rec.Status == health.Warn:
			if told {
				return b.FencedQuotaDel(ctx, token, holderDownMark)
			}
			return nil
		case rec.Status != health.Fail || rec.Since == 0, told:
			return nil
		}
		msg := holderNoticePrefix + fmt.Sprintf("%s: %s; every agent it held is gone. Run: agentbus --project %s service start holder",
			b.Project(), rec.Detail, b.Project())
		_, err = b.FencedNotifyOnceForPerson(ctx, token, holderDownMark, strconv.FormatInt(rec.Since, 10), "dispatch", msg)
		return err
	}
}
