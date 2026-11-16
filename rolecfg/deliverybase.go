package rolecfg

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/config"
)

// TemplatePath is project's template: <ProjectsDir>/<project>.toml.
func TemplatePath(env func(string) string, project string) string {
	return filepath.Join(ProjectsDir(env), project+".toml")
}

// DeliveryBase is the base branch project's template names in its
// [delivery] table (dev-branch-flow §4.1: a project may deliver to dev while
// the machine's delivery.base stays main). ok is false when there is no
// template, no table or an empty base: the machine's setting applies. A base
// that is not a branch name, or a template that does not decode, is an error
// naming the file: delivering to a base nobody chose is worse than stopping.
func DeliveryBase(env func(string) string, project string) (base string, ok bool, err error) {
	path := TemplatePath(env, project)
	var tpl struct {
		Delivery struct {
			Base string `toml:"base"`
		} `toml:"delivery"`
	}
	if _, err := toml.DecodeFile(path, &tpl); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("%s: %w", path, err)
	}
	b := tpl.Delivery.Base
	if b == "" {
		return "", false, nil
	}
	if !config.ValidBranchName(b) || b == "HEAD" {
		return "", false, fmt.Errorf("%s: [delivery] base %q is not a valid branch name", path, b)
	}
	return b, true, nil
}
