package main

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// The pause banner (busmon-suspended-banner, the person 2026-11-22): a
// suspended or paused project says so on a full-width row under the header,
// on every page (the Projects page included), /resume named; For you's first
// line says the same, Enter on it the /resume question. It takes the
// stopped-project banner's row: a suspended project reached by a switch is
// suspended first, its way back still named.

// forYouResumeKey is For you's pause row: never an item, never counted;
// forYouStartKey the same row on a paused project reached stopped by a
// switch, whose way back is S (resume lifts a pause, it starts nothing).
const (
	forYouResumeKey = "project:resume"
	forYouStartKey  = "project:start"
)

// bannerShown: a row under the header, the pause's or the stopped project's.
func (m Model) bannerShown() bool { return m.snap.pause != nil || m.stoppedBanner() }

// pauseBannerLine is the band: the word and /resume first (a narrow screen
// keeps them), then since when, by whom and why, filled to width on the
// attention colour. A paused project reached stopped by a switch names S
// instead: resume lifts a pause, it starts no service.
func (m Model) pauseBannerLine(width int) Line {
	p := m.snap.pause
	word, what := " PAUSED", "/resume ends it now"
	switch {
	case p.Suspended():
		word, what = " SUSPENDED", "/resume brings it back"
	case m.stoppedBanner(): // read-only, and resume is no way back for a pause: S is
		what = "stopped project, read-only " + GlyphNone + " S start services"
	}
	sep := " " + GlyphNone + " "
	// the header's words without their own first word: "since 21:40 · by
	// dana · reboot", "resumes in 1h52 (lunch)"
	rest := strings.TrimPrefix(pauseSeg(p, m.now)[0].Text, "suspended ")
	rest = strings.TrimPrefix(rest, "paused "+GlyphNone+" ")
	text := what + sep + rest
	if !p.Suspended() && p.By != "" {
		text += sep + "by " + p.By
	}
	if m.cfg.from != "" {
		text += sep + "B back to " + m.cfg.from
	}
	l := Line{{Text: word, Role: RoleAttention, Bold: true, Pill: true}, {Text: sep + Preview(text), Role: RoleAttention, Pill: true}}
	return FitLine(l, width) // its padding keeps the band's colour
}

// forYouPauseRow is For you's first line while the project is paused: what
// the band says, its way back named (/resume; S on a paused project reached
// stopped, as the band says).
func (m Model) forYouPauseRow() (ListItem, bool) {
	p := m.snap.pause
	if p == nil {
		return ListItem{}, false
	}
	word, key, way := "Project paused", forYouResumeKey, "/resume"
	switch {
	case p.Suspended():
		word = "Project suspended"
	case m.stoppedBanner():
		key, way = forYouStartKey, "S start services"
	}
	return ListItem{Key: key, Selectable: true,
		Line: Line{{Text: word, Role: RoleAttention, Bold: true}, {Text: " " + GlyphNone + " ", Role: RoleMuted}, {Text: way, Role: RoleAccent}}}, true
}

// resumeQuestion is /resume's confirmation, as Z words it: what comes back.
func (m Model) resumeQuestion() (title, body string) {
	body = "The pause lifted: the agents are woken again."
	if m.snap.pause != nil && m.snap.pause.Suspended() {
		body = "Services, missing agents, then the pause lifted."
	}
	return "Resume " + m.cfg.project + "?", body
}

// resumeRow is Enter on For you's pause row: the /resume command.
func (m *Model) resumeRow() tea.Cmd {
	for _, c := range classCRows() {
		if c.Name == "resume" {
			return m.palAgentbus(&c, nil)
		}
	}
	return nil
}

// isResume is the open suspended project's resume run (Z's or /resume's):
// allowed while read-only, as S is, since a suspended project reached by a
// switch is stopped and resume is its way back (a pause's is S).
func (m Model) isResume(args []string) bool {
	return m.snap.pause != nil && m.snap.pause.Suspended() && slices.Equal(args, []string{"--project", m.cfg.project, "resume", "--yes"})
}
