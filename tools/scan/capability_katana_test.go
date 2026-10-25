package scan

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCrawlerProfilesAreExplicitlyComposed(t *testing.T) {
	for _, mode := range []string{"quick", "full", " FULL ", "", " QUICK "} {
		p, err := profileForMode(mode)
		if err != nil {
			t.Fatal(err)
		}
		cmd := New(nil)
		cmd.extendKatanaProfile(mode, &p)
		if p.Enabled(capKatanaCrawl) || p.Enabled(capKatanaDeep) {
			t.Fatal("crawler enabled without an adapter")
		}
		cmd.crawler = func(context.Context, string, string, int, bool, string, func(string)) error { return nil }
		cmd.extendKatanaProfile(mode, &p)
		if !p.Enabled(capKatanaCrawl) || p.Enabled(capKatanaDeep) != (strings.TrimSpace(strings.ToLower(mode)) == "full") {
			t.Fatalf("profile=%v", p)
		}
	}
}
func TestCrawlerFiltersAndDeduplicatesTargets(t *testing.T) {
	cmd := New(nil, WithProxy("http://proxy"), WithCrawler(func(_ context.Context, url, host string, depth int, js bool, proxy string, emit func(string)) error {
		if url != "https://a.example.com" || host != "vhost" || depth != 3 || !js || proxy != "http://proxy" {
			t.Fatal("lost crawler options")
		}
		for _, url := range []string{"https://a.example.com/", "https://b.example.com/path", "https://b.example.com/path", "https://other.test/", ""} {
			emit(url)
		}
		return errors.New("partial crawl failed")
	}))
	var emitted []event
	runKatanaCrawl(t.Context(), cmd, targetEvent(capSprayCheck, newWebTarget("https://a.example.com", "vhost")), 3, true, func(e event) { emitted = append(emitted, e) })
	targets, failures := 0, 0
	for _, e := range emitted {
		if e.Kind == eventTarget {
			targets++
			if e.Target.(webTarget).HostHeader != "vhost" {
				t.Fatal("host header lost")
			}
		}
		if e.Kind == eventError && strings.Contains(e.Error, "partial crawl failed") {
			failures++
		}
	}
	if targets != 1 || failures != 1 {
		t.Fatalf("targets=%d failures=%d", targets, failures)
	}
}
