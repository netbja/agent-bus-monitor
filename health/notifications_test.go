package health

import (
	"strings"
	"testing"
	"time"
)

func TestEvaluateNotifications(t *testing.T) {
	now := time.Date(2026, 11, 3, 14, 0, 0, 0, time.UTC)
	ok := []NotifyTarget{{Name: "phone", Sent: true, OK: true}, {Name: "here"}}
	for name, c := range map[string]struct {
		in     NotificationsInput
		status Status
		detail []string
	}{
		"off":            {NotificationsInput{}, Unknown, []string{"off"}},
		"unreadable":     {NotificationsInput{On: true, FileErr: "cannot be read"}, Unknown, []string{"notify.toml", "cannot be read"}},
		"record":         {NotificationsInput{On: true, RecordErr: "does not parse", Targets: ok}, Unknown, []string{"notify-last.json"}},
		"open to others": {NotificationsInput{On: true, Refused: "readable or writable by others: run chmod 600 on notify.toml"}, Warn, []string{"chmod 600"}},
		"none listed":    {NotificationsInput{On: true}, Warn, []string{"notify.targets"}},
		"missing":        {NotificationsInput{On: true, Targets: []NotifyTarget{{Name: "gone", Missing: true}}}, Warn, []string{"gone", "not in notify.toml"}},
		"refused":        {NotificationsInput{On: true, Targets: []NotifyTarget{{Name: "bad", Refused: true}}}, Warn, []string{"bad", "refused"}},
		"failed send": {NotificationsInput{On: true, Now: now, Targets: []NotifyTarget{{Name: "phone", Sent: true, Err: "phone: 401 unauthorized", At: now.Add(-time.Minute)}}},
			Warn, []string{"phone: 401 unauthorized"}},
		"source failing long": {NotificationsInput{On: true, Now: now, Targets: ok, Sources: map[string]time.Time{"asks": now.Add(-11 * time.Minute)}},
			Warn, []string{"asks", "nothing is sent"}},
		"source failing briefly": {NotificationsInput{On: true, Now: now, Targets: ok, Sources: map[string]time.Time{"asks": now.Add(-time.Minute)}},
			OK, []string{"phone"}},
		"fine": {NotificationsInput{On: true, Now: now, Targets: ok}, OK, []string{"phone", "here"}},
	} {
		got := EvaluateNotifications(c.in)
		if got.Status != c.status {
			t.Errorf("%s: %s %q, want %s", name, got.Status, got.Detail, c.status)
		}
		for _, s := range c.detail {
			if !strings.Contains(got.Detail, s) {
				t.Errorf("%s: detail %q lacks %q", name, got.Detail, s)
			}
		}
	}
}

func TestNotificationsCheckIsForYouNeverAFail(t *testing.T) {
	var d Definition
	for _, x := range Definitions() {
		if x.ID == "notifications" {
			d = x
		}
	}
	if d.ID == "" || d.Place(Warn) != PlaceForYou || d.Command != "agentbus notifications test" || d.Interval != 5*time.Minute {
		t.Fatalf("%+v", d)
	}
}
