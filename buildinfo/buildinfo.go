// Package buildinfo carries the release a binary was built from. setup.sh sets
// Version with -ldflags "-X github.com/netbja/agent-bus-monitor/buildinfo.Version=<tag>";
// a plain go build leaves "dev", which never checks for updates.
package buildinfo

import (
	"strconv"
	"strings"
)

// Version is the release tag (v0.11.0, or v0.11.0-3-gabc123 between tags), or "dev".
var Version = "dev"

// Newer reports whether tag a is a strictly newer release than b. Both are
// vMAJOR.MINOR.PATCH, optionally followed by "-…" (git describe); anything
// else, including "dev", is never newer and never older.
func Newer(a, b string) bool {
	pa, ok1 := parse(a)
	pb, ok2 := parse(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	v, ok := strings.CutPrefix(v, "v")
	if !ok {
		return out, false
	}
	v, _, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
