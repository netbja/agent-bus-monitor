package health

import (
	"strings"
	"testing"
	"time"
)

func TestEffectiveStatusUnknownForMissingAndStaleRecords(t *testing.T) {
	now := int64(2_000_000)
	interval := time.Minute
	cases := []struct {
		name string
		rec  Record
		want Status
	}{
		{"missing", Record{}, Unknown},
		{"fresh", Record{ID: "runaway", Status: Warn, ObservedAt: now - 1}, Warn},
		{"stale", Record{ID: "runaway", Status: OK, ObservedAt: now - interval.Milliseconds()*2}, Unknown},
		{"bad status", Record{ID: "runaway", Status: "surprise", ObservedAt: now - 1}, Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveStatus(tc.rec, now, interval); got != tc.want {
				t.Fatalf("EffectiveStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCatalogueHasUniqueIDsAndBoundedIntervals(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Definitions() {
		if d.ID == "" || seen[d.ID] || d.Interval <= 0 || d.Deadline <= 0 || d.Remediation == "" {
			t.Fatalf("invalid catalogue definition: %+v", d)
		}
		seen[d.ID] = true
	}
	for _, excluded := range []string{"secret-scanner", "canary", "project-checks", "key-inventory"} {
		if _, ok := ByID(excluded); ok {
			t.Errorf("out-of-scope check %q is registered", excluded)
		}
	}
}

func TestSafeTextRejectsOversizedText(t *testing.T) {
	if SafeText(strings.Repeat("x", 601)) {
		t.Fatal("SafeText accepted oversized text")
	}
	if !SafeText("service is ready; restart budget low") {
		t.Fatal("SafeText rejected a safe diagnostic")
	}
}

func TestEvaluatorsCoverBoundariesAndUnknownEvidence(t *testing.T) {
	t.Run("services", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			in   ServiceEvidence
			want Status
		}{
			{"unavailable", ServiceEvidence{}, Unknown},
			{"uninstalled", ServiceEvidence{Known: true}, OK},
			{"inactive", ServiceEvidence{Known: true, Installed: true}, Fail},
			{"budget warning", ServiceEvidence{Known: true, Installed: true, Active: true, Failures: map[string]int{"dispatch": 4}}, Warn},
			{"budget spent", ServiceEvidence{Known: true, Installed: true, Active: true, Failures: map[string]int{"missions": 5}}, Fail},
			{"missions waits for git", ServiceEvidence{Known: true, Installed: true, Active: true, WaitingGit: true}, Warn},
			{"inactive beats waiting", ServiceEvidence{Known: true, Installed: true, WaitingGit: true}, Fail},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if got := EvaluateServices(tc.in).Status; got != tc.want {
					t.Fatalf("got %s want %s", got, tc.want)
				}
			})
		}
	})
	t.Run("identity", func(t *testing.T) {
		if got := EvaluateIdentity(IdentityEvidence{Contradiction: true}).Status; got != Fail {
			t.Fatalf("positive contradiction must dominate missing evidence: %s", got)
		}
		if got := EvaluateIdentity(IdentityEvidence{Known: true, Active: true}).Status; got != Warn {
			t.Fatalf("unverified active identity = %s", got)
		}
	})
	t.Run("runaway", func(t *testing.T) {
		if got := EvaluateRunaway(RunawaySilentAfter, 0, 0, 0).Status; got != Warn {
			t.Fatalf("silent boundary = %s", got)
		}
		if got := EvaluateRunaway(0, RunawayFastGrowth, RunawayGrowthSpan, 4).Status; got != Warn {
			t.Fatalf("repeat/growth boundary = %s", got)
		}
		if got := EvaluateRunaway(0, RunawayFastGrowth, RunawayGrowthSpan, 3).Status; got != OK {
			t.Fatalf("below repeat boundary = %s", got)
		}
	})
	t.Run("delivery", func(t *testing.T) {
		for _, tc := range []struct {
			in   DeliveryEvidence
			want Status
		}{
			{DeliveryEvidence{}, Unknown},
			{DeliveryEvidence{Known: true, HasPending: true, Pending: PendingWarnAfter}, Warn},
			{DeliveryEvidence{Known: true, HasPending: true, Pending: PendingFailAfter}, Fail},
			{DeliveryEvidence{Known: true, Lag: LagWarnAt}, Warn},
			{DeliveryEvidence{Known: true, Lag: LagFailAt}, Fail},
		} {
			if got := EvaluateDelivery(tc.in).Status; got != tc.want {
				t.Fatalf("EvaluateDelivery(%+v) = %s want %s", tc.in, got, tc.want)
			}
		}
	})
	t.Run("quota", func(t *testing.T) {
		now := time.Unix(10_000_000, 0)
		if got := EvaluateQuotaFreshness(true, false, 0, now).Status; got != Unknown {
			t.Fatalf("unknown quota = %s", got)
		}
		if got := EvaluateQuotaFreshness(true, true, now.Add(-QuotaStaleAfter).UnixMilli(), now).Status; got != OK {
			t.Fatalf("exact quota boundary = %s", got)
		}
	})
}

func TestSecretPathMatchingAndVersionOutcomes(t *testing.T) {
	for _, tc := range []struct {
		path   string
		secret bool
	}{
		{".env", true}, {".env.production", true}, {".env.example", false}, {"id_ed25519.pub", true},
		{"wallet-main.json", true}, {"keypair.json", true}, {"README.md", false},
	} {
		_, got := IsSecretLikePath(tc.path)
		if got != tc.secret {
			t.Errorf("IsSecretLikePath(%q) = %v", tc.path, got)
		}
	}
	if got := EvaluateSecretPaths([]string{"docs/.env.production"}, true).Status; got != Fail {
		t.Fatalf("tracked secret = %s", got)
	}
	if got := EvaluateSecretPaths(nil, false).Status; got != Unknown {
		t.Fatalf("unknown secret scan = %s", got)
	}
	if got := EvaluateVersions(false, 0).Status; got != Unknown {
		t.Fatalf("incomplete version inventory = %s", got)
	}
	if got := EvaluateVersions(true, 1).Status; got != Warn {
		t.Fatalf("version issue = %s", got)
	}
	if got := EvaluateHerdrProbe(false, false, 0).Status; got != Unknown {
		t.Fatalf("missing herdr = %s", got)
	}
	if got := EvaluateHerdrProbe(true, true, 1).Status; got != Warn {
		t.Fatalf("transient herdr error = %s", got)
	}
	if got := EvaluateHerdrProbe(true, true, HerdrProbeFailAfter).Status; got != Fail {
		t.Fatalf("repeated herdr error = %s", got)
	}
}
