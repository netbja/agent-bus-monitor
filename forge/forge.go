// Package forge reads which forge a repository delivers to (spec
// docs/superpowers/specs/2026-11-01-forge-delivery-design.md, "The setting"):
// the machine's delivery.forge, or with auto its origin remote's host. Pure:
// the caller reads the setting and the remote.
package forge

import (
	"net/url"
	"strings"
)

// Kind is a forge, none (a remote no adapter knows: pushed, no request
// page), or local (no remote: nothing pushed).
type Kind string

const (
	GitHub    Kind = "github"
	GitLab    Kind = "gitlab"
	Bitbucket Kind = "bitbucket"
	None      Kind = "none"
	Local     Kind = "local"
)

// Forge is the resolved forge and origin's place on it, never with user info.
type Forge struct {
	Kind              Kind
	Host, Owner, Repo string
	// Note says why auto chose none or local for a remote it could not place, or "".
	Note string
}

// Resolve is the forge for setting (auto | github | gitlab | bitbucket |
// none | local) and origin's URL (has: an origin remote exists).
func Resolve(setting, origin string, has bool) Forge {
	var f Forge
	local := false
	if has {
		f.Host, f.Owner, f.Repo, local = parse(origin)
	}
	switch Kind(setting) {
	case GitHub, GitLab, Bitbucket, None, Local:
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
	case local:
		f.Kind, f.Note = Local, "origin is a local path: local mode (nothing pushed)"
	default:
		// A remote on a host no adapter knows is pushed to, with no request
		// page: none, no longer local (master-idle §3.1, the person's answer
		// 4); the git test alone then judges its delivery.
		f.Kind = None
		host := f.Host
		if host == "" {
			host = "an unreadable host"
		}
		f.Note = "origin is on " + host + ": no forge adapter, so none mode (the branch is pushed, no request page); " +
			"set delivery.forge if it is a GitLab or Bitbucket under another name, which applies to every project on this machine"
	}
	return f
}

// parse reads host, owner (may hold a group path) and repository from
// [user@]host:o/r(.git) (git's scp-like form: no "://", and no "/" before the
// first colon), https://[user@]host/o/r(.git) or ssh://[user@]host[:port]/o/r(.git);
// user info and the port are dropped. local: a path or a file:// URL.
func parse(raw string) (host, owner, repo string, local bool) {
	raw = strings.TrimSpace(raw)
	var path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", "", false
		}
		if u.Scheme == "file" {
			return "", "", "", true
		}
		host, path = strings.ToLower(u.Hostname()), u.Path
	} else if i := strings.Index(raw, ":"); i > 0 && !strings.Contains(raw[:i], "/") {
		h := raw[:i]
		if at := strings.LastIndex(h, "@"); at >= 0 {
			h = h[at+1:]
		}
		host, path = strings.ToLower(h), raw[i+1:]
	} else {
		return "", "", "", true // no host part: a path on this machine
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return host, "", "", false
	}
	return host, path[:i], path[i+1:], false
}
