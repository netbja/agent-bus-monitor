// Package projlist is the shared project listing: the project templates on
// disk, annotated with whether the broker holds keys for each. It takes the
// broker lookup as a function and imports no Redis client.
package projlist

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/bus"
)

// State is what is known about a project on the broker.
type State int

const (
	Unknown State = iota
	Present
	Absent
)

func (s State) String() string {
	switch s {
	case Present:
		return "present"
	case Absent:
		return "absent"
	}
	return "?"
}

// Row is one project template and its broker state.
type Row struct {
	Name, WorkingDir, Path string
	Broker                 State
}

// Templates lists the valid-named *.toml templates in dir, sorted by name.
// A missing directory is an empty list; a file that does not parse is skipped.
func Templates(dir string) ([]Row, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []Row
	for _, e := range entries {
		file := e.Name()
		name, ok := strings.CutSuffix(file, ".toml")
		if !ok || e.IsDir() || !bus.ValidName(name) {
			continue
		}
		path := filepath.Join(dir, file)
		var t struct {
			WorkingDir string `toml:"working_dir"`
		}
		if _, err := toml.DecodeFile(path, &t); err != nil {
			continue
		}
		rows = append(rows, Row{Name: name, WorkingDir: t.WorkingDir, Path: path})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

// KeysFunc lists the broker keys of one project (production: bus.ProjectKeys
// bound to a client).
type KeysFunc func(ctx context.Context, project string) ([]string, error)

// Exists asks the broker, bounded by timeout. Keys mean Present, none Absent;
// an error or a timeout is Unknown together with that error.
func Exists(ctx context.Context, keys KeysFunc, project string, timeout time.Duration) (State, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type result struct {
		keys []string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		k, err := keys(ctx, project)
		ch <- result{k, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return Unknown, r.err
		}
		if len(r.keys) > 0 {
			return Present, nil
		}
		return Absent, nil
	case <-ctx.Done():
		return Unknown, ctx.Err()
	}
}

// Annotate returns a copy of rows with Broker set on every row. Rows are
// probed concurrently, so a hung broker costs about one timeout in total.
func Annotate(ctx context.Context, rows []Row, keys KeysFunc, timeout time.Duration) []Row {
	out := make([]Row, len(rows))
	copy(out, rows)
	var wg sync.WaitGroup
	for i := range out {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i].Broker, _ = Exists(ctx, keys, out[i].Name, timeout)
		}()
	}
	wg.Wait()
	return out
}
