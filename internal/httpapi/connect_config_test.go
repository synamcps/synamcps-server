package httpapi

import (
	"strings"
	"testing"

	"github.com/synamcps/synamcps-server/internal/models"
)

func TestBuildConnectConfigClaudeDesktopOAuth(t *testing.T) {
	cfg := buildConnectConfig(models.MCPClientClaudeDesktop, "https://syna.example", "secret", true)
	if cfg.ConfigBody != "https://syna.example/mcp" {
		t.Fatalf("ConfigBody=%q", cfg.ConfigBody)
	}
	if cfg.ConfigFileName != "claude-connector.txt" {
		t.Fatalf("ConfigFileName=%q", cfg.ConfigFileName)
	}
	joined := strings.Join(cfg.Instructions, " ")
	if !strings.Contains(joined, "Connectors") {
		t.Fatalf("instructions=%v", cfg.Instructions)
	}

	legacy := buildConnectConfig(models.MCPClientClaudeDesktop, "https://syna.example", "secret", false)
	if !strings.Contains(legacy.ConfigBody, "Authorization") {
		t.Fatalf("expected bearer config, got %s", legacy.ConfigBody)
	}
}
