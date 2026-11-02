// Package forge reads which forge a repository delivers to (spec
// docs/superpowers/specs/2026-11-01-forge-delivery-design.md, "The setting"):
// the machine's delivery.forge, or with auto its origin remote's host. Pure:
// the caller reads the setting and the remote.
package forge

import (
	"net/url"
	"strings"
)

// Kind is a forge, or local (no forge: nothing pushed).
type Kind string

const (
	GitHub    Kind = "github"
	GitLab    Kind = "gitlab"
	Bitbucket Kind = "bitbucket"
	Local     Kind = "local"
)

// Forge is the resolved forge and origin's place on it, never with user info.
type Forge struct {
	Kind              Kind
	Host, Owner, Repo string
	// Note says why auto chose local for a remote it could not place, or "".
	Note string
}

// Resolve is the forge for setting (auto | github | gitlab | bitbucket |
// local) and origin's URL (has: an origin remote exists).
func Resolve(setting, origin string, has bool) Forge {
	var f Forge
	if has {
		f.Host, f.Owner, f.Repo = parse(origin)
	}
	switch Kind(setting) {
	case GitHub, GitLab, Bitbucket, Local:
		f.Kind = Kind(setting)
		return f
	}
	switch {
	case !has:
		f.Kind = Local
	case f.Host == "github.com":
		f.Kind = GitHub
	case f.Host == "gitlab.com" || strings.HasPrefix(f.Host, "gitlab."):
		f.Kind = GitLab
	case f.Host == "bitbucket.org":
		f.Kind = Bitbucket
	default:
		f.Kind = Local
		host := f.Host
		if host == "" {
			host = "an unreadable host"
		}
		f.Note = "origin is on " + host + ": not a known forge; set delivery.forge"
	}
	return f
}

// parse reads host, owner (may hold a group path) and repository from
// git@host:o/r(.git), https://[user@]host/o/r(.git) or
// ssh://[user@]host[:port]/o/r(.git); user info and the port are dropped.
func parse(raw string) (host, owner, repo string) {
	raw = strings.TrimSpace(raw)
	var path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", ""
		}
		host, path = strings.ToLower(u.Hostname()), u.Path
	} else if at := strings.Index(raw, "@"); at >= 0 && strings.Contains(raw[at:], ":") {
		rest := raw[at+1:]
		h, p, _ := strings.Cut(rest, ":")
		host, path = strings.ToLower(h), p
	} else {
		return "", "", ""
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return host, "", ""
	}
	return host, path[:i], path[i+1:]
}
