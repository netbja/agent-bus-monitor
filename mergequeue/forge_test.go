package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGH puts a gh on a fresh PATH that prints out (or fails with code), and
// writes its environment and arguments beside it.
func fakeGH(t *testing.T, out string, code int) (dir string, env []string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.json"), []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nenv > %q/env.txt\necho \"$@\" > %q/args.txt\ncat %q/out.json\nexit %d\n", dir, dir, dir, code)
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, []string{"PATH=" + dir + ":/usr/bin:/bin", "GH_PROMPT_DISABLED=1", "LC_ALL=C"}
}

func prJSON(n int, head string) string {
	return fmt.Sprintf(`{"number":%d,"title":"t%d","url":"https://github.com/o/r/pull/%d","headRefName":"b%d","headRefOid":%q,"baseRefName":"main","isDraft":false,"mergeable":"MERGEABLE"}`, n, n, n, n, head)
}

func TestReadGitHub(t *testing.T) {
	out := "[" + prJSON(1, hA) + `,{"number":2,"title":"draft \u001b[31mred","url":"u","headRefName":"d","headRefOid":"` + hB + `","baseRefName":"main","isDraft":true,"mergeable":"CONFLICTING"}]`
	dir, env := fakeGH(t, out, 0)
	prs, err := ReadGitHub(context.Background(), dir, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 || prs[0].Number != 1 || prs[0].Head != hA || prs[0].Branch != "b1" || prs[0].URL != "https://github.com/o/r/pull/1" || !prs[1].Draft ||
		prs[0].Conflicting || !prs[1].Conflicting {
		t.Fatalf("%+v", prs)
	}
	if strings.ContainsRune(prs[1].Title, 0x1b) {
		t.Fatalf("a control reached the title: %q", prs[1].Title)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if want := "pr list --state open --json number,title,url,headRefName,headRefOid,baseRefName,isDraft,mergeable --limit 1000"; strings.TrimSpace(string(args)) != want {
		t.Fatalf("args %q", args)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "env.txt"))
	if !strings.Contains(string(got), "GH_PROMPT_DISABLED=1") {
		t.Fatalf("env %s", got)
	}
}

// A list of exactly the limit may be cut: incomplete, never read as whole
// (reviewer-codex); a failed call is an error.
func TestReadGitHubIncompleteAndFailures(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for i := 1; i <= ghLimit; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		b.WriteString(prJSON(i, hC))
	}
	b.WriteString("]")
	dir, env := fakeGH(t, b.String(), 0)
	if _, err := ReadGitHub(context.Background(), dir, env); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("a full page: %v", err)
	}
	for name, c := range map[string]struct {
		out  string
		code int
	}{
		"exit 1":     {"error connecting to api.github.com", 1},
		"not json":   {"<html>", 0},
		"bad head":   {"[" + prJSON(1, "nothex") + "]", 0},
		"bad number": {"[" + prJSON(0, hA) + "]", 0},
	} {
		dir, env := fakeGH(t, c.out, c.code)
		if _, err := ReadGitHub(context.Background(), dir, env); err == nil {
			t.Fatalf("%s: no error", name)
		}
	}
}

func TestReadGitHubTimeout(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := ReadGitHub(ctx, dir, []string{"PATH=" + dir + ":/usr/bin:/bin"}); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("err %v after %s", err, time.Since(start))
	}
}
