package config_test

import (
	"testing"

	"github.com/synamcps/synamcps-server/internal/config"
)

func TestValidateAppliesPoolDefaults(t *testing.T) {
	cfg := config.Config{
		Server:        config.ServerConfig{ListenAddr: ":8080"},
		Chunking:      config.ChunkingConfig{ChunkSize: 100, Overlap: 10},
		Summarization: config.ModelConfig{Model: "sum"},
		Embedding:     config.ModelConfig{Model: "emb"},
		Metadata:      config.MetadataConfig{DSN: "postgres://localhost/syna"},
		API:           config.APIConfig{AllowedOrigins: []string{"http://localhost:8080"}},
		OAuth: config.OAuthConfig{Providers: []config.ProviderConfig{
			{Name: "keycloak", Issuer: "https://issuer", Audience: "aud", JWKSURL: "https://issuer/certs"},
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if cfg.Metadata.PoolMaxConns != 20 {
		t.Fatalf("PoolMaxConns = %d, want 20", cfg.Metadata.PoolMaxConns)
	}
	if cfg.Metadata.PoolMinConns != 2 {
		t.Fatalf("PoolMinConns = %d, want 2", cfg.Metadata.PoolMinConns)
	}
}
