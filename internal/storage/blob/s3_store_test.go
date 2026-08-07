package blob

import "testing"

func TestNormalizeKeyPrefix(t *testing.T) {
	if got := normalizeKeyPrefix(""); got != "" {
		t.Fatalf("empty: got %q", got)
	}
	if got := normalizeKeyPrefix("test9"); got != "test9/" {
		t.Fatalf("slug: got %q", got)
	}
	if got := normalizeKeyPrefix("test9/"); got != "test9/" {
		t.Fatalf("trailing: got %q", got)
	}
}

func TestObjectKeyAppliesPrefix(t *testing.T) {
	s := &Store{prefix: "test9/"}
	if got := s.objectKey("storages/abc/documents/1/source.txt"); got != "test9/storages/abc/documents/1/source.txt" {
		t.Fatalf("got %q", got)
	}
	s2 := &Store{}
	if got := s2.objectKey("storages/abc/x"); got != "storages/abc/x" {
		t.Fatalf("no prefix: got %q", got)
	}
}
