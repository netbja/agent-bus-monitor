package plan

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRepositoryPlansParse keeps every committed plan loadable: a plan that
// does not parse would be refused by agentbus plan load.
func TestRepositoryPlansParse(t *testing.T) {
	files, _ := filepath.Glob("../docs/plans/*.json")
	if len(files) == 0 {
		t.Skip("no plan in docs/plans")
	}
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(data); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}
