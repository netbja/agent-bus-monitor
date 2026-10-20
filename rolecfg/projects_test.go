package rolecfg

import "testing"

func TestProjectsDirOrder(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"AGENTBUS_PROJECTS_DIR": "/a", "XDG_CONFIG_HOME": "/x", "HOME": "/h"}, "/a"},
		{map[string]string{"XDG_CONFIG_HOME": "/x", "HOME": "/h"}, "/x/agentbus/projects"},
		{map[string]string{"HOME": "/h"}, "/h/.config/agentbus/projects"},
	}
	for _, c := range cases {
		if got := ProjectsDir(func(k string) string { return c.env[k] }); got != c.want {
			t.Errorf("ProjectsDir(%v) = %q, want %q", c.env, got, c.want)
		}
	}
}
