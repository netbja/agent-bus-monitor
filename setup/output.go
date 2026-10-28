// Package setup is `agentbus setup install|update|uninstall|status`, the Go
// port of setup.sh (spec docs/superpowers/specs/2026-10-28-go-installer-design.md).
// Its output is setup.sh's, byte for byte, and every external command goes
// through one injected Runner.
package setup

import (
	"fmt"
	"io"
)

// Output prints setup.sh's log lines: the same prefixes, colours and streams
// (errors on stderr). Warnings counts Warn calls, for the summary.
type Output struct {
	out, err io.Writer
	color    bool
	Warnings int
}

// NewOutput writes to out and errw; color is setup.sh's rule (a terminal on
// stdout and no NO_COLOR), decided by the caller.
func NewOutput(out, errw io.Writer, color bool) *Output {
	return &Output{out: out, err: errw, color: color}
}

func (o *Output) c(code string) string {
	if !o.color {
		return ""
	}
	return "\033[" + code + "m"
}

func (o *Output) Step(s string) {
	fmt.Fprintf(o.out, "\n%s==>%s %s%s%s\n", o.c("36"), o.c("0"), o.c("1"), s, o.c("0"))
}
func (o *Output) Info(s string) { fmt.Fprintf(o.out, "    %s•%s %s\n", o.c("2"), o.c("0"), s) }
func (o *Output) OK(s string)   { fmt.Fprintf(o.out, "    %s✓%s %s\n", o.c("32"), o.c("0"), s) }
func (o *Output) Warn(s string) {
	o.Warnings++
	fmt.Fprintf(o.out, "    %s!%s %s%s%s\n", o.c("33"), o.c("0"), o.c("33"), s, o.c("0"))
}
func (o *Output) Error(s string) {
	fmt.Fprintf(o.err, "    %s✗%s %s%s%s\n", o.c("31"), o.c("0"), o.c("31"), s, o.c("0"))
}

// Fatal prints setup.sh's log_fatal line; the caller exits 1.
func (o *Output) Fatal(s string) {
	fmt.Fprintf(o.err, "\n%serror:%s %s\n", o.c("31"), o.c("0"), s)
}
