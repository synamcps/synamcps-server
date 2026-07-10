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
- `redis`: session backend settings (`addr`, `password`, `db`, key prefix, TTL)
- `limits.max_upload_bytes`: max request body size for the REST API (oversized bodies get `413`; default ~40 MiB)
- `usage`: per-token rate limits (minute/hour/day + burst), retention and exporters
- `agent`: built-in chat agent provider/model, system prompt, context limits, allowed knowledge tools, and conversation TTL
- `web.user_ui.enabled`: enables the user-facing `/app` interface independently from the admin UI

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
