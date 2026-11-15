package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/holderdrain"
)

// runHolderDrain is `agentbus --project <p> holder drain [<project>]
// [--ignore-background]`: the
// person finishes an old holder build's drain (herdr exit spec 4.4,
// holderdrain). Exit 0 drained, 1 failed, 3 agents skipped (run it again).
func runHolderDrain(project, host string, args []string, stdout, stderr io.Writer) int {
	ignoreBG := false
	if i := slices.Index(args, "--ignore-background"); i >= 0 {
		ignoreBG, args = true, slices.Delete(slices.Clone(args), i, i+1)
	}
	switch {
	case len(args) == 1 && project == "":
		project = args[0]
	case len(args) == 1 && args[0] != project:
		fmt.Fprintf(stderr, "holder drain: project %s and --project %s differ\n", args[0], project)
		return 2
	case len(args) > 1:
		fmt.Fprintln(stderr, "usage: agentbus holder drain <project>")
		return 2
	}
	if !bus.ValidName(project) {
		fmt.Fprintln(stderr, "usage: agentbus holder drain <project>")
		return 2
	}
	c, err := bus.Connect(host)
	if err != nil {
		fmt.Fprintf(stderr, "holder drain: %v\n", err)
		return 1
	}
	defer c.Close()
	b, err := bus.Open(c, project)
	if err != nil {
		fmt.Fprintf(stderr, "holder drain: %v\n", err)
		return 1
	}
	h, err := holder.NewHost(project, holder.BuildID(), os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "holder drain: %v\n", err)
		return 1
	}
	gc := func() error {
		if code := gcHolderCmd(realServiceCmdDeps(host), project, stdout, stderr); code != 0 {
			return fmt.Errorf("agentbus service gc exited %d", code)
		}
		return nil
	}
	d := holderdrain.NewDeps(project, h, b, holder.BuildID(), gc)
	d.IgnoreBackground = ignoreBG
	return holderdrain.Run(context.Background(), d, stdout)
}
