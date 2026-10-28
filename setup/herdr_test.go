package setup

import "testing"

// herdr's plugin list, read as JSON (spec: Outputs, stated difference):
// our row found with its fields; a listing that is no listing says so.
func TestPluginRows(t *testing.T) {
	js := `{"result":{"plugins":[{"plugin_id":"other.x","enabled":true,"plugin_root":"/o","version":"1"},
	  {"plugin_id":"netbja.agentbus","enabled":false,"plugin_root":"/r","version":"0.23","extra":{"nested":1}}]}}`
	rows, ok := PluginRows([]byte(js))
	if !ok || len(rows) != 2 {
		t.Fatalf("%v %v", rows, ok)
	}
	r, found := Ours(rows)
	if !found || r.Enabled || r.Root != "/r" || r.Version != "0.23" {
		t.Fatalf("%+v %v", r, found)
	}
	if _, ok := PluginRows([]byte(`not json`)); ok {
		t.Fatal("garbage read as a listing")
	}
	if rows, ok := PluginRows([]byte(`{"result":{"plugins":[]}}`)); !ok || len(rows) != 0 {
		t.Fatal("an empty listing")
	}
}

// The exact-block swap: every exact copy replaced; NEW empty drops the
// block with the blank line before it; an edited or absent block is
// refused (setup.sh's exit 3).
func TestKeybindSwap(t *testing.T) {
	file := "[ui]\ntheme = \"x\"\n" + OldKeybindBlock + "\n[other]\nk = 1\n"
	got, ok := KeybindSwap(OldKeybindBlock, KeybindBlock, file)
	if want := "[ui]\ntheme = \"x\"\n" + KeybindBlock + "\n[other]\nk = 1\n"; !ok || got != want {
		t.Fatalf("swap:\n%q\nwant\n%q", got, want)
	}
	got, ok = KeybindSwap(KeybindBlock, "", "[ui]\n"+KeybindBlock+"\n")
	if !ok || got != "[ui]\n" {
		t.Fatalf("drop: %q", got)
	}
	edited := "[ui]" + OldKeybindBlock[:len(OldKeybindBlock)-5] + "EDIT\"\n"
	if _, ok := KeybindSwap(OldKeybindBlock, KeybindBlock, edited); ok {
		t.Fatal("an edited block was swapped")
	}
	two := "a\n" + OldKeybindBlock + "\n" + OldKeybindBlock + "\n"
	if got, ok := KeybindSwap(OldKeybindBlock, KeybindBlock, two); !ok || got != "a\n"+KeybindBlock+"\n"+KeybindBlock+"\n" {
		t.Fatalf("two copies: %q", got)
	}
}

func TestPluginActionState(t *testing.T) {
	for out, want := range map[string]string{
		`{"result":{"actions":[{"action_id":"projects","label":"x","plugin_id":"netbja.agentbus"}]}}`: "ok",
		`{"error":{"code":"server_not_running"}}`:                                                     "noserver",
		`{"result":{"actions":[]}}`:                                                                   "missing",
	} {
		if got := pluginActionState(out); got != want {
			t.Errorf("%s: %s, want %s", out, got, want)
		}
	}
}
