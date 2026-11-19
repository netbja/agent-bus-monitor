// Package cmdtest holds the test fixtures cmd/agentbus and the dispatch
// service's tests share (dispatchsvc spec §5, rule 4; architect's decision).
package cmdtest

import (
	"errors"
	"strings"

	"github.com/netbja/agent-bus-monitor/service"
)

// Manager is a service manager in memory: installed specs, active set,
// and every call.
type Manager struct {
	Specs     map[string]service.Spec
	Active    map[string]bool
	Calls     []string
	FailStart bool
	FailState bool                    // every State read fails
	StopErr   func(p, k string) error // nil = every stop succeeds
	FailList  bool                    // List fails after the first removal
	Removed   int
	OnCall    func(call string) // nil = nothing; told each start and stop as it happens
}

func NewManager() *Manager {
	return &Manager{Specs: map[string]service.Spec{}, Active: map[string]bool{}}
}

func (f *Manager) Name() string { return "fake" }

func (f *Manager) Install(s service.Spec) (bool, error) {
	f.Calls = append(f.Calls, "install "+s.Project+" "+s.Kind)
	key := s.Project + "/" + s.Kind
	old, had := f.Specs[key]
	f.Specs[key] = s
	return !had || strings.Join(old.Args, " ") != strings.Join(s.Args, " ") || old.Exe != s.Exe, nil
}

func (f *Manager) Start(p, k string) error {
	f.Calls = append(f.Calls, "start "+p+" "+k)
	if f.OnCall != nil {
		f.OnCall("start " + p + " " + k)
	}
	if f.FailStart {
		return errors.New("start refused")
	}
	f.Active[p+"/"+k] = true
	return nil
}

func (f *Manager) Stop(p, k string) error {
	f.Calls = append(f.Calls, "stop "+p+" "+k)
	if f.OnCall != nil {
		f.OnCall("stop " + p + " " + k)
	}
	if f.StopErr != nil {
		if err := f.StopErr(p, k); err != nil {
			return err
		}
	}
	f.Active[p+"/"+k] = false
	return nil
}

func (f *Manager) Remove(p, k string) error {
	f.Calls = append(f.Calls, "remove "+p+" "+k)
	delete(f.Specs, p+"/"+k)
	f.Removed++
	delete(f.Active, p+"/"+k)
	return nil
}

func (f *Manager) State(p, k string) (service.State, error) {
	if f.FailState {
		return service.State{}, errors.New("cannot read the unit")
	}
	_, inst := f.Specs[p+"/"+k]
	st := service.State{Installed: inst, Status: "inactive (dead)"}
	if f.Active[p+"/"+k] {
		st.Active, st.Status, st.PID = true, "active (running)", 99
	}
	return st, nil
}

func (f *Manager) List() ([]service.Unit, error) {
	if f.FailList && f.Removed > 0 {
		return nil, errors.New("cannot list units")
	}
	var out []service.Unit
	for _, k := range service.Kinds {
		for key := range f.Specs {
			if p, kind, _ := strings.Cut(key, "/"); kind == k {
				out = append(out, service.Unit{Project: p, Kind: kind})
			}
		}
	}
	return out, nil
}

func (f *Manager) Saw(c string) bool {
	for _, x := range f.Calls {
		if x == c {
			return true
		}
	}
	return false
}
