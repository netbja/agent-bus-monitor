package notify

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// NotifySend is notify-send with an urgency, bounded to 5 s; nil when it is
// not on PATH (dispatchsvc P6, moved from cmd/agentbus notificationscmd.go's
// desktopRun).
func TestNotifySend(t *testing.T) {
	missing := func(string) (string, error) { return "", errors.New("not found") }
	if NotifySend(missing, nil) != nil {
		t.Fatal("no notify-send: a sender")
	}
	var got []string
	var left time.Duration
	run := func(ctx context.Context, bin string, args ...string) error {
		got = append([]string{bin}, args...)
		dl, ok := ctx.Deadline()
		if !ok {
			t.Error("no deadline")
		}
		left = time.Until(dl)
		return nil
	}
	send := NotifySend(func(string) (string, error) { return "/usr/bin/notify-send", nil }, run)
	if err := send(context.Background(), "title", "body", "critical"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/notify-send", "-a", "agentbus", "-u", "critical", "title", "body"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	if left <= 4*time.Second || left > 5*time.Second {
		t.Errorf("bounded to %v, want 5 s", left)
	}
}

// RunQuiet runs a command, its output discarded, and returns its exit.
func TestRunQuiet(t *testing.T) {
	if err := RunQuiet(context.Background(), "true"); err != nil {
		t.Errorf("true: %v", err)
	}
	if err := RunQuiet(context.Background(), "false"); err == nil {
		t.Error("false: no error")
	}
}
