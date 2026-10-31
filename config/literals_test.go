package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// notYetMigrated are the env names whose readers still read them raw; each
// migration step empties a part of it, and the last one deletes it.
var notYetMigrated = map[string]bool{
	"AGENT_BUS_HUMAN":                        true, // agentbus ask, plugin
	"AGENT_BUS_CONTEXT_ALERT":                true,
	"AGENT_BUS_PROMPT_GRACE":                 true,
	"AGENT_BUS_QUOTA":                        true,
	"AGENT_BUS_QUOTA_WARN":                   true,
	"AGENT_BUS_QUOTA_HOLD":                   true,
	"AGENT_BUS_QUOTA_MAX_AGE":                true,
	"AGENT_BUS_QUOTA_LONG_HOLD":              true,
	"AGENT_BUS_TOKENS":                       true,
	"AGENT_BUS_AUTO_CLEAR":                   true,
	"AGENT_BUS_REPORT_MAX":                   true,
	"AGENT_BUS_REPORT_FULL_MAX":              true,
	"AGENT_BUS_VERDICT_MAX":                  true,
	"AGENTBUS_HEALTH_RUNAWAY_SILENT_AFTER":   true,
	"AGENTBUS_HEALTH_RUNAWAY_CONTEXT_GROWTH": true,
	"AGENTBUS_HEALTH_RUNAWAY_GROWTH_SPAN":    true,
	"AGENTBUS_HEALTH_RUNAWAY_SCREEN_REPEATS": true,
}

// TestNoEnvLiteralOutsideTheRegistry: a reader resolves a setting through the
// registry, so no registry env name is a string literal anywhere else in the
// Go sources (tests aside: they set the environment). Readers go through
// injected getenv functions and wrappers, which an os.Getenv search would miss.
func TestNoEnvLiteralOutsideTheRegistry(t *testing.T) {
	names := map[string]bool{}
	for _, k := range Keys {
		if k.Env != "" {
			names[k.Env] = true
		}
	}
	for n := range notYetMigrated {
		if !names[n] {
			t.Errorf("notYetMigrated names %s, which is not in the registry", n)
		}
	}
	root := ".."
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".worktrees" || d.Name() == "testdata" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == filepath.Join("..", "config", "keys.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err == nil && names[v] && !notYetMigrated[v] {
				t.Errorf("%s: %s read outside the registry", fset.Position(lit.Pos()), v)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
