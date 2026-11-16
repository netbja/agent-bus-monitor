package main

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/netbja/agent-bus-monitor/lifecycle"
)

// The run record's writers (reboot-resume §2.1): the dispatch service at
// start and on SIGTERM, up before it starts the services, down and suspend
// before they stop them. The next boot reads it to tell an intentional
// reboot from an unplanned one. A write that fails is said and never stops
// the command: at worst the next boot judges nothing (or one restart more).
// Where the boot id is unknown (the BSDs) nothing is written: nothing can be
// judged there (§2.3).

// runBootID is this host's boot id, dispatch.BootID (a variable: tests).
var runBootID = hostBootID

// recordUp is agentbus up's write: the counter recreated when missing, then
// running, this boot.
func recordUp(dir string, warn io.Writer) {
	boot := runBootID()
	if boot == "" {
		return
	}
	if err := lifecycle.Up(dir, boot, time.Now()); err != nil {
		fmt.Fprintf(warn, "run record: %v (a reboot after this start may not be recognised)\n", err)
	}
}

// recordStopping is down's and suspend's write: stopped, with its cause.
func recordStopping(dir, cause string, warn io.Writer) {
	boot := runBootID()
	if boot == "" {
		return
	}
	if err := lifecycle.Stopping(dir, boot, cause, time.Now()); err != nil {
		fmt.Fprintf(warn, "run record: %v (a reboot may later read as unexpected)\n", err)
	}
}

// recordRunning puts running back for this boot when a stop was cancelled
// (a suspend whose services started again).
func recordRunning(dir string, warn io.Writer) {
	boot := runBootID()
	if boot == "" {
		return
	}
	if err := lifecycle.Started(dir, boot, time.Now()); err != nil {
		fmt.Fprintf(warn, "run record: %v\n", err)
	}
}

// dispatchRunRecord is the dispatch service's: running once it first holds
// the lease, stopped (signal) when a signal ends it.
type dispatchRunRecord struct {
	dir  string
	boot string
	log  io.Writer
	once sync.Once
}

func newDispatchRunRecord(dir string, log io.Writer) *dispatchRunRecord {
	return &dispatchRunRecord{dir: dir, boot: runBootID(), log: log}
}

// onLease wraps the service's lease callback: the first lease writes running.
func (r *dispatchRunRecord) onLease(next func(bool)) func(bool) {
	return func(held bool) {
		if held && r.boot != "" {
			r.once.Do(func() {
				if err := lifecycle.Started(r.dir, r.boot, time.Now()); err != nil {
					fmt.Fprintf(r.log, "dispatch: run record: %v\n", err)
				}
			})
		}
		if next != nil {
			next(held)
		}
	}
}

// exited is called when the service returns: signalled, it writes stopped
// (cause signal) over running of this boot only.
func (r *dispatchRunRecord) exited(signalled bool) {
	if !signalled || r.boot == "" {
		return
	}
	if err := lifecycle.Signalled(r.dir, r.boot, time.Now()); err != nil {
		fmt.Fprintf(r.log, "dispatch: run record: %v\n", err)
	}
}
