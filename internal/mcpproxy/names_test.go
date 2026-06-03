package mcpproxy

import "testing"

func TestNamespacedToolRoundtrip(t *testing.T) {
	name := NamespacedTool("github", "search_repos")
	slug, tool, ok := ParseNamespacedTool(name)
	if !ok || slug != "github" || tool != "search_repos" {
		t.Fatalf("parse failed: %q %q %v", slug, tool, ok)
	}
}

func TestNamespacedResourceURI(t *testing.T) {
	uri := NamespacedResourceURI("docs", "file:///tmp/a.txt")
	slug, upstream, ok := ParseNamespacedResourceURI(uri)
	if !ok || slug != "docs" || upstream != "file:///tmp/a.txt" {
		t.Fatalf("got %q %q %v", slug, upstream, ok)
	}
}

func TestFilterAllowlist(t *testing.T) {
	items := []string{"a", "b", "c"}
	got := FilterAllowlist(items, []string{"b"})
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("got %v", got)
	}
}
