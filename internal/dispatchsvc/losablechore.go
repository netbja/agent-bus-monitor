package dispatchsvc

import (
	"context"
	"errors"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/forge"
	"github.com/netbja/agent-bus-monitor/mergequeue"
)

// The pass's losable work (cleaning-up-agents §3.1 item 5): after the queue,
// each role's unpushed held branches and the stashes that name it, written
// fenced to {p}:losable. A read that fails writes nothing: the last finding
// ages out (bus.LosableFresh) and item 5 reads unknown, never "owes nothing".

// losableBudget bounds the losable read, its write included.
const losableBudget = 10 * time.Second

// losablePass judges and writes; only the lease's error is returned.
func (c *mergeQueueChore) losablePass(ctx context.Context, token string) error {
	if c.stashes == nil {
		return nil // a chore built without the reader
	}
	lctx, cancel := context.WithTimeout(ctx, losableBudget)
	defer cancel()
	found, ok := c.judgeLosable(lctx)
	if !ok {
		return nil
	}
	if err := c.b.FencedPutLosable(lctx, token, found); errors.Is(err, bus.ErrNotHolder) {
		return err
	}
	return nil
}

// judgeLosable reads the board, the agents and git; false when any read
// fails.
func (c *mergeQueueChore) judgeLosable(ctx context.Context) (map[string]bus.Losable, bool) {
	base, _, err := forge.ProjectDeliveryBase(c.getenv, c.b.Project())
	if err != nil {
		return nil, false
	}
	env := mergeQueueEnv(c.environ())
	call := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, mergeQueueCall) }
	cctx, cancel := call()
	err = c.repo(cctx, c.dir, env)
	cancel()
	if err != nil {
		return nil, false
	}
	cctx, cancel = call()
	url, has, err := c.origin(cctx, c.dir, env)
	cancel()
	if err != nil {
		return nil, false
	}
	board, err := c.b.Board(ctx)
	if err != nil {
		return nil, false
	}
	agents, err := c.b.Agents(ctx)
	if err != nil {
		return nil, false
	}
	held := mergequeue.HeldBranches(board)
	roleSet := map[string]bool{}
	for role := range agents {
		roleSet[role] = true
	}
	for _, h := range held {
		roleSet[h.Role] = true
	}
	in := mergequeue.LosableIn{Held: held, Local: map[string]string{}}
	for role := range roleSet {
		in.Roles = append(in.Roles, role)
	}
	cctx, cancel = call()
	locals, err := c.localBranches(cctx, c.dir, env)
	cancel()
	if err != nil {
		return nil, false
	}
	for _, t := range locals {
		in.Local[t.Branch] = t.Head
	}
	cctx, cancel = call()
	in.Stashes, err = c.stashes(cctx, c.dir, env)
	cancel()
	if err != nil {
		return nil, false
	}
	if forge.Resolve(config.Machine(c.getenv).String("delivery.forge"), url, has).Kind != forge.Local {
		cctx, cancel = call()
		remote, baseTip, err := c.remoteTips(cctx, c.dir, env, base)
		cancel()
		if err != nil {
			return nil, false
		}
		tips := map[string]string{}
		for _, t := range remote {
			tips[t.Branch] = t.Head
		}
		in.Pushed = func(branch, tip string) (bool, error) {
			if tips[branch] == tip {
				return true, nil
			}
			for _, on := range []string{tips[branch], baseTip} { // a remote tip past it, or the base that merged it
				if on == "" {
					continue
				}
				if ok, err := c.contains(ctx, env, on, tip); err != nil || ok {
					return ok, err
				}
			}
			return false, nil
		}
	}
	found, err := mergequeue.JudgeLosable(in)
	return found, err == nil
}

// contains tells whether commit on (absent here: false) has tip in its
// history.
func (c *mergeQueueChore) contains(ctx context.Context, env []string, on, tip string) (bool, error) {
	cctx, cancel := context.WithTimeout(ctx, mergeQueueCall)
	defer cancel()
	has, err := c.hasCommit(cctx, c.dir, env, on)
	if err != nil || !has {
		return false, err
	}
	return c.isAncestor(cctx, c.dir, env, tip, on)
}
