package oauthas

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestVerifyPKCES256(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if !VerifyPKCES256(verifier, challenge) {
		t.Fatal("expected PKCE verification to succeed")
	}
	if VerifyPKCES256("wrong", challenge) {
		t.Fatal("expected PKCE verification to fail for wrong verifier")
	}
	if VerifyPKCES256("", challenge) || VerifyPKCES256(verifier, "") {
		t.Fatal("empty verifier/challenge must fail")
	}
}

func TestRedirectURIAllowed(t *testing.T) {
	patterns := []string{"http://localhost*", "http://127.0.0.1*", "https://*"}
	cases := []struct {
		uri        string
		ok         bool
		registered []string
	}{
		{uri: "http://localhost:1234/callback", ok: true},
		{uri: "http://127.0.0.1:8787/cb", ok: true},
		{uri: "https://claude.ai/api/mcp/auth_callback", ok: true},
		{uri: "http://evil.example/cb", ok: false},
		{uri: "https://ok.example/cb", registered: []string{"https://other.example/cb"}, ok: false},
		{uri: "https://ok.example/cb", registered: []string{"https://ok.example/cb"}, ok: true},
		{uri: "ftp://localhost/cb", ok: false},
	}
	for _, tc := range cases {
		got := RedirectURIAllowed(tc.uri, tc.registered, patterns)
		if got != tc.ok {
			t.Fatalf("RedirectURIAllowed(%q)=%v want %v", tc.uri, got, tc.ok)
		}
	}
}
