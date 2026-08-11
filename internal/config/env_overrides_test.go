package config

import "testing"

func TestApplyAgentEnvOverrides(t *testing.T) {
	t.Setenv("SYNAMCP_AGENT_PROVIDER", "openai-compatible")
	t.Setenv("SYNAMCP_AGENT_MODEL", "gpt-4.1-mini")
	t.Setenv("SYNAMCP_AGENT_ENDPOINT", "https://api.openai.com/v1/chat/completions")
	t.Setenv("SYNAMCP_AGENT_API_KEY_ENV", "AGENT_API_KEY")
	t.Setenv("SYNAMCP_AGENT_MAX_OUTPUT_TOKENS", "1024")

	cfg := Config{}
	applyEnvOverrides(&cfg)

	if !cfg.Agent.Enabled {
		t.Fatal("expected agent enabled")
	}
	if cfg.Agent.Provider != "openai-compatible" {
		t.Fatalf("provider: %q", cfg.Agent.Provider)
	}
	if cfg.Agent.Model != "gpt-4.1-mini" {
		t.Fatalf("model: %q", cfg.Agent.Model)
	}
	if cfg.Agent.API != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("api: %q", cfg.Agent.API)
	}
	if cfg.Agent.APIKeyEnvRef != "env:AGENT_API_KEY" {
		t.Fatalf("api_key_env_ref: %q", cfg.Agent.APIKeyEnvRef)
	}
	if cfg.Agent.MaxResponseTokens != 1024 {
		t.Fatalf("max_response_tokens: %d", cfg.Agent.MaxResponseTokens)
	}
}

func TestApplyModelEnvOverrides(t *testing.T) {
	t.Setenv("SYNAMCP_EMBEDDING_PROVIDER", "openai")
	t.Setenv("SYNAMCP_EMBEDDING_MODEL", "text-embedding-3-small")
	t.Setenv("SYNAMCP_EMBEDDING_ENDPOINT", "https://api.openai.com/v1/embeddings")
	t.Setenv("SYNAMCP_EMBEDDING_API_KEY_ENV", "env:EMBEDDING_API_KEY")
	t.Setenv("SYNAMCP_EMBEDDING_MAX_INPUT_TOKENS", "4096")

	cfg := Config{}
	applyEnvOverrides(&cfg)

	if cfg.Embedding.Provider != "openai" {
		t.Fatalf("provider: %q", cfg.Embedding.Provider)
	}
	if cfg.Embedding.API != "https://api.openai.com/v1/embeddings" {
		t.Fatalf("api: %q", cfg.Embedding.API)
	}
	if cfg.Embedding.APIKeyEnvRef != "env:EMBEDDING_API_KEY" {
		t.Fatalf("api_key_env_ref: %q", cfg.Embedding.APIKeyEnvRef)
	}
	if cfg.Embedding.MaxInputTokens != 4096 {
		t.Fatalf("max_input_tokens: %d", cfg.Embedding.MaxInputTokens)
	}
}
