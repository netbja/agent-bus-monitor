package setup

import (
	"bytes"
	"testing"
)

// The log lines are setup.sh's, prefixes and colours byte for byte.
func TestOutputLinesMatchTheBash(t *testing.T) {
	for _, color := range []bool{false, true} {
		var out, errw bytes.Buffer
		o := NewOutput(&out, &errw, color)
		o.Step("1/5 Checking")
		o.Info("info")
		o.OK("ok")
		o.Warn("warn")
		o.Error("err")
		c := func(code string) string {
			if color {
				return "\033[" + code + "m"
			}
			return ""
		}
		wantOut := "\n" + c("36") + "==>" + c("0") + " " + c("1") + "1/5 Checking" + c("0") + "\n" +
			"    " + c("2") + "•" + c("0") + " info\n" +
			"    " + c("32") + "✓" + c("0") + " ok\n" +
			"    " + c("33") + "!" + c("0") + " " + c("33") + "warn" + c("0") + "\n"
		wantErr := "    " + c("31") + "✗" + c("0") + " " + c("31") + "err" + c("0") + "\n"
		if out.String() != wantOut || errw.String() != wantErr || o.Warnings != 1 {
			t.Fatalf("color %v:\nout %q\nwant %q\nerr %q\nwant %q", color, out.String(), wantOut, errw.String(), wantErr)
		}
		errw.Reset()
		o.Fatal("boom")
		if want := "\n" + c("31") + "error:" + c("0") + " boom\n"; errw.String() != want {
			t.Fatalf("fatal %q, want %q", errw.String(), want)
		}
	}
}
