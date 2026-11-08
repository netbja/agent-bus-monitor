package main

import "testing"

// model-detection §3.5, §3.6: the model-catalogue info line's news is a
// notice once (only the text in this step; the wiring follows): taken from
// an ok detail or after a warn's findings, never repeated, nothing without
// news.
func TestCatalogueNewsNotice(t *testing.T) {
	news := "new: xai grok-4.7 (2h) · anthropic opus now claude-opus-5-5"
	n, ok := catalogueNewsNotice("", news)
	if !ok || n.level != noticeInfo || n.text != "models: xai grok-4.7 (2h) · anthropic opus now claude-opus-5-5 (agentbus models --changes)" {
		t.Fatalf("%+v %v", n, ok)
	}
	if _, ok := catalogueNewsNotice(news, news); ok {
		t.Fatal("the same news twice")
	}
	if _, ok := catalogueNewsNotice(news, "profile grok: grok-4.6 newer? grok-4.7; "+news); ok {
		t.Fatal("the same news under a warn")
	}
	if n, ok := catalogueNewsNotice("", "profile grok: grok-4.6 newer? grok-4.7; new: xai grok-4.8 (1h)"); !ok || n.text != "models: xai grok-4.8 (1h) (agentbus models --changes)" {
		t.Fatalf("news after findings: %+v %v", n, ok)
	}
	for _, d := range []string{"every configured model is listed; no news in 24h", "profile old: gpt-5-old gone", ""} {
		if _, ok := catalogueNewsNotice("", d); ok {
			t.Errorf("%q: a notice without news", d)
		}
	}
}
