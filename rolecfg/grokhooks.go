package rolecfg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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

// errGrokExclude: the hook file is in place, but the repository's local
// exclude could not list it.
var errGrokExclude = errors.New("not listed in the repository's .git/info/exclude")

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
		path, _, err = installGrokHooks(e, dir, grokHooksJSON(launchHooks(e)))
		if err == nil || errors.Is(err, errGrokExclude) {
			fmt.Fprintf(stderr, "note: grok turn hooks in %s; Grok runs them once you trust this folder (its own dialog, answered once)\n", path)
		}
		if err == nil {
			return
		}
		if errors.Is(err, errGrokExclude) {
			fmt.Fprintf(stderr, "warning: %v: it holds this machine's agentbus path, keep it out of commits\n", err)
			return
		}
	}
	fmt.Fprintf(stderr, "warning: grok turn hooks not installed (%v); the state follows declarations only\n", err)
}

// installGrokHooks writes text to <dir>/.grok/hooks/agentbus.json when the
// file is absent or is the one agentbus wrote last, unedited (its hash kept in
// the launch cache); any other file is the person's and stays. Never the home
// folder (that is ~/.grok, the person's own hooks and trust store), never
// through a link. ours says the file in place is agentbus's: then, in a Git
// checkout, the repository's local exclude lists it, since it holds this
// machine's agentbus path (never a .gitignore, which would be committed).
func installGrokHooks(e *envMap, dir, text string) (file string, ours bool, err error) {
	file, ours, err = writeGrokHooks(e, dir, text)
	if err == nil && ours {
		if xerr := excludeFromGit(file); xerr != nil {
			err = fmt.Errorf("%s: %w: %v", file, errGrokExclude, xerr)
		}
	}
	return file, ours, err
}

func writeGrokHooks(e *envMap, dir, text string) (string, bool, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false, err
	}
	if home := homeDir(e); home != "" {
		if h, err := filepath.EvalSymlinks(home); err == nil && h == real {
			return "", false, fmt.Errorf("%s is the home folder: its .grok is the person's own", dir)
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
				return "", false, err
			}
		case err != nil:
			return "", false, err
		case !st.IsDir(): // a symlink included: Lstat does not follow it
			return "", false, fmt.Errorf("%s is not a plain folder", d)
		}
	}
	record, err := grokHooksRecord(e, file)
	if err != nil {
		return "", false, err
	}
	sum := sha256.Sum256([]byte(text))
	if st, err := os.Lstat(file); err == nil {
		if !st.Mode().IsRegular() {
			return file, false, fmt.Errorf("%s: not a regular file: %w", file, errGrokHooksNotOurs)
		}
		old, err := os.ReadFile(file)
		if err != nil {
			return file, false, err
		}
		kept, _ := os.ReadFile(record)
		oldSum := sha256.Sum256(old)
		recorded := string(bytes.TrimSpace(kept)) == hex.EncodeToString(oldSum[:])
		// Ours only when the record says so: an identical file agentbus did
		// not write (the person's copy, or ours whose record was lost) is used
		// as it is and never adopted, so a later text never replaces it.
		if bytes.Equal(old, []byte(text)) {
			return file, recorded, nil
		}
		if !recorded {
			return file, false, fmt.Errorf("%s: %w", file, errGrokHooksNotOurs)
		}
	} else if !os.IsNotExist(err) {
		return file, false, err
	}
	tmp, err := os.CreateTemp(hooksDir, ".agentbus-*.tmp")
	if err != nil {
		return file, false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return file, false, err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return file, false, err
	}
	if err := tmp.Close(); err != nil {
		return file, false, err
	}
	// The record first: a crash before the rename leaves the old file under a
	// record that no longer matches it, so the next launch keeps it and says so.
	if err := os.WriteFile(record, []byte(hex.EncodeToString(sum[:])+"\n"), 0o600); err != nil {
		return file, false, err
	}
	if err := os.Rename(tmp.Name(), file); err != nil {
		return file, false, err
	}
	return file, true, nil
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

// excludeFromGit lists file, by its path from the top of the Git checkout it
// is in, in that repository's local exclude (the common one for a linked
// worktree), once. Outside a checkout it does nothing.
func excludeFromGit(file string) error {
	dir := filepath.Dir(file)
	top, err := gitIn(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil // not a checkout, or no git: nothing to list it in
	}
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	rel, err := filepath.Rel(top, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s is not under %s", file, top)
	}
	if strings.ContainsAny(rel, "\n\r") {
		return fmt.Errorf("%q holds a line break: it cannot be one exclude line", rel)
	}
	exclude, err := gitIn(dir, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	line := "/" + gitignoreLiteral(filepath.ToSlash(rel))
	old, err := os.ReadFile(exclude)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(old), "\n") {
		if strings.TrimSpace(l) == line {
			return checkIgnored(dir, file)
		}
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(exclude, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	add := "# agentbus: Grok turn hooks, this machine's agentbus path\n" + line + "\n"
	if len(old) > 0 && old[len(old)-1] != '\n' {
		add = "\n" + add
	}
	if _, err := f.WriteString(add); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return checkIgnored(dir, file)
}

// checkIgnored asks git whether the file is now ignored, by the patterns
// alone (--no-index), so a line that does not match it is said, never silent.
func checkIgnored(dir, file string) error {
	if _, err := gitIn(dir, "check-ignore", "-q", "--no-index", file); err != nil {
		return fmt.Errorf("git check-ignore does not see %s as ignored", file)
	}
	return nil
}

// gitignoreLiteral is path as a gitignore pattern that matches only itself:
// the pattern characters [ ] * ? and the backslash escaped. The line begins
// with "/" and ends in agentbus.json, so neither a leading # or ! nor a
// trailing space can occur.
func gitignoreLiteral(path string) string {
	var b strings.Builder
	for _, r := range path {
		if strings.ContainsRune(`[]*?\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// gitIn runs git in dir, bounded, without the caller's GIT_DIR-like
// variables (a hook's or a worktree's would point it elsewhere), and returns
// its trimmed output.
func gitIn(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); k != "GIT_DIR" && k != "GIT_WORK_TREE" && k != "GIT_COMMON_DIR" && k != "GIT_INDEX_FILE" {
			c.Env = append(c.Env, kv)
		}
	}
	out, err := c.Output()
	return strings.TrimSpace(string(out)), err
}
