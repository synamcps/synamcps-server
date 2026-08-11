package mcp

import "testing"

func TestNegotiateProtocolVersion(t *testing.T) {
	if got := negotiateProtocolVersion("2025-03-26"); got != "2025-03-26" {
		t.Fatalf("got %q", got)
	}
	// Claude Connectors send 2025-11-25 and require an echo, not a downgrade.
	if got := negotiateProtocolVersion("2025-11-25"); got != "2025-11-25" {
		t.Fatalf("should echo client version, got %q", got)
	}
	if got := negotiateProtocolVersion(""); got != "2025-06-18" {
		t.Fatalf("empty got %q", got)
	}
	if got := negotiateProtocolVersion("not-a-date"); got != "2025-06-18" {
		t.Fatalf("invalid got %q", got)
	}
}

func TestInitializeResultOmitsHeavyWorkShape(t *testing.T) {
	got := (&Server{}).initializeResult("2025-11-25", "sess-1")
	if got["protocolVersion"] != "2025-11-25" {
		t.Fatalf("%v", got["protocolVersion"])
	}
	caps, _ := got["capabilities"].(map[string]any)
	tools, _ := caps["tools"].(map[string]any)
	if _, ok := tools["listChanged"]; !ok {
		t.Fatalf("expected listChanged in tools capability: %v", got)
	}
	if _, ok := caps["prompts"]; !ok {
		t.Fatalf("expected prompts capability: %v", got)
	}
	if _, ok := caps["resources"]; !ok {
		t.Fatalf("expected resources capability: %v", got)
	}
	if got["sessionId"] != "sess-1" {
		t.Fatalf("internal sessionId plumbing missing")
	}
}
