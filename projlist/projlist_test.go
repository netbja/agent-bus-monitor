package projlist

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTemplatesReadsValidNamesOnly(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "demo.toml", "name = \"demo\"\nworking_dir = \"/work/demo\"\n")
	write(t, dir, "Bad.toml", "working_dir = \"/x\"\n")
	write(t, dir, "x.txt", "working_dir = \"/x\"\n")
	write(t, dir, "broken.toml", "working_dir = [[[")
	rows, err := Templates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "demo" || rows[0].WorkingDir != "/work/demo" ||
		rows[0].Path != filepath.Join(dir, "demo.toml") || rows[0].Broker != Unknown {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestTemplatesSortedByName(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "zeta.toml", "working_dir = \"/z\"\n")
	write(t, dir, "alpha.toml", "working_dir = \"/a\"\n")
	rows, err := Templates(dir)
	if err != nil || len(rows) != 2 || rows[0].Name != "alpha" || rows[1].Name != "zeta" {
		t.Fatalf("rows = %+v err = %v", rows, err)
	}
}

func TestTemplatesMissingDirIsEmpty(t *testing.T) {
	rows, err := Templates(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows = %+v err = %v", rows, err)
	}
}

func TestExistsStates(t *testing.T) {
	ctx := context.Background()
	ret := func(k []string, e error) KeysFunc {
		return func(context.Context, string) ([]string, error) { return k, e }
	}
	if s, err := Exists(ctx, ret([]string{"demo:status"}, nil), "demo", time.Second); s != Present || err != nil {
		t.Fatalf("present: %v %v", s, err)
	}
	if s, err := Exists(ctx, ret([]string{}, nil), "demo", time.Second); s != Absent || err != nil {
		t.Fatalf("absent: %v %v", s, err)
	}
	boom := errors.New("boom")
	if s, err := Exists(ctx, ret(nil, boom), "demo", time.Second); s != Unknown || !errors.Is(err, boom) {
		t.Fatalf("error: %v %v", s, err)
	}
	block := func(c context.Context, _ string) ([]string, error) { <-c.Done(); return nil, c.Err() }
	start := time.Now()
	s, err := Exists(ctx, block, "demo", 50*time.Millisecond)
	if s != Unknown || err == nil || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("timeout: %v %v %v", s, err, time.Since(start))
	}
}

func TestExistsIgnoresKeysFuncThatNeverReturns(t *testing.T) {
	hang := func(context.Context, string) ([]string, error) { select {} }
	start := time.Now()
	s, err := Exists(context.Background(), hang, "demo", 50*time.Millisecond)
	if s != Unknown || err == nil || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("%v %v %v", s, err, time.Since(start))
	}
}

func TestAnnotate(t *testing.T) {
	keys := func(_ context.Context, p string) ([]string, error) {
		switch p {
		case "a":
			return []string{"a:status"}, nil
		case "b":
			return nil, nil
		}
		return nil, errors.New("down")
	}
	rows := []Row{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	out := Annotate(context.Background(), rows, keys, time.Second)
	if out[0].Broker != Present || out[1].Broker != Absent || out[2].Broker != Unknown {
		t.Fatalf("%+v", out)
	}
	if rows[0].Broker != Unknown {
		t.Fatal("input mutated")
	}
}

func TestStateString(t *testing.T) {
	if Unknown.String() != "?" || Present.String() != "present" || Absent.String() != "absent" {
		t.Fatal("strings")
	}
}
