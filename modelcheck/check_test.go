package modelcheck

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/models"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// models check names what uses a profile: claude-fable is architect's
// alternate (fable-profile-architect), so a gone Fable model is not read as
// an unused profile.
func TestModelsCheckNamesAnAlternate(t *testing.T) {
	m, err := rolecfg.Load(filepath.Join("..", "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	rep := ModelsCheckReport(&models.Table{}, models.Config{}, m, time.Now())
	for _, r := range rep.Profiles {
		if r.Profile == "claude-fable" {
			if !slices.Contains(r.UsedBy, "alt of architect") {
				t.Fatalf("claude-fable used by %v", r.UsedBy)
			}
			return
		}
	}
	t.Fatal("no claude-fable row")
}
