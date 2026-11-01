package config

import (
	"net/url"
	"sort"
	"strings"
)

// The local LLM peer's endpoints (spec docs/superpowers/specs/2026-10-31-local-llm-peer-design.md,
// "The endpoint: config.toml"): one [llm.<name>] table each in config.toml,
// never in busmon.toml. They are not registry keys: a name is the person's,
// and an endpoint is read whole or not at all. No secret ever: a key, token
// or password, user info or a query is refused; a URL that is not loopback is
// refused unless remote = true, so a typo never sends repository text to
// another host.

// Endpoint is one [llm.<name>] table that passed every rule.
type Endpoint struct {
	Name   string
	URL    string
	Model  string
	Remote bool
	Line   int // its [llm.<name>] header
}

// Endpoints is the file's valid endpoints, by name.
func (f *File) Endpoints() []Endpoint {
	if f == nil || len(f.endpoints) == 0 {
		return nil
	}
	out := append([]Endpoint(nil), f.endpoints...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Endpoint is the valid endpoint called name.
func (f *File) Endpoint(name string) (Endpoint, bool) {
	for _, e := range f.Endpoints() {
		if e.Name == name {
			return e, true
		}
	}
	return Endpoint{}, false
}

// loopbackHosts are the hosts an endpoint may name without remote = true
// (the spec's three).
var loopbackHosts = map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true}

// endpointSecrets are the whole names a key in an endpoint is refused for as a
// secret (the registry's list, plus key: the spec names it).
func endpointSecret(name string) bool {
	return name == "key" || secretNames[name]
}

// loadEndpoints reads the llm.* leaves of config.toml into f: each endpoint
// whole, or dropped with a warning per problem at its line; never a value.
func (f *File) loadEndpoints(leaves map[string]any, lines map[string]int, headers map[string]int) {
	type table struct {
		keys map[string]any
	}
	tables := map[string]*table{}
	var names []string
	warn := func(key string, line int, why string) {
		f.Warnings = append(f.Warnings, Warning{File: f.Label, Line: line, Key: key, Msg: why + "; endpoint ignored"})
	}
	for leaf, v := range leaves {
		rest, ok := strings.CutPrefix(leaf, "llm.")
		if !ok {
			continue
		}
		name, key, ok := strings.Cut(rest, ".")
		if !ok {
			warn(leaf, lines[leaf], "an endpoint is a table: [llm.<name>]")
			continue
		}
		t := tables[name]
		if t == nil {
			t = &table{keys: map[string]any{}}
			tables[name] = t
			names = append(names, name)
		}
		t.keys[key] = v
	}
	sort.Strings(names)
	for _, name := range names {
		t, at := tables[name], "llm."+name
		header := headers[at]
		if !validName(name) {
			warn(at, header, "not a valid endpoint name")
			continue
		}
		e := Endpoint{Name: name, Line: header}
		bad := map[string]bool{} // the keys refused, so every problem is said once
		keys := make([]string, 0, len(t.keys))
		for k := range t.keys {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return lines[at+"."+keys[i]] < lines[at+"."+keys[j]] })
		for _, k := range keys {
			v, line := t.keys[k], lines[at+"."+k]
			switch k {
			case "url":
				s, ok := v.(string)
				if !ok {
					warn(at+".url", line, "want a string")
					bad[k] = true
					continue
				}
				e.URL = s
			case "model":
				s, ok := v.(string)
				if !ok || strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\r\n") || s != strings.TrimSpace(s) {
					warn(at+".model", line, "want a one-line name")
					bad[k] = true
					continue
				}
				e.Model = s
			case "remote":
				b, ok := v.(bool)
				if !ok {
					warn(at+".remote", line, "want true or false")
					bad[k] = true
					continue
				}
				e.Remote = b
			default:
				msg := "unknown key"
				if endpointSecret(strings.ToLower(k)) {
					msg += "; a secret does not belong in this file"
				}
				warn(at+"."+k, line, msg)
				bad[k] = true
			}
		}
		if e.URL == "" && !bad["url"] {
			warn(at, header, "no url")
			bad["url"] = true
		}
		if e.Model == "" && !bad["model"] {
			warn(at, header, "no model")
			bad["model"] = true
		}
		if !bad["url"] && !bad["remote"] {
			if why := endpointURL(e.URL, e.Remote); why != "" {
				warn(at+".url", lines[at+".url"], why)
				bad["url"] = true
			}
		}
		if len(bad) == 0 {
			f.endpoints = append(f.endpoints, e)
		}
	}
}

// endpointURL is why raw is refused as an endpoint's URL ("" = taken).
func endpointURL(raw string, remote bool) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" {
		return "want an http or https URL"
	}
	if u.User != nil {
		return "user info in a URL is a secret: it does not belong in this file"
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "want a URL without a query or fragment"
	}
	if !remote && !loopbackHosts[strings.ToLower(u.Hostname())] {
		return "not loopback: set remote = true to send repository text there"
	}
	return ""
}

// tableLines finds the line of each [table] header.
func tableLines(text string) map[string]int {
	out := map[string]int{}
	for i, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "[") && !strings.HasPrefix(s, "[[") {
			if end := strings.Index(s, "]"); end > 0 {
				if name := strings.TrimSpace(s[1:end]); out[name] == 0 {
					out[name] = i + 1
				}
			}
		}
	}
	return out
}
