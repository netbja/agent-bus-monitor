package main

// The shell's view cache (busmon Charm spec, "Caching"): rendered markdown
// by (message id, width, palette), so a redraw that changes no data (a fade
// frame, a key that only moves a selection) renders no markdown. Reader pages
// carry their own build stamp (tea_reader.go). Filled on the update path
// only: View reads the rows refresh built.

type mdKey struct {
	id    string
	width int
	pal   Palette
}

type viewCache struct {
	md     map[mdKey][]Line
	builds int // reader page bodies built
}

func newViewCache() *viewCache { return &viewCache{md: map[mdKey][]Line{}} }

// markdown is src rendered at width for the message id, from the cache when
// it is there. A message's text never changes under its id.
func (c *viewCache) markdown(id, src string, width int) []Line {
	k := mdKey{id, width, th.Palette()}
	if ls, ok := c.md[k]; ok {
		return ls
	}
	ls := renderMarkdown(src, width)
	c.md[k] = ls
	return ls
}
