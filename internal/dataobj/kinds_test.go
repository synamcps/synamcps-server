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
	if !HasMethod("securebus", "publish") || !HasMethod("securebus", "policy") || HasMethod("securebus", "produce") {
		t.Fatal("securebus method set")
	}
	if IsMutating("policy") || IsMutating("verify") || !IsMutating("publish") {
		t.Fatal("securebus mutating flags")
	}
	if !ValidKind("git") || !ValidKind("securebus") || ValidKind("unknown") {
		t.Fatal("kind validation")
	}
}
