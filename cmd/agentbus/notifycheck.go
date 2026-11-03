package main

import (
	"context"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/notify"
)

// notificationsCheck judges project with health.EvaluateNotifications from
// the host's config.toml, notify.toml and notify-last.json; nothing it
// reads names a URL.
func notificationsCheck(project string, getenv func(string) string, now func() time.Time) func(context.Context) (health.Outcome, error) {
	return func(context.Context) (health.Outcome, error) {
		r := config.Machine(getenv)
		in := health.NotificationsInput{On: r.Bool("notify.on"), Now: now()}
		if !in.On {
			return health.EvaluateNotifications(in), nil
		}
		f := notify.Load(notify.Path(getenv))
		switch {
		case f.Refused == "cannot be read" || strings.HasPrefix(f.Refused, "its folder cannot"):
			in.FileErr = f.Refused
		case f.Refused != "":
			in.Refused = f.Refused
		}
		refused := map[string]bool{}
		for _, w := range f.Warnings {
			if parts := strings.SplitN(w.Key, ".", 3); len(parts) >= 2 {
				refused[parts[1]] = true
			}
		}
		state := notifyStateDir(getenv)
		var last notify.Last
		if state == "" {
			in.RecordErr = "no state folder"
		} else if l, err := notify.ReadLast(notify.LastPath(state)); err != nil {
			in.RecordErr = err.Error()
		} else {
			last = l
		}
		for _, name := range config.SplitList(r.String("notify.targets")) {
			t := health.NotifyTarget{Name: name}
			if _, ok := f.Target(name); !ok {
				t.Refused, t.Missing = refused[name], !refused[name]
			} else if o, ok := last.Latest(name); ok {
				t.Sent, t.OK, t.Err, t.At = true, o.OK, o.Err, o.At
			}
			in.Targets = append(in.Targets, t)
		}
		in.Sources = last.Sources[project]
		return health.EvaluateNotifications(in), nil
	}
}
