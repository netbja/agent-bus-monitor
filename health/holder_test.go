package health

import (
	"strings"
	"testing"
	"time"
)

// holder (herdr exit 4.4a): fail when the current build's unit is installed
// and no holder of the project answers; ok when none is installed.
func TestEvaluateHolder(t *testing.T) {
	cur := HolderBuild{Key: "k2", Raw: "v2", Current: true, Unit: true, Active: true, Answers: true, Terms: 3}
	for name, c := range map[string]struct {
		e      HolderEvidence
		status Status
		want   string
	}{
		"unknown":         {HolderEvidence{}, Unknown, "unavailable"},
		"none installed":  {HolderEvidence{Known: true}, OK, "no holder service"},
		"only an old one": {HolderEvidence{Known: true, Builds: []HolderBuild{{Key: "k1", Raw: "v1", Unit: true, Answers: true}}}, OK, "no holder service"},
		"answers":         {HolderEvidence{Known: true, Builds: []HolderBuild{cur}}, OK, "v2"},
		"down": {HolderEvidence{Known: true, Builds: []HolderBuild{{Key: "k2", Raw: "v2", Current: true, Unit: true}}},
			Fail, "no holder answers"},
		"an old one answers, the current down": {HolderEvidence{Known: true, Builds: []HolderBuild{
			{Key: "k2", Raw: "v2", Current: true, Unit: true}, {Key: "k1", Raw: "v1", Unit: true, Active: true, Answers: true, Terms: 1}}},
			Fail, "current build v2"},
	} {
		got := EvaluateHolder(c.e)
		if got.Status != c.status || !strings.Contains(got.Detail, c.want) {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

// holder-draining: warn while an older build still holds terminals; an older
// one that answers with none is about to exit.
func TestEvaluateHolderDraining(t *testing.T) {
	cur := HolderBuild{Key: "k2", Raw: "v2", Current: true, Unit: true, Active: true, Answers: true}
	old := HolderBuild{Key: "k1", Raw: "v1", Unit: true, Active: true, Answers: true, Terms: 2, Since: 26 * time.Hour}
	if got := EvaluateHolderDraining(HolderEvidence{Known: true, Builds: []HolderBuild{cur, old}}); got.Status != Warn ||
		!strings.Contains(got.Detail, "v1") || !strings.Contains(got.Detail, "2 terminal") || !strings.Contains(got.Detail, "26h") {
		t.Fatalf("draining: %+v", got)
	}
	old.Terms = 0
	if got := EvaluateHolderDraining(HolderEvidence{Known: true, Builds: []HolderBuild{cur, old}}); got.Status != OK {
		t.Fatalf("an empty old holder: %+v", got)
	}
	if got := EvaluateHolderDraining(HolderEvidence{}); got.Status != Unknown {
		t.Fatalf("unknown: %+v", got)
	}
}

// holder-leftovers: warn when an older build that no longer runs still has
// its unit or its binary copy (a gc that failed); never the current build,
// never one that runs or answers.
func TestEvaluateHolderLeftovers(t *testing.T) {
	cur := HolderBuild{Key: "k3", Raw: "v3", Current: true, Unit: true, Binary: true, Active: true, Answers: true}
	for name, c := range map[string]struct {
		b      HolderBuild
		status Status
	}{
		"unit left":     {HolderBuild{Key: "k1", Raw: "v1", Unit: true}, Warn},
		"binary left":   {HolderBuild{Key: "k1", Raw: "v1", Binary: true}, Warn},
		"still running": {HolderBuild{Key: "k1", Raw: "v1", Unit: true, Binary: true, Active: true}, OK},
		"still answers": {HolderBuild{Key: "k1", Raw: "v1", Unit: true, Binary: true, Answers: true}, OK},
	} {
		got := EvaluateHolderLeftovers(HolderEvidence{Known: true, Builds: []HolderBuild{cur, c.b}})
		if got.Status != c.status || (c.status == Warn && !strings.Contains(got.Detail, "v1")) {
			t.Errorf("%s: %+v", name, got)
		}
	}
	cur.Active, cur.Answers = false, false // the current build down is holder's business, not a leftover
	if got := EvaluateHolderLeftovers(HolderEvidence{Known: true, Builds: []HolderBuild{cur}}); got.Status != OK {
		t.Fatalf("the current build counted: %+v", got)
	}
}
