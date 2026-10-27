package main

import (
	"slices"
	"strconv"
	"strings"
)

// Inline expansion (busmon Charm spec, "Reading"): Enter on a selected
// Activity line, or a For you unread line, shows its text in place, under the
// row, indented, at the panel's width, up to half the panel's height. One row
// at a time; moving the selection or Esc collapses it, and a second Enter
// opens it in the reader. The line is read when its expansion closes (or the
// reader opens on it): For you keeps the row while it is open.

// expandKey is the key of an expansion's line i: not selectable, never a
// feed id ("\x00" starts no stream id).
func expandKey(i int) string { return "\x00" + strconv.Itoa(i) }

func isExpandKey(k string) bool { return strings.HasPrefix(k, "\x00") }

// expandedLine is the feed line the expansion shows.
func (m *Model) expandedLine() (feedLine, bool) {
	key := m.ui.expanded
	for _, fl := range m.feed.feed {
		if fl.sep {
			continue
		}
		if (m.ui.expandIn == PanelActivity && fl.id == key) || (m.ui.expandIn == PanelForYou && "unread:"+fl.ev.ID == key) {
			return fl, true
		}
	}
	return feedLine{}, false
}

// expand opens the row key of list p, closing any other.
func (m *Model) expand(p PanelID, key string) {
	m.collapse()
	m.ui.expanded, m.ui.expandIn = key, p
	m.lists.of(p).anchor = "" // follow the selection, so the text below it shows
}

// collapse closes the expansion; its line is read.
func (m *Model) collapse() {
	if m.ui.expanded == "" {
		return
	}
	if fl, ok := m.expandedLine(); ok {
		m.markRead(fl.ev.ID)
	}
	m.ui.expanded = ""
}

// markRead records that the person opened an entry. The local cache keeps
// only the ids still in the retained window (the file cannot grow); its save
// runs in a command after the update (afterUpdate).
func (m *Model) markRead(id string) {
	if !m.feed.unread.forPerson[id] || m.feed.unread.read[id] {
		return
	}
	m.feed.unread.read[id] = true
	retained := map[string]bool{}
	for _, fl := range m.feed.feed {
		if !fl.sep {
			retained[fl.ev.ID] = true
		}
	}
	c := m.cache_()
	var keep []string
	for _, r := range append(slices.Clone(c.Read), id) {
		if retained[r] && !slices.Contains(keep, r) {
			keep = append(keep, r)
		}
	}
	slices.Sort(keep)
	c.Read = keep
	m.sig.readDirty = true
}

// keepExpansion collapses the expansion once its row is not selected.
func (m *Model) keepExpansion() {
	if m.ui.expanded != "" && m.lists.of(m.ui.expandIn).Selected() != m.ui.expanded {
		m.collapse()
	}
}

// withExpansion inserts the expanded row's text after it; width is the
// rows' width.
func (m *Model) withExpansion(p PanelID, items []ListItem, width int) []ListItem {
	if m.ui.expanded == "" || m.ui.expandIn != p {
		return items
	}
	i := slices.IndexFunc(items, func(it ListItem) bool { return it.Key == m.ui.expanded })
	fl, ok := m.expandedLine()
	if i < 0 || !ok {
		return items
	}
	limit := max(1, m.lists.of(p).rect.H/2)
	md := m.cache.markdown(fl.ev.ID, messageBody(fl.ev), max(1, width-2))
	more := len(md) > limit
	if more {
		md = md[:limit-1]
	}
	ext := make([]ListItem, 0, len(md)+1)
	for j, l := range md {
		ext = append(ext, ListItem{Key: expandKey(j), Line: append(Line{{Text: "  "}}, l...)})
	}
	if more {
		ext = append(ext, ListItem{Key: expandKey(len(md)), Line: Line{{Text: "  ... enter for more", Role: RoleMuted}}})
	}
	return slices.Concat(items[:i+1], ext, items[i+1:])
}

// keepInView sets each list's kept rows: the expanded row's text, which
// its viewport keeps in view with the row, in auto-scroll too.
func (m *Model) keepInView() {
	for _, p := range narrowPanels {
		lst := m.lists.of(p)
		lst.keep = 0
		if m.ui.expanded == "" || m.ui.expandIn != p {
			continue
		}
		if i := lst.index(m.ui.expanded); i >= 0 {
			for j := i + 1; j < len(lst.items) && isExpandKey(lst.items[j].Key); j++ {
				lst.keep++
			}
		}
	}
}
