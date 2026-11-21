package bus

import "testing"

// ConnectOptions is Connect's resolution with an explicit environment, so
// a caller can tell which broker a child process with that environment
// reaches (busmon --delete's backup, reviewer-codex): REDIS_URL first, else
// the host given, else REDIS_HOST, then REDIS_PORT.
func TestConnectOptions(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, c := range []struct {
		name string
		env  map[string]string
		host string
		want string
	}{
		{"defaults", nil, "", "localhost:6380"},
		{"REDIS_HOST", map[string]string{"REDIS_HOST": "box", "REDIS_PORT": "7000"}, "", "box:7000"},
		{"host over REDIS_HOST", map[string]string{"REDIS_HOST": "box"}, "vdr", "vdr:6380"},
		{"REDIS_URL over all", map[string]string{"REDIS_URL": "redis://u:6390/2", "REDIS_HOST": "box"}, "vdr", "u:6390"},
	} {
		o, err := ConnectOptions(env(c.env), c.host)
		if err != nil || o.Addr != c.want {
			t.Errorf("%s: %v %v, want %s", c.name, o, err, c.want)
		}
	}
	if _, err := ConnectOptions(env(map[string]string{"REDIS_URL": "::bad"}), ""); err == nil {
		t.Error("a bad REDIS_URL taken")
	}
}
