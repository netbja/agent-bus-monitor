package forge

import "testing"

// The forge from origin (forge-delivery, "The setting"): each URL form, a
// self-hosted GitLab by its first label, user info dropped, an unknown host
// local with its note, no remote local; a set value wins over auto.
func TestResolve(t *testing.T) {
	for _, c := range []struct {
		setting, url string
		has          bool
		want         Kind
		host, o, r   string
		noteHas      string
	}{
		{"auto", "git@github.com:netbja/agent-bus-monitor.git", true, GitHub, "github.com", "netbja", "agent-bus-monitor", ""},
		{"auto", "https://github.com/o/r", true, GitHub, "github.com", "o", "r", ""},
		{"auto", "https://user:tok-SECRET@github.com/o/r.git", true, GitHub, "github.com", "o", "r", ""},
		{"auto", "ssh://git@gitlab.com:2222/grp/sub/r.git", true, GitLab, "gitlab.com", "grp/sub", "r", ""},
		{"auto", "git@gitlab.example.org:team/r.git", true, GitLab, "gitlab.example.org", "team", "r", ""},
		{"auto", "https://bitbucket.org/ws/r.git", true, Bitbucket, "bitbucket.org", "ws", "r", ""},
		{"auto", "git@git.example.org:team/r.git", true, Local, "git.example.org", "team", "r", "origin is on git.example.org: not a known forge; set delivery.forge"},
		{"auto", "", false, Local, "", "", "", ""},
		{"gitlab", "git@git.example.org:team/r.git", true, GitLab, "git.example.org", "team", "r", ""},
		{"local", "git@github.com:o/r.git", true, Local, "github.com", "o", "r", ""},
		{"github", "", false, GitHub, "", "", "", ""},
	} {
		got := Resolve(c.setting, c.url, c.has)
		if got.Kind != c.want || got.Host != c.host || got.Owner != c.o || got.Repo != c.r || (c.noteHas != "" && got.Note != c.noteHas) || (c.noteHas == "" && got.Note != "") {
			t.Errorf("Resolve(%q, %q) = %+v", c.setting, c.url, got)
		}
		if got.Note != "" && (contains(got.Note, "SECRET") || contains(got.Note, "user")) {
			t.Errorf("a note names user info: %q", got.Note)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
