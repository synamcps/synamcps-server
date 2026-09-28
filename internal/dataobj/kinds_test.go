package dataobj

import "testing"

func TestMethodsClosedPerKind(t *testing.T) {
	if !HasMethod("variable", "set") || HasMethod("variable", "enqueue") {
		t.Fatal("variable method set")
	}
	if !HasMethod("queue", "enqueue") || HasMethod("queue", "set") {
		t.Fatal("queue method set")
	}
	if !HasMethod("storage", "search") {
		t.Fatal("storage search")
	}
	if !ValidKind("git") || ValidKind("unknown") {
		t.Fatal("kind validation")
	}
}
