package rolecfg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Grok's turn hooks (auto-state step 5, route b): Grok 1.0.70 reads project
// hooks from <project>/.grok/hooks/*.json once the folder is trusted (its own
// startup dialog, recorded in ~/.grok/trusted_folders.toml; never bypassed
// here), and runs each hook as a child of the grok process with its
// environment, so the hook's chain reaches the bound pid. Turn start on
// UserPromptSubmit, turn end on Stop and StopFailure. StopCancelled is left
// out: Grok sends it off the command loop, so it can arrive after the next
// turn's UserPromptSubmit; an interrupted turn is read idle by
// bus.EffectiveState instead.

// grokHookTimeout is each hook's own bound, in seconds: Stop runs on the
// turn's critical path (Grok's default there is 600 s), and roles turn ends
// within 2 s anyway.
const grokHookTimeout = 5

// errGrokHooksNotOurs: agentbus.json exists and is not the file agentbus
// wrote last, unedited; it is left as it is.
var errGrokHooksNotOurs = errors.New("not the file agentbus wrote (edited, or never ours): left as it is")

// launchDir is the folder the client starts in, the launcher's own.
var launchDir = os.Getwd

type grokHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type grokGroup struct {
	Hooks []grokHook `json:"hooks"`
}

// grokHooksJSON is the hook file's text: one group of one command per event.
func grokHooksJSON(h claudeHooks) string {
	group := func(command string) []grokGroup {
		return []grokGroup{{Hooks: []grokHook{{Type: "command", Command: command, Timeout: grokHookTimeout}}}}
	}
	doc := struct {
		Hooks struct {
			UserPromptSubmit []grokGroup `json:"UserPromptSubmit"`
			Stop             []grokGroup `json:"Stop"`
			StopFailure      []grokGroup `json:"StopFailure"`
		} `json:"hooks"`
	}{}
	doc.Hooks.UserPromptSubmit = group(h.TurnStart)
	doc.Hooks.Stop = group(h.TurnEnd)
	doc.Hooks.StopFailure = group(h.TurnEnd)
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(doc) // strings only: it cannot fail
	return b.String()
}

// grokTurnHooks writes the hook file in the folder Grok starts in. A file it
// cannot write never stops the agent: it starts without turn hooks, and
// stderr says why.
func grokTurnHooks(e *envMap, stderr io.Writer) {
	dir, err := launchDir()
	if err == nil {
		var path string
		path, err = installGrokHooks(e, dir, grokHooksJSON(launchHooks(e)))
		if err == nil {
			fmt.Fprintf(stderr, "note: grok turn hooks in %s; Grok runs them once you trust this folder (its own dialog, answered once)\n", path)
			return
		}
	}
	fmt.Fprintf(stderr, "warning: grok turn hooks not installed (%v); the state follows declarations only\n", err)
}

// installGrokHooks writes text to <dir>/.grok/hooks/agentbus.json when the
// file is absent or is the one agentbus wrote last, unedited (its hash kept in
// the launch cache); any other file is the person's and stays. Never the home
// folder (that is ~/.grok, the person's own hooks and trust store), never
// through a link.
func installGrokHooks(e *envMap, dir, text string) (string, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if home := homeDir(e); home != "" {
		if h, err := filepath.EvalSymlinks(home); err == nil && h == real {
			return "", fmt.Errorf("%s is the home folder: its .grok is the person's own", dir)
		}
	}
	grokDir := filepath.Join(real, ".grok")
	hooksDir := filepath.Join(grokDir, "hooks")
	file := filepath.Join(hooksDir, "agentbus.json")
	for _, d := range []string{grokDir, hooksDir} {
		st, err := os.Lstat(d)
		switch {
		case os.IsNotExist(err):
			if err := os.Mkdir(d, 0o755); err != nil {
				return "", err
			}
		case err != nil:
			return "", err
		case !st.IsDir(): // a symlink included: Lstat does not follow it
			return "", fmt.Errorf("%s is not a plain folder", d)
		}
	}
	record, err := grokHooksRecord(e, file)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(text))
	if st, err := os.Lstat(file); err == nil {
		if !st.Mode().IsRegular() {
			return file, fmt.Errorf("%s: not a regular file: %w", file, errGrokHooksNotOurs)
		}
		old, err := os.ReadFile(file)
		if err != nil {
			return file, err
		}
		if bytes.Equal(old, []byte(text)) {
			return file, os.WriteFile(record, []byte(hex.EncodeToString(sum[:])+"\n"), 0o600)
		}
		kept, _ := os.ReadFile(record)
		oldSum := sha256.Sum256(old)
		if string(bytes.TrimSpace(kept)) != hex.EncodeToString(oldSum[:]) {
			return file, fmt.Errorf("%s: %w", file, errGrokHooksNotOurs)
		}
	} else if !os.IsNotExist(err) {
		return file, err
	}
	tmp, err := os.CreateTemp(hooksDir, ".agentbus-*.tmp")
	if err != nil {
		return file, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return file, err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return file, err
	}
	if err := tmp.Close(); err != nil {
		return file, err
	}
	// The record first: a crash before the rename leaves the old file under a
	// record that no longer matches it, so the next launch keeps it and says so.
	if err := os.WriteFile(record, []byte(hex.EncodeToString(sum[:])+"\n"), 0o600); err != nil {
		return file, err
	}
	return file, os.Rename(tmp.Name(), file)
}

// grokHooksRecord is where the hash of the hook file agentbus last wrote at
// path is kept: the launch cache, one entry per file.
func grokHooksRecord(e *envMap, path string) (string, error) {
	root, err := launchCache(e)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "grok-hooks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(path))
	return filepath.Join(dir, hex.EncodeToString(key[:])), nil
}
