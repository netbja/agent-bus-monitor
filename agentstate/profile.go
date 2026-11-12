// Package agentstate judges an agent's state from what its terminal shows,
// for every host and every client (spec 2026-11-10-herdr-exit-design.md,
// 4.3 and 4.9). Detection is per client and kept as data: one profile per
// client in clients/<client>.toml, embedded, with its screen fixtures in
// testdata/screens. Herdr exit step 2a moves today's judgement here from
// dispatch, unchanged.
package agentstate

import (
	"embed"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed clients/*.toml
var clientFiles embed.FS

// Profile is one client's data.
type Profile struct {
	Client    string
	Signature Signature
}

type profileFile struct {
	Client    string `toml:"client"`
	Signature struct {
		ANSI      bool     `toml:"ansi"`
		Prompt    string   `toml:"prompt"`
		RuleAbove bool     `toml:"rule_above"`
		Ready     []string `toml:"ready"`
		Busy      []string `toml:"busy"`
		Modal     []string `toml:"modal"`
		Dialog    []string `toml:"dialog"`
	} `toml:"signature"`
}

// parseProfile reads one profile: every key known, the client named as its
// file, every pattern compiling, a Prompt marker present.
func parseProfile(name string, data []byte) (Profile, error) {
	var f profileFile
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return Profile{}, fmt.Errorf("%s: %w", name, err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return Profile{}, fmt.Errorf("%s: unknown key %s", name, u[0])
	}
	if f.Client+".toml" != name {
		return Profile{}, fmt.Errorf("%s: client %q is not the file's name", name, f.Client)
	}
	if f.Signature.Prompt == "" {
		return Profile{}, fmt.Errorf("%s: no prompt marker", name)
	}
	compile := func(ps []string) ([]*regexp.Regexp, error) {
		var out []*regexp.Regexp
		for _, p := range ps {
			r, err := regexp.Compile(p)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			out = append(out, r)
		}
		return out, nil
	}
	s := Signature{ANSI: f.Signature.ANSI, RuleAbove: f.Signature.RuleAbove}
	if s.Prompt, err = regexp.Compile(f.Signature.Prompt); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", name, err)
	}
	for _, x := range []struct {
		dst *[]*regexp.Regexp
		src []string
	}{{&s.Ready, f.Signature.Ready}, {&s.Busy, f.Signature.Busy}, {&s.Modal, f.Signature.Modal}, {&s.Dialog, f.Signature.Dialog}} {
		if *x.dst, err = compile(x.src); err != nil {
			return Profile{}, err
		}
	}
	return Profile{Client: f.Client, Signature: s}, nil
}

// loadProfiles reads every embedded profile.
func loadProfiles() (map[string]Profile, error) {
	entries, err := clientFiles.ReadDir("clients")
	if err != nil {
		return nil, err
	}
	out := map[string]Profile{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		data, err := clientFiles.ReadFile(path.Join("clients", e.Name()))
		if err != nil {
			return nil, err
		}
		p, err := parseProfile(e.Name(), data)
		if err != nil {
			return nil, err
		}
		out[p.Client] = p
	}
	return out, nil
}

// profiles are the embedded profiles; a broken one is a build error caught by
// the package's tests, so it panics at start.
var profiles = func() map[string]Profile {
	p, err := loadProfiles()
	if err != nil {
		panic("agentstate: " + err.Error())
	}
	return p
}()

// Profiles is every client with a profile, by client name.
func Profiles() map[string]Profile {
	out := make(map[string]Profile, len(profiles))
	for k, v := range profiles {
		out[k] = v
	}
	return out
}

// Signatures is each profiled client's screen signature. Only clients probed
// live have a profile; a client without one is unsupported and never
// prompted. Fixtures: testdata/screens.
func Signatures() map[string]Signature {
	out := make(map[string]Signature, len(profiles))
	for k, v := range profiles {
		out[k] = v.Signature
	}
	return out
}

// Clients is the profiled clients' names, sorted.
func Clients() []string {
	out := make([]string, 0, len(profiles))
	for k := range profiles {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
