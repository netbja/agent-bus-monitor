package service

import "testing"

// The state name busmon's Services page matches as a string (services.go,
// services_cards.go): renaming it here silently breaks that page (dispatchsvc
// P2, moved from cmd/agentbus servicerun.go).
func TestGitWaitState(t *testing.T) {
	if GitWaitState != "waiting-git" {
		t.Errorf("GitWaitState = %q, want waiting-git", GitWaitState)
	}
}
