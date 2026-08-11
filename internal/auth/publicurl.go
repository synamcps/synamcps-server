package auth

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// IsLoopbackPublicURL reports whether raw is empty or points at a loopback host.
// Deployments that leave oauth_as.public_base_url as http://localhost:8080 cause
// Claude Connectors to attempt Dynamic Client Registration against localhost.
func IsLoopbackPublicURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return true
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// PublicBaseFromRequest builds an absolute origin from forwarded proxy headers or the request host.
func PublicBaseFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := firstHeaderValue(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = strings.ToLower(proto)
	}
	host := firstHeaderValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// ResolvePublicBaseURL prefers a non-loopback configured URL; otherwise uses the request origin.
func ResolvePublicBaseURL(configured string, r *http.Request) string {
	configured = strings.TrimRight(strings.TrimSpace(configured), "/")
	if configured != "" && !IsLoopbackPublicURL(configured) {
		return configured
	}
	if derived := strings.TrimRight(PublicBaseFromRequest(r), "/"); derived != "" {
		return derived
	}
	return configured
}

func firstHeaderValue(v string) string {
	if v == "" {
		return ""
	}
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
