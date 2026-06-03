package mcpproxy

import (
	"errors"
	"strings"
)

const resourcePrefix = "syna-mcp/"

func NamespacedTool(slug, toolName string) string {
	return slug + "__" + toolName
}

func NamespacedPrompt(slug, promptName string) string {
	return slug + "__" + promptName
}

func NamespacedResourceURI(slug, upstreamURI string) string {
	return resourcePrefix + slug + "/" + strings.TrimPrefix(upstreamURI, "/")
}

func ParseNamespacedTool(name string) (slug, tool string, ok bool) {
	slug, tool, ok = strings.Cut(name, "__")
	return slug, tool, ok && slug != "" && tool != ""
}

func ParseNamespacedPrompt(name string) (slug, prompt string, ok bool) {
	return ParseNamespacedTool(name)
}

func ParseNamespacedResourceURI(uri string) (slug, upstream string, ok bool) {
	if !strings.HasPrefix(uri, resourcePrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(uri, resourcePrefix)
	slug, upstream, ok = strings.Cut(rest, "/")
	if !ok || slug == "" || upstream == "" {
		return "", "", false
	}
	return slug, upstream, true
}

func FilterAllowlist(items []string, allowlist []string) []string {
	if len(allowlist) == 0 {
		return items
	}
	allowed := map[string]struct{}{}
	for _, v := range allowlist {
		allowed[v] = struct{}{}
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := allowed[item]; ok {
			out = append(out, item)
		}
	}
	return out
}

var ErrUnknownProxyTarget = errors.New("unknown proxied mcp target")
