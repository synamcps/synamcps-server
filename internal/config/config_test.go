package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/synamcps/synamcps-server/internal/config"
)

func TestLoadOverridesMetadataDSNFromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
server:
  listen_addr: ":8080"
chunking:
  chunk_size: 100
  overlap: 10
summarization:
  model: "sum"
embedding:
  model: "emb"
metadata_catalog:
  dsn: "postgres://postgres:postgres@postgres:5432/syna?sslmode=disable"
api:
  allowed_origins:
    - "http://localhost:8080"
oauth:
  providers:
    - name: keycloak
      issuer: "https://issuer"
      audience: "aud"
      jwks_url: "https://issuer/certs"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("METADATA_CATALOG_DSN", "postgres://tenant:secret@157.90.119.254:5432/my_slug?sslmode=disable")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := "postgres://tenant:secret@157.90.119.254:5432/my_slug?sslmode=disable"
	if cfg.Metadata.DSN != want {
		t.Fatalf("Metadata.DSN = %q, want %q", cfg.Metadata.DSN, want)
	}
}

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
