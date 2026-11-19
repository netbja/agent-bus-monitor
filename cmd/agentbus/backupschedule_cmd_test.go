package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agentbus backup warns about a defaulted backup.dir while daily is on.
func TestBackupWarnsAboutADefaultDirWhenDaily(t *testing.T) {
	w := newBackupWorld(t)
	os.WriteFile(filepath.Join(w.Home, ".config", "agentbus", "config.toml"), []byte("[backup]\nschedule = \"daily\"\n"), 0o600)
	if _, _, errw := runBackupWorld(w, "--project", w.B.Project()); !strings.Contains(errw, "backup.dir is the default") {
		t.Fatalf("no warning: %q", errw)
	}
}
