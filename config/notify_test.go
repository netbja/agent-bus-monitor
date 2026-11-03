package config

import (
	"testing"
	"time"
)

func TestNotifyKeys(t *testing.T) {
	want := map[string]any{
		"notify.on":           false,
		"notify.targets":      "",
		"notify.events":       "decision,stall,fail,blocked,prompt",
		"notify.detail":       "summary",
		"notify.delay":        2 * time.Minute,
		"notify.max_per_hour": 6,
		"notify.quiet":        "",
		"notify.quiet_exempt": "fail,stall",
	}
	for name, def := range want {
		k, ok := Lookup(name)
		if !ok {
			t.Fatalf("%s: not in the registry", name)
		}
		_, isList := def.(string)
		isList = isList && (name == "notify.targets" || name == "notify.events" || name == "notify.quiet_exempt")
		if k.Default != def || k.Env != "" || !k.MachineOnly || k.PersonOnly || k.Group() != GroupNotifications || k.List != isList {
			t.Errorf("%s: %+v group %q", name, k, k.Group())
		}
	}
}

func TestNotifyFileRules(t *testing.T) {
	cases := []struct {
		key  string
		in   any
		want any // nil = refused
	}{
		{"notify.targets", []any{"phone", "here"}, "phone,here"},
		{"notify.targets", []any{}, ""},
		{"notify.targets", []any{"Phone"}, nil},
		{"notify.targets", []any{"phone", "phone"}, nil},
		{"notify.targets", "phone", nil},
		{"notify.events", []any{"decision", "for-you"}, "decision,for-you"},
		{"notify.events", []any{"report"}, nil},
		{"notify.events", []any{}, ""},
		{"notify.quiet_exempt", []any{"fail"}, "fail"},
		{"notify.quiet_exempt", []any{}, ""},
		{"notify.quiet_exempt", []any{"nope"}, nil},
		{"notify.detail", "full", "full"},
		{"notify.detail", "all", nil},
		{"notify.delay", "0", time.Duration(0)},
		{"notify.delay", "90s", 90 * time.Second},
		{"notify.max_per_hour", int64(0), nil},
		{"notify.max_per_hour", int64(12), 12},
		{"notify.quiet", "22:00-07:30", "22:00-07:30"},
		{"notify.quiet", "", ""},
		{"notify.quiet", "9:00-10:00", nil},
		{"notify.quiet", "22:00-22:00", nil},
		{"notify.quiet", "24:00-07:00", nil},
		{"notify.quiet", "22:60-07:00", nil},
		{"notify.quiet", " 9:00-10:00", nil},
		{"notify.quiet", "+9:00-10:00", nil},
		{"notify.on", true, true},
	}
	for _, c := range cases {
		k, _ := Lookup(c.key)
		got, why := k.file(c.in)
		if c.want == nil {
			if why == "" {
				t.Errorf("%s %#v: taken as %#v", c.key, c.in, got)
			}
			continue
		}
		if why != "" || got != c.want {
			t.Errorf("%s %#v: got %#v %q, want %#v", c.key, c.in, got, why, c.want)
		}
	}
}

func TestQuietHours(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 3, h, m, 0, 0, time.Local) }
	q, ok := ParseQuiet("22:00-07:30")
	if !ok {
		t.Fatal("not parsed")
	}
	for _, c := range []struct {
		t    time.Time
		want bool
	}{{at(21, 59), false}, {at(22, 0), true}, {at(3, 0), true}, {at(7, 29), true}, {at(7, 30), false}, {at(12, 0), false}} {
		if q.Holds(c.t) != c.want {
			t.Errorf("%s: %v", c.t.Format("15:04"), !c.want)
		}
	}
	day, _ := ParseQuiet("12:00-14:00")
	if !day.Holds(at(13, 0)) || day.Holds(at(14, 0)) || day.Holds(at(11, 59)) {
		t.Error("a window within one day")
	}
	if _, ok := ParseQuiet(""); ok {
		t.Error("empty is no window")
	}
}
