// Package mission runs a change's missions: a versioned missions.json read
// from Git, a pure state machine, and the conductor that requests, checks,
// reviews and integrates each mission (spec
// docs/superpowers/specs/2026-10-11-missions-design.md).
package mission

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

// File is a change's missions.json (§3.1).
type File struct {
	Change      string    `json:"change"`
	Base        string    `json:"base"`
	Branch      string    `json:"branch"`
	Reviewer    string    `json:"reviewer"`
	Test        string    `json:"test"`
	MaxAttempts int       `json:"max_attempts"`
	Missions    []Mission `json:"missions"`
}

// Mission is one task of the plan, as the conductor runs it.
type Mission struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Agent        string   `json:"agent"`
	Reviewer     string   `json:"reviewer,omitempty"`
	DependsOn    []string `json:"depends_on,omitempty"`
	AllowedPaths []string `json:"allowed_paths"`
	Test         string   `json:"test"`
	Timeout      string   `json:"timeout,omitempty"`
	Brief        string   `json:"brief"`
}

// Parse reads missions.json strictly (unknown fields are refused) and fills
// the defaults: base main, branch change/<change>, max_attempts 3.
func Parse(data []byte) (File, error) {
	var f File
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("missions.json: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return File{}, fmt.Errorf("missions.json: data after the top-level object")
	}
	if f.Base == "" {
		f.Base = "main"
	}
	if f.Branch == "" {
		f.Branch = "change/" + f.Change
	}
	if f.MaxAttempts == 0 {
		f.MaxAttempts = 3
	}
	return f, nil
}

// ReviewerOf is the mission's reviewer, or the change's default.
func (f File) ReviewerOf(m Mission) string {
	if m.Reviewer != "" {
		return m.Reviewer
	}
	return f.Reviewer
}

// Mission looks a mission up by id.
func (f File) Mission(id string) (Mission, bool) {
	for _, m := range f.Missions {
		if m.ID == id {
			return m, true
		}
	}
	return Mission{}, false
}

// Hash identifies a mission's definition: the sha256 of its JSON encoding.
func (m Mission) Hash() string {
	b, _ := json.Marshal(m)
	return FileHash(b)
}

// FileHash is the hex sha256 of raw bytes.
func FileHash(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// TaskName is an attempt's request task and review ref: <change>-<id>-<attempt>.
func TaskName(change, id string, attempt int) string {
	return fmt.Sprintf("%s-%s-%d", change, id, attempt)
}
