package cmdtest

import "github.com/netbja/agent-bus-monitor/service"

// Holders is a HolderManager over a list of units and their activity.
type Holders struct {
	*Manager
	Units  []service.HolderUnit
	Active map[string]bool
	Err    error
}

func (f *Holders) InstallHolder(service.HolderSpec) (bool, error) { return false, nil }

func (f *Holders) StartHolder(project, key string) error {
	f.Calls = append(f.Calls, "start-holder "+project+" "+key)
	return nil
}

func (f *Holders) StopHolder(string, string) error { return nil }

func (f *Holders) RestartHolder(string, string) error { return nil }

func (f *Holders) HolderActive(project, key string) (bool, error) {
	return f.Active[key], f.Err
}

func (f *Holders) RemoveHolder(project, key string) error {
	f.Calls = append(f.Calls, "remove-holder "+project+" "+key)
	for i, u := range f.Units {
		if u.Key == key {
			f.Units = append(f.Units[:i], f.Units[i+1:]...)
			break
		}
	}
	return nil
}

func (f *Holders) HolderUnits(project string) ([]service.HolderUnit, error) {
	var out []service.HolderUnit
	for _, u := range f.Units {
		if project == "" || u.Project == project {
			out = append(out, u)
		}
	}
	return out, nil
}
