// Package personmem is the person's long-term memory, layer 1 (spec
// docs/superpowers/specs/2026-11-21-person-memory-design.md r2): what the
// person said or decided, one Markdown file per topic in a host-level git
// repository under ${XDG_DATA_HOME:-~/.local/share}/agentbus/memory/person.
// Every observation quotes the person's own typed words (typed=yes); the
// topic, type and importance are master's filing, which the person sees and
// can change. Ideas from MemoryGraph and memory-mcp-server-go (spec §3);
// no code from either.
package personmem

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The names (spec §3, the person's answer 6): six types, three levels of
// importance, four relations, five kinds of topic.
var (
	Types       = []string{"decision", "preference", "constraint", "problem", "solution", "fact"}
	Importances = []string{"critical", "standard", "minor"}
	RelTypes    = []string{"supersedes", "solves", "applies-to", "contradicts"}
	Kinds       = []string{"person", "project", "tool", "practice", "other"}
)

// topicRE is a topic's name, and its file's name without .md.
var topicRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

// ValidTopic: a topic name as spec §4.1 allows it.
func ValidTopic(s string) bool { return topicRE.MatchString(s) }

// Relation links a topic to another (spec §3: from memory-mcp-server-go's
// relations, in active voice).
type Relation struct {
	Type  string
	Topic string
}

// Observation is one statement of the person's (spec §4.1).
type Observation struct {
	ID         string // <YYYYMMDD>-<entry id>, or <YYYYMMDD>-b<ms> from busmon
	Type       string
	Importance string
	Said       string // "<date> by <who>, project <p>, entry <id>" or "<date> by <who> from busmon"
	Quote      string // the person's words, the fact
	Answers    string // "<project>:<entry> by <who>": the proposal it replies to
	Question   string // that proposal's words, context only
	Supersedes string // an earlier observation's id
}

// Topic is one file: the entity (its kind and relations) and its
// observations.
type Topic struct {
	Topic     string
	Kind      string
	Relations []Relation
	Obs       []Observation
}

// Format is the topic's file, canonical: Parse(Format) gives it back.
func (t *Topic) Format() string {
	var b strings.Builder
	b.WriteString("---\ntopic: " + t.Topic + "\n")
	b.WriteString("kind: " + or(t.Kind, "other") + "\n")
	if len(t.Relations) > 0 {
		b.WriteString("relations:\n")
		for _, r := range t.Relations {
			b.WriteString("  - " + r.Type + ": " + r.Topic + "\n")
		}
	}
	b.WriteString("---\n# " + t.Topic + "\n")
	for _, o := range t.Obs {
		b.WriteString("\n- id: " + o.ID + "\n")
		b.WriteString("  type: " + o.Type + "\n")
		b.WriteString("  importance: " + o.Importance + "\n")
		b.WriteString("  said: " + o.Said + "\n")
		b.WriteString("  quote: " + strconv.Quote(o.Quote) + "\n")
		if o.Answers != "" {
			b.WriteString("  answers: " + o.Answers + "\n")
		}
		if o.Question != "" {
			b.WriteString("  question: " + strconv.Quote(o.Question) + "\n")
		}
		if o.Supersedes != "" {
			b.WriteString("  supersedes: " + o.Supersedes + "\n")
		}
	}
	return b.String()
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Parse reads a topic file. It is lenient about blank lines and a comment
// after an unquoted value (a hand edit), strict about the shape: the front
// matter first, then the observations, each "- id:" followed by its fields.
func Parse(text string) (*Topic, error) {
	t := &Topic{}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	n, state := 0, 0 // 0 before the front matter, 1 in it, 2 the body
	var cur *Observation
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), " \t\r")
		switch {
		case state == 0:
			if line == "" {
				continue
			}
			if line != "---" {
				return nil, fmt.Errorf("line %d: the file starts with its front matter (---)", n)
			}
			state = 1
		case state == 1:
			switch {
			case line == "---":
				state = 2
			case strings.HasPrefix(line, "topic:"):
				t.Topic = value(line, "topic:")
			case strings.HasPrefix(line, "kind:"):
				t.Kind = value(line, "kind:")
			case line == "relations:" || line == "":
			case strings.HasPrefix(line, "  - "):
				typ, topic, ok := strings.Cut(strings.TrimPrefix(line, "  - "), ":")
				if !ok {
					return nil, fmt.Errorf("line %d: a relation is \"  - <type>: <topic>\"", n)
				}
				t.Relations = append(t.Relations, Relation{Type: strings.TrimSpace(typ), Topic: unComment(strings.TrimSpace(topic))})
			default:
				return nil, fmt.Errorf("line %d: unknown front matter %q", n, line)
			}
		default:
			switch {
			case line == "" || strings.HasPrefix(line, "# "):
			case strings.HasPrefix(line, "- id:"):
				t.Obs = append(t.Obs, Observation{ID: value(line, "- id:")})
				cur = &t.Obs[len(t.Obs)-1]
			case strings.HasPrefix(line, "  ") && cur != nil:
				k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
				if !ok {
					return nil, fmt.Errorf("line %d: a field is \"  <name>: <value>\"", n)
				}
				v = strings.TrimSpace(v)
				switch k {
				case "type":
					cur.Type = unComment(v)
				case "importance":
					cur.Importance = unComment(v)
				case "said":
					cur.Said = v
				case "quote":
					cur.Quote = unquote(v)
				case "answers":
					cur.Answers = v
				case "question":
					cur.Question = unquote(v)
				case "supersedes":
					cur.Supersedes = unComment(v)
				default:
					return nil, fmt.Errorf("line %d: unknown field %q", n, k)
				}
			default:
				return nil, fmt.Errorf("line %d: an observation starts with \"- id:\"", n)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if state < 2 {
		return nil, fmt.Errorf("the front matter is not closed (---)")
	}
	return t, nil
}

func value(line, prefix string) string {
	return unComment(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
}

// unComment drops a "  # ..." comment after an unquoted value.
func unComment(v string) string {
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// unquote reads a quoted value; a hand-written unquoted one stays as it is.
func unquote(v string) string {
	if s, err := strconv.Unquote(v); err == nil {
		return s
	}
	return v
}
