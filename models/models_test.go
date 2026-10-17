package models

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func fakeEnv(files map[string]string, cmds map[string]string) Env {
	return Env{
		Getenv: func(string) string { return "" },
		Home:   "/h",
		Read: func(p string) ([]byte, error) {
			if v, ok := files[p]; ok {
				return []byte(v), nil
			}
			return nil, errors.New("no such file")
		},
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if v, ok := cmds[name]; ok {
				return []byte(v), nil
			}
			return nil, errors.New("not installed")
		},
	}
}

const codexSample = `{"models":[
 {"slug":"gpt-6-sol","display_name":"GPT-6-Sol","description":"Previous generation workhorse model.","visibility":"list","priority":3,"upgrade":{"model":"gpt-6.1-sol"}},
 {"slug":"gpt-6.1-sol","display_name":"GPT-6.1-Sol","description":"Latest workhorse model for coding and everyday work.","visibility":"list","priority":1,"upgrade":null},
 {"slug":"gpt-6-astra","display_name":"GPT-6-Astra","description":"Frontier intelligence for the most demanding work.","visibility":"list","priority":2},
 {"slug":"gpt-6-luna","display_name":"GPT-6-Luna","description":"Fast and affordable model for easier tasks.","visibility":"list","priority":4},
 {"slug":"codex-auto-review","description":"internal","visibility":"hide","priority":43}]}`

const grokSample = "You are logged in with grok.com.\n\nDefault model: grok-4.7\n\nAvailable models:\n  * grok-4.7 (default)\n  - grok-4.7-build-fast\n  - grok-4.6\n"

const kimiSample = `default_model = "kimi-code/k3"
[providers."managed:kimi-code".oauth]
storage = "file"
[models."kimi-code/k3"]
model = "k3"
display_name = "K3"
[models."kimi-code/kimi-for-coding"]
model = "kimi-for-coding"
display_name = "K2.8 Preview"
`

const agySample = "Fetching available models...\ngemini-3.8-flash-high\tGemini 3.8 Flash (High)\ngemini-3.8-flash-low\tGemini 3.8 Flash (Low)\nclaude-opus-4-6-thinking\tClaude Opus 4.6 (Thinking)\n"

func TestReadersAndTiers(t *testing.T) {
	e := fakeEnv(map[string]string{
		filepath.Join("/h", ".codex", "models_cache.json"): codexSample,
		filepath.Join("/h", ".kimi-code", "config.toml"):   kimiSample,
		filepath.Join("/h", ".claude.json"):                `{"additionalModelOptionsCache":[{"value":"claude-fable-5-1[1m]","label":"Fable","description":"Fable 5.1 · Most capable"}],"oauthAccount":{"emailAddress":"x"}}`,
	}, map[string]string{"grok": grokSample, "agy": agySample})
	tbl := &Table{Providers: map[string]*Provider{}}
	now := time.Now()
	tbl.Refresh(context.Background(), Readers(e), DefaultTiers(), now)
	for _, p := range []string{"openai", "anthropic", "xai", "moonshot", "google"} {
		if pr := tbl.Providers[p]; pr == nil || !pr.OK || len(pr.Rows) == 0 {
			t.Fatalf("%s: %+v", p, pr)
		}
	}
	oa := tbl.Providers["openai"]
	if oa.Rows[0].ID != "gpt-6.1-sol" || len(oa.Rows) != 4 {
		t.Fatalf("codex order/visibility: %+v", oa.Rows)
	}
	tiers := map[string]string{}
	for _, r := range oa.Rows {
		tiers[r.ID] = r.Tier
	}
	if tiers["gpt-6.1-sol"] != Workhorse || tiers["gpt-6-astra"] != Frontier || tiers["gpt-6-luna"] != Fast || tiers["gpt-6-sol"] != Legacy {
		t.Fatalf("tiers %v", tiers)
	}
	if r, _ := oa.Find("gpt-6-sol"); r.Newer != "gpt-6.1-sol" {
		t.Fatalf("upgrade evidence lost: %+v", r)
	}
	if r, ok := tbl.Resolve("openai", Workhorse, now); !ok || r.ID != "gpt-6.1-sol" {
		t.Fatalf("resolve workhorse = %+v %v", r, ok)
	}
	if r, ok := tbl.Resolve("xai", Frontier, now); !ok || r.ID != "grok-4.7" {
		t.Fatalf("resolve grok = %+v %v", r, ok)
	}
	if r, ok := tbl.Resolve("anthropic", Frontier, now); !ok || r.ID != "opus" {
		t.Fatalf("resolve claude frontier = %+v %v (the default alias)", r, ok)
	}
	g := tbl.Providers["google"]
	if len(g.Rows) != 3 || g.Rows[2].Family != "anthropic" {
		t.Fatalf("agy rows %+v", g.Rows)
	}
	if r, ok := tbl.Resolve("google", Fast, now); !ok || r.ID != "gemini-3.8-flash-low" {
		t.Fatalf("resolve agy fast = %+v %v (another family never resolves)", r, ok)
	}
	if k := tbl.Providers["moonshot"]; !k.Rows[0].Default || k.Rows[0].ID != "kimi-code/k3" {
		t.Fatalf("kimi %+v", k.Rows)
	}
	if _, ok := tbl.Providers["anthropic"].Find("claude-fable-5-1-20261001"); !ok {
		t.Fatal("a dated id does not find its base")
	}
}

// §12.2: a failed read keeps the last good rows and records the error; an
// old read is not fresh.
func TestFailedReadKeepsRows(t *testing.T) {
	tbl := &Table{Providers: map[string]*Provider{}}
	now := time.Now()
	ok := []Reader{{Provider: "xai", Read: func(context.Context) ([]Row, string, error) {
		return []Row{{Provider: "xai", ID: "grok-4.7", Default: true}}, "grok models", nil
	}}}
	bad := []Reader{{Provider: "xai", Read: func(context.Context) ([]Row, string, error) { return nil, "", errors.New("timeout") }}}
	tbl.Refresh(context.Background(), ok, nil, now)
	tbl.Refresh(context.Background(), bad, nil, now.Add(time.Hour))
	p := tbl.Providers["xai"]
	if p.OK || p.Error != "timeout" || len(p.Rows) != 1 {
		t.Fatalf("after a failure: %+v", p)
	}
	if _, fresh := tbl.Fresh("xai", now.Add(time.Hour)); !fresh {
		t.Fatal("the last good read should still count")
	}
	if _, fresh := tbl.Fresh("xai", now.Add(8*24*time.Hour)); fresh {
		t.Fatal("an 8-day-old read counted as fresh")
	}
}

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.json")
	tbl := &Table{Providers: map[string]*Provider{"xai": {OK: true, ReadAt: 1, Rows: []Row{{ID: "grok-4.7"}}}}}
	if err := tbl.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.Providers["xai"].Rows[0].ID != "grok-4.7" {
		t.Fatalf("%+v %v", got, err)
	}
	if empty, err := Load(filepath.Join(t.TempDir(), "none.json")); err != nil || len(empty.Providers) != 0 {
		t.Fatalf("missing file: %+v %v", empty, err)
	}
}
