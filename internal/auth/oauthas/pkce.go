package oauthas

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
)

func VerifyPKCES256(codeVerifier, codeChallenge string) bool {
	if codeVerifier == "" || codeChallenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(codeVerifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return computed == codeChallenge
}

func RedirectURIAllowed(redirectURI string, registered []string, patterns []string) bool {
	if redirectURI == "" {
		return false
	}
	u, err := url.Parse(redirectURI)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	if u.Fragment != "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	isLocal := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if scheme != "https" && !(scheme == "http" && isLocal) {
		return false
	}
	if len(registered) > 0 {
		for _, allowed := range registered {
			if allowed == redirectURI {
				return matchPatterns(redirectURI, patterns)
			}
		}
		return false
	}
	return matchPatterns(redirectURI, patterns)
}

func matchPatterns(value string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if matchSimpleGlob(pattern, value) {
			return true
		}
	}
	return false
}

// matchSimpleGlob supports a single trailing '*' wildcard.
func matchSimpleGlob(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(value, prefix)
	}
	return pattern == value
}

func ClientHasRedirectURI(client Client, redirectURI string) bool {
	for _, uri := range client.RedirectURIs {
		if uri == redirectURI {
			return true
		}
	}
	return false
}
