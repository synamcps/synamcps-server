# Setup Guide

## Prerequisites

- Docker + Docker Compose
- Go 1.23+

## Local Run

```bash
make compose-up
```

Server starts on `http://localhost:8080`.

Web routes:

- `/login`: shared login. Platform admins go to `/admin`; regular users go to `/app`.
- `/admin`: administrative console for users/groups/storages/tokens/MCP proxy/status.
- `/app`: user interface centered on the SynaMCPs agent chat when `web.user_ui.enabled=true`.

## Config

- default config: `configs/config.example.yaml`
- override with `CONFIG_PATH=/path/to/config.yaml`

Key sections:

- `embedding`: embedding provider/model/api/tokens
- `summarization`: separate LLM for summary provider/model/api/tokens
- `vector_backend.active`: `pgvector` or `qdrant`
- `api.allowed_origins`: strict CORS allowlist (unknown origins are rejected)
- `redis`: session backend settings (`addr`, `password`, `db`, key prefix, TTL). Compose uses `redis/redis-stack-server` so TimeSeries (`TS.*`) is available for counters and optional usage metrics.
- `queue.rabbitmq.url`: optional RabbitMQ for queue objects (`docker compose --profile rabbitmq up`)
- `bus.kafka.brokers`: optional Kafka for bus objects (`docker compose --profile kafka up`)
- `securebus.policy_signing_key_env_ref`: Ed25519 seed for signing topic policies (`SECUREBUS_POLICY_SIGNING_KEY`); empty generates an ephemeral key (dev only)
- `securebus.policy_grace_seconds`: allow previous policy version briefly during rotation (default 300)
- `limits.max_upload_bytes`: max request body size for the REST API (oversized bodies get `413`; default ~40 MiB)
- `usage`: per-token rate limits (minute/hour/day + burst), retention and exporters
- `agent`: built-in chat agent provider/model, system prompt, context limits, allowed knowledge tools, and conversation TTL
- `web.user_ui.enabled`: enables the user-facing `/app` interface independently from the admin UI

Typed data objects (variable, counter, table, queue, bus, securebus, schedule, log, git, storage) are managed in Admin → Data objects. See `docs/api.md` and `docs/securebus-envelope.md`.

### Securebus agent sidecar

Claude Desktop / Cursor cannot hold Ed25519 keys. Run the local stdio MCP sidecar:

```bash
go build -o securebus-agent ./cmd/securebus-agent
export SYNA_BASE_URL=http://localhost:8080
export SYNA_TOKEN=<opaque access token>
export SYNA_TOKEN_ID=<token id from admin UI>
./securebus-agent
```

Cursor / Claude Desktop stdio config example:

```json
{
  "mcpServers": {
    "securebus": {
      "command": "/path/to/securebus-agent",
      "env": {
        "SYNA_BASE_URL": "https://your-host",
        "SYNA_TOKEN": "<token>",
        "SYNA_TOKEN_ID": "<token-id>"
      }
    }
  }
}
```

Tools: `keygen`, `register`, `securebus_publish`, `securebus_attest`, plus read tools (`policy`, `verify`, `consume`, `keys`).

### Securebus LangChain example

A three-agent demo (Streamlit Chat → Attester → Playwright Executor) lives in `examples/securebus/`. It registers client-held Ed25519 keys, publishes signed tool-call messages, attests against config `rule_text`, and only then browses. See [examples/securebus/README.md](../examples/securebus/README.md).

## Deployment model

The server is designed as a **single-instance** deployment when Redis is not
configured: MCP sessions, web sessions, and usage counters are kept in process
memory with TTL-based eviction on shutdown. For horizontal scaling, configure
Redis (`redis.addr`) so session and rate-limit state is shared across replicas.

Postgres connections use a **single shared pool** per process (`metadata_catalog.pool_*`).

Schema is managed with **versioned SQL migrations** in `migrations/`. On startup the
server applies pending migrations automatically (`golang-migrate`). To add a schema
change, add a new file pair `NNNNNN_description.up.sql` / `.down.sql` — do not put
DDL in store code.

Optional: set `MIGRATIONS_PATH` to override the migrations directory (default:
`migrations` relative to the working directory).

## Stop

```bash
make compose-down
```
