package health

import "testing"

// model-detection §3.3: one list of the verdicts that fail, shared by roles
// doctor, model-drift and busmon; newer? is a hint and never fails.
func TestModelVerdictFails(t *testing.T) {
	for v, want := range map[string]bool{"drift": true, "unknown model": true, "gone": true, "newer": true,
		"newer?": false, "ok": false, "not reported": false, "unknown": false, "": false} {
		if ModelVerdictFails(v) != want {
			t.Errorf("%q: %v", v, !want)
		}
	}
	if out := EvaluateModelDrift([]string{"ok", "newer?"}, true); out.Status != OK {
		t.Fatalf("newer? warned: %+v", out)
	}
	if out := EvaluateModelDrift([]string{"ok", "newer"}, true); out.Status != Warn {
		t.Fatalf("newer did not warn: %+v", out)
	}
}
