package cli

import (
	"slices"
	"strings"
	"testing"
)

var testGlobals = []Flag{
	{Long: "project", Short: 'p', Arg: "<project>"},
	{Long: "host", Short: 'H', Arg: "<host>"},
}

var testFlags = []Flag{
	{Long: "json", Short: 'j'},
	{Long: "ref", Short: 'r', Arg: "<id>"},
	{Long: "branch", Arg: "<b>"},
}

func TestExpand(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"global short", []string{"-p", "demo", "agents"}, []string{"--project", "demo", "agents"}},
		{"command short", []string{"coder", "-r", "T1", "-j"}, []string{"coder", "--ref", "T1", "--json"}},
		{"long with equals", []string{"--ref=T1", "--project=demo"}, []string{"--ref", "T1", "--project", "demo"}},
		{"equals on a boolean is left alone", []string{"--json=yes"}, []string{"--json=yes"}},
		{"unknown short left alone", []string{"-n", "50", "-z"}, []string{"-n", "50", "-z"}},
		{"no bundling", []string{"-jr"}, []string{"-jr"}},
		{"no attached value", []string{"-pdemo"}, []string{"-pdemo"}},
		{"negative number", []string{"-1"}, []string{"-1"}},
		{"long untouched", []string{"--ref", "x", "--branch", "b"}, []string{"--ref", "x", "--branch", "b"}},
		{"long only flag has no short", []string{"-b"}, []string{"-b"}},
		{"unknown equals left alone", []string{"--nope=1"}, []string{"--nope=1"}},
		{"bare dashes left alone", []string{"-", "--"}, []string{"-", "--"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := slices.Clone(c.in)
			got := Expand(testGlobals, testFlags, in)
			if !slices.Equal(got, c.want) {
				t.Fatalf("Expand(%q) = %q, want %q", c.in, got, c.want)
			}
			if !slices.Equal(in, c.in) {
				t.Fatalf("Expand modified its input: %q", in)
			}
		})
	}
}

var testCmds = []Command{
	{
		Name: "thread", Usage: []string{"thread <thread-id>"},
		Summary:  "show one :cmd thread",
		Examples: []string{"agentbus thread 1792537337865-0"},
	},
	{
		Name: "subscribe", Aliases: []string{"watch"},
		Usage:    []string{"subscribe [--since <id>] <agent> [idle_secs]", "subscribe --drain <agent>"},
		Summary:  "wait for one addressed command",
		Flags:    []Flag{{Long: "since", Short: 's', Arg: "<id>", Help: "cursor"}, {Long: "loop", Help: "headless"}},
		Examples: []string{"agentbus subscribe -s 0 coder", "agentbus subscribe --drain coder"},
	},
	{Name: "plugin", Usage: []string{"plugin projects"}, Summary: "herdr entry points", Internal: true, NoProject: true},
}

func TestRender(t *testing.T) {
	var sb strings.Builder
	Render(&sb, "agentbus", testCmds[1])
	want := `usage: agentbus subscribe [--since <id>] <agent> [idle_secs]
       agentbus subscribe --drain <agent>
alias: watch

wait for one addressed command

flags:
  -s, --since <id>  cursor
      --loop        headless

examples:
  agentbus subscribe -s 0 coder
  agentbus subscribe --drain coder
`
	if sb.String() != want {
		t.Fatalf("Render:\n%s\nwant:\n%s", sb.String(), want)
	}
}

func TestRenderWithoutFlags(t *testing.T) {
	var sb strings.Builder
	Render(&sb, "agentbus", testCmds[0])
	want := `usage: agentbus thread <thread-id>

show one :cmd thread

examples:
  agentbus thread 1792537337865-0
`
	if sb.String() != want {
		t.Fatalf("Render:\n%s\nwant:\n%s", sb.String(), want)
	}
}

func TestIndex(t *testing.T) {
	var sb strings.Builder
	Index(&sb, "agentbus", testGlobals[:1], testCmds)
	want := `usage: agentbus [flags] <command> [args...]

flags:
  -p, --project <project>

commands:
  thread     show one :cmd thread
  subscribe  wait for one addressed command

internal (scripts, herdr, units):
  plugin     herdr entry points

agentbus help <command>, or agentbus <command> --help, shows one command.
`
	if sb.String() != want {
		t.Fatalf("Index:\n%s\nwant:\n%s", sb.String(), want)
	}
}

func TestLookup(t *testing.T) {
	if c, ok := Lookup(testCmds, "watch"); !ok || c.Name != "subscribe" {
		t.Fatalf("Lookup(watch) = %q, %v", c.Name, ok)
	}
	if _, ok := Lookup(testCmds, "nope"); ok {
		t.Fatal("Lookup(nope) found something")
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(testGlobals, testCmds); err != nil {
		t.Fatalf("valid table: %v", err)
	}
	bad := []struct {
		name string
		cmds []Command
	}{
		{"shadows a global short", []Command{{Name: "a", Flags: []Flag{{Long: "pr", Short: 'p'}}}}},
		{"duplicate short", []Command{{Name: "a", Flags: []Flag{{Long: "x", Short: 'x'}, {Long: "y", Short: 'x'}}}}},
		{"duplicate long", []Command{{Name: "a", Flags: []Flag{{Long: "x"}, {Long: "x"}}}}},
		{"long shadows a global", []Command{{Name: "a", Flags: []Flag{{Long: "host"}}}}},
		{"short not a letter", []Command{{Name: "a", Flags: []Flag{{Long: "x", Short: '1'}}}}},
		{"internal with a short", []Command{{Name: "a", Internal: true, Flags: []Flag{{Long: "x", Short: 'x'}}}}},
		{"duplicate name", []Command{{Name: "a"}, {Name: "b", Aliases: []string{"a"}}}},
		{"no usage", []Command{{Name: "a"}}},
	}
	for _, b := range bad {
		for i := range b.cmds {
			if b.name != "no usage" && len(b.cmds[i].Usage) == 0 {
				b.cmds[i].Usage = []string{b.cmds[i].Name}
			}
		}
		if err := Validate(testGlobals, b.cmds); err == nil {
			t.Errorf("%s: Validate accepted it", b.name)
		}
	}
}
