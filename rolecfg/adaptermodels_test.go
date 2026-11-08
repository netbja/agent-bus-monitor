package rolecfg

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// model-detection §3.9: an adapter may name a fixed argv that lists its
// models; the executable resolves against the manifest, as executable does;
// the provider is its profiles' one.
func TestAdapterModels(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) (*Manifest, error) {
		path := filepath.Join(dir, "roles.toml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}
	head := "schema_version = 2\n[defaults]\nprofile = \"main\"\n[profiles.main]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"manual\" }\n[roles.coder]\ntier = \"boot\"\n"
	m, err := write(head + `[adapters.deep]
executable = "./adapters/deep"
models = ["./adapters/deep-models", "--list"]
[adapters.plain]
executable = "/usr/bin/plain"
[profiles.ds]
client = "deep"
provider = "deepseek"
model = "deepseek-v4"
[profiles.pl]
client = "plain"
provider = "other"
model = "x"
`)
	if err != nil {
		t.Fatal(err)
	}
	got, errs := m.AdapterModels()
	want := []AdapterModels{{Client: "deep", Provider: "deepseek", Argv: []string{filepath.Join(dir, "adapters", "deep-models"), "--list"}}}
	if len(errs) != 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v %v, want %+v", got, errs, want)
	}
	for name, body := range map[string]string{
		"not an array": `[adapters.deep]
executable = "./d"
models = "./d --list"
[profiles.ds]
client = "deep"
provider = "deepseek"
model = "m"
`,
		"empty": `[adapters.deep]
executable = "./d"
models = []
[profiles.ds]
client = "deep"
provider = "deepseek"
model = "m"
`,
	} {
		if _, err := write(head + body); err == nil || !strings.Contains(err.Error(), "models") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Two providers on one adapter: it cannot say whose models it lists.
	m, err = write(head + `[adapters.deep]
executable = "./d"
models = ["./d", "--list"]
[profiles.a]
client = "deep"
provider = "p1"
model = "m"
[profiles.b]
client = "deep"
provider = "p2"
model = "m"
`)
	if err != nil {
		t.Fatal(err)
	}
	if got, errs := m.AdapterModels(); len(got) != 0 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "p1") {
		t.Fatalf("two providers: %+v %v", got, errs)
	}
}
