package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type bgCall struct {
	agent, text, by string
	d               time.Duration
	clear           bool
}

func bgDeps(calls *[]bgCall, err error) backgroundDeps {
	return backgroundDeps{
		Self: "master",
		Set: func(_ context.Context, agent, text, by string, d time.Duration) error {
			*calls = append(*calls, bgCall{agent: agent, text: text, by: by, d: d})
			return err
		},
		Clear: func(_ context.Context, agent string) error {
			*calls = append(*calls, bgCall{agent: agent, clear: true})
			return err
		},
	}
}

// progress-line §4.2: background <agent> "<text>" [--for d] | <agent> --clear.
func TestRunBackground(t *testing.T) {
	for _, c := range []struct {
		args []string
		code int
		want bgCall
		out  string
	}{
		{[]string{"master", "2", "checks", "running"}, 0, bgCall{agent: "master", text: "2 checks running", by: "master", d: 30 * time.Minute}, "until"},
		{[]string{"master", "2 checks running", "--for", "20m"}, 0, bgCall{agent: "master", text: "2 checks running", by: "master", d: 20 * time.Minute}, "20m"},
		{[]string{"master", "--clear"}, 0, bgCall{agent: "master", clear: true}, "cleared"},
	} {
		var calls []bgCall
		var out, errb bytes.Buffer
		if code := runBackground(context.Background(), bgDeps(&calls, nil), c.args, &out, &errb); code != c.code || len(calls) != 1 || calls[0] != c.want || !strings.Contains(out.String(), c.out) {
			t.Errorf("%v: code %d calls %+v out %q err %q", c.args, code, calls, out.String(), errb.String())
		}
	}
	for _, args := range [][]string{
		nil,                              // no agent
		{"master"},                       // no text
		{"master", "x", "--for", "soon"}, // not a duration
		{"master", "x", "--clear"},       // both
	} {
		var calls []bgCall
		var out, errb bytes.Buffer
		if code := runBackground(context.Background(), bgDeps(&calls, nil), args, &out, &errb); code != 2 || len(calls) != 0 || !strings.Contains(errb.String(), "usage") {
			t.Errorf("%v: code %d calls %+v err %q", args, code, calls, errb.String())
		}
	}
	var calls []bgCall
	var out, errb bytes.Buffer
	if code := runBackground(context.Background(), bgDeps(&calls, errors.New("at most 24h")), []string{"master", "x", "--for", "25h"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "24h") {
		t.Errorf("a refused write: %d %q", code, errb.String())
	}
}

// The identity guard counts it as a write; the command table knows it.
func TestBackgroundIsAWrite(t *testing.T) {
	if readOnlyCommand("background", []string{"master", "x"}) {
		t.Fatal("background read as read-only")
	}
	found := false
	for _, c := range commands {
		if c.Name == "background" {
			found = true
		}
	}
	if !found {
		t.Fatal("no background in the command table")
	}
}
