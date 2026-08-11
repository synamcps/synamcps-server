package auth

import (
	"crypto/tls"
	"net/http"
	"testing"
)

func TestResolvePublicBaseURL(t *testing.T) {
	req := httptestRequest("ares.example", "https")
	if got := ResolvePublicBaseURL("http://localhost:8080", req); got != "https://ares.example" {
		t.Fatalf("got %q", got)
	}
	if got := ResolvePublicBaseURL("https://configured.example", req); got != "https://configured.example" {
		t.Fatalf("got %q", got)
	}
}

func httptestRequest(host, proto string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Host = host
	if proto == "https" {
		req.TLS = &tls.ConnectionState{}
	}
	req.Header.Set("X-Forwarded-Proto", proto)
	return req
}

func TestIsLoopbackPublicURL(t *testing.T) {
	if !IsLoopbackPublicURL("http://localhost:8080") {
		t.Fatal("expected localhost loopback")
	}
	if IsLoopbackPublicURL("https://ares.synamcps.net") {
		t.Fatal("expected public host")
	}
}
