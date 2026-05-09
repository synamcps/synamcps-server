# Synamcps (SynaMCPs) — MCP + Knowledge Storage Gateway

`Synamcps` is a server that provides:

- **An MCP server** for LLM clients (Cursor / Claude Desktop / Claude Code / etc.)
- **An HTTP API** to create/search/read knowledge items
- **A Web Admin UI** to manage users/groups/storages/tokens, view status, and do basic diagnostics
- **Token-based access to storages** (tokens only *narrow* permissions), with ACL/RBAC, rate limiting, and usage/metrics

The server supports multiple auth methods (OIDC/Keycloak/Google/Teleport Proxy JWT) and an **internal login** for the Admin UI.

---

## Quick start (Docker Compose)

Requirements:

- Docker + Docker Compose

Run:

```bash
cp .env.example .env
make compose-up
```

Open:

- Web UI: `http://localhost:8080/login`
- MCP endpoint (streamable): `http://localhost:8080/mcp`
- HTTP API: `http://localhost:8080/api/*`

Stop:

```bash
make compose-down
```

---

## Local run (without Docker)

Requirements:

- Go **1.23+**
- Postgres, Redis, S3/MinIO available (or use Docker Compose as your infrastructure)

```bash
export CONFIG_PATH=configs/config.local.yaml
go run ./cmd/server
```

---

## Features (high level)

### Storages, ACL and tokens

- **Storage** is a logical entity tied to:
  - records in the metadata catalog (Postgres)
  - an S3 prefix (`storage.S3Prefix`)
  - search scoping (vector backend: pgvector/qdrant)
- **ACL bindings** define user/group access to a storage (read/write/admin/owner).
- **Access tokens**:
  - belong to a user (owner)
  - **do not expand** permissions — they *narrow* the owner's access (intersection of user ACL and token scopes)
  - can restrict: `storageIds`, `maxMode` (read/read_write), `toolAllowlist`, rate limits.

### Knowledge items

- You can add an item as:
  - **Text** (via the standard `POST /api/knowledge`)
  - **File** (upload → raw stored to S3 → best-effort extraction → summary+embeddings → item saved to storage)
  - **Link** (download → raw stored to S3 → extraction → summary+embeddings → item saved to storage)

### MCP

- MCP exposes a **dynamic `tools/list`** based on the bearer token (only allowed tools/storages are visible).
- Supports **Streamable HTTP** transport (`/mcp`) and optional legacy SSE.
- MCP tool names use `_` (underscore) to avoid filtering/warnings in some clients.

### Usage / Rate limit / Metrics

- Rate limiting per token (minute/hour/day + burst).
- Usage events (and status/errors) can be written to Redis TimeSeries (when enabled).
- `/metrics` exposes Prometheus-format metrics.

### Web Admin UI

The built-in admin UI (server-rendered HTML) lets you:

- Users / Groups / Group members
- Storages + Storage details (ACL, keys/tokens, items list)
- Tokens + MCP Connect wizard + delete
- Add item (Text/File/Link)
- Search (by token / by storage)
- Status (Postgres/Redis/S3/LLMs + error counters)

---

## Configuration

Default config: `configs/config.example.yaml`  
Override: `CONFIG_PATH=/path/to/config.yaml`

Key sections:

- `web.default_admin`: Admin UI username/password (password via env ref)
- `oauth.providers`: OIDC providers (issuer/audience/jwks_url)
- `teleport`: Teleport Proxy JWT (issuer/audience)
- `redis`: sessions + usage/time-series (when enabled)
- `s3`: endpoint/bucket + large document threshold
- `embedding`, `summarization`: LLMs (provider/model/api/api_key_env_ref)
- `vector_backend.active`: `pgvector` or `qdrant`
- `metadata_catalog.dsn`: Postgres DSN
- `api.allowed_origins`: strict CORS allowlist
- `usage`: accounting and time series, retention, exporters

Example `.env` for local development: `.env.example`.

---

## HTTP API

Authentication:

- **cookieAuth**: web UI session (`session_id` cookie)
- **bearerAuth**: `Authorization: Bearer <token>`

Common error codes:

- `401` — missing token/session
- `403` — forbidden (insufficient permissions for the storage/operation)
- `404` — not found
- `422` — invalid request

### Knowledge API

#### `GET /api/knowledge`

List items with pagination and filters.

Query params:

- `page` (int)
- `pageSize` (int)
- `storageId` (string) — limit to a specific storage
- `source` (string) — exact match
- `sourceUrl` (string)
- `sourceUrlMode` (`exact` | `partial`) — `partial` works only when `search.filters.source_url.allow_partial_match=true`

Response: `models.PaginatedKnowledgeList` (items + total + hasNext + page/pageSize).

#### `POST /api/knowledge`

Create an item from text.

Body:

```json
{
  "storageId": "storage-id-optional",
  "title": "Runbook",
  "text": "Long knowledge text...",
  "mimeType": "text/plain",
  "visibility": "personal",
  "groupIds": [],
  "source": "api",
  "sourceUrl": "https://docs.example.com/runbook"
}
```

Notes:

- if `storageId` is empty and access-service is enabled, the server uses/creates the user's personal storage
- `visibility` defaults to `personal`
- `groupIds` must be an array (not `null`)

#### `GET /api/knowledge/{docId}`

Return a single document.

#### `DELETE /api/knowledge/{docId}`

Delete a document (and associated embeddings in the vector store).

#### `POST /api/knowledge/search`

Embedding-based search.

Body:

```json
{
  "query": "kubernetes ingress timeout",
  "topK": 10,
  "filters": {
    "storageId": "storage-id-optional",
    "source": "api",
    "sourceUrl": "https://...",
    "sourceUrlMode": "exact"
  }
}
```

Response: an array of search hits (including snippet/title/source/sourceUrl).

### Ingest API (File/Link)

#### `POST /api/knowledge/ingest/file` (multipart)

Upload a file as an item:

- raw content is stored in S3
- best-effort text extraction is performed
- the pipeline produces summary + embeddings
- the final result is saved as a normal knowledge item

Multipart fields:

- `storageId` (string, optional)
- `title` (string, optional)
- `visibility` (`personal`|`group`|`public`, optional)
- `source` (string, optional)
- `sourceUrl` (string, optional)
- `mimeType` (string, optional)
- `file` (required)

Example:

```bash
curl -X POST http://localhost:8080/api/knowledge/ingest/file \
  -H "Authorization: Bearer $TOKEN" \
  -F "storageId=..." \
  -F "title=Spec" \
  -F "visibility=personal" \
  -F "file=@./spec.txt"
```

#### `POST /api/knowledge/ingest/link` (json)

Downloads a URL, stores raw content to S3, extracts text, and saves an item.

Body:

```json
{
  "storageId": "storage-id-optional",
  "title": "Optional title",
  "url": "https://example.com/docs",
  "visibility": "personal",
  "source": "link"
}
```

---

## Admin API (`/api/admin/*`)

All endpoints require authentication (cookie or bearer), and many require `platform_admin`.

### Users

- `GET /api/admin/me`
- `GET /api/admin/users` (platform_admin)
- `POST /api/admin/users` (platform_admin)
- `GET /api/admin/users/{id}` (admin или сам пользователь)
- `PATCH /api/admin/users/{id}` (admin или сам пользователь)
- `POST /api/admin/users/{id}/password` (admin или сам пользователь)
- `DELETE /api/admin/users/{id}` (platform_admin)

### Groups

- `GET /api/admin/groups` (platform_admin)
- `POST /api/admin/groups` (platform_admin)
- `DELETE /api/admin/groups/{id}` (platform_admin)
- `GET /api/admin/groups/{id}/members`
- `PUT /api/admin/groups/{id}/members/{userId}`
- `DELETE /api/admin/groups/{id}/members/{userId}`

### Storages

- `GET /api/admin/storages` (storages available to the current user)
- `POST /api/admin/storages`
- `DELETE /api/admin/storages/{id}`
- `GET /api/admin/storages/{id}` (storage details: storage + acl + tokens)
- `GET /api/admin/storages/{id}/acl`
- `PUT /api/admin/storages/{id}/acl`

### Tokens

- `GET /api/admin/tokens`
- `POST /api/admin/tokens`
- `DELETE /api/admin/tokens/{id}`
- `PATCH /api/admin/tokens/{id}/rate-limit`
- `POST /api/admin/tokens/{id}/revoke`
- `POST /api/admin/tokens/{id}/rotate`
- `GET/POST /api/admin/tokens/{id}/connect-options` (wizard для MCP клиентов)

### Usage / Status

- `GET /api/admin/usage/series`
- `GET /api/admin/usage/summary`
- `GET /api/admin/status` — component status + error counters (Redis TimeSeries)

---

## MCP

### Transport

- Streamable HTTP: `POST /mcp` (JSON-RPC) + `GET /mcp` (SSE stream по `Mcp-Session-Id`)
- Legacy SSE (если включено): `/sse` + `/messages`

### Minimal flow (streamable)

1) Obtain a bearer token (OIDC/Teleport/or internal)  
2) `initialize`:

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":"1","method":"initialize","params":{}}'
```

3) Save `Mcp-Session-Id` from headers/response (clients do this automatically)  
4) Open the stream:

```bash
curl -N http://localhost:8080/mcp -H "Mcp-Session-Id: <session_id>"
```

### Dynamic tools/list

`tools/list` returns only the tools allowed by the current bearer token and its storage scopes.

### tools/call

`tools/call` routes calls to the corresponding internal methods (knowledge_* etc.).

### MCP Connect (Web UI)

The Admin UI includes an **MCP Connect** page that generates:

- config file name
- `configBody` (JSON)
- step-by-step instructions

You can copy `configBody` by clicking it.

---

## Installation and operations

### Docker Compose

```bash
cp .env.example .env
make compose-up
```

Helpful:

- `make compose-down`
- `make seed-dev` (if used in your environment)

### Config and secrets

- `configs/config.example.yaml` — example config
- `configs/config.local.yaml` — local compose config (used in `docker-compose.yml`)
- `.env` — secrets (passwords/keys), example in `.env.example`

### CORS

`api.allowed_origins` is a strict allowlist. Unknown origins are rejected.
Web UI routes (`/`, `/login`, `/logout`, `/app*`) bypass the origin check.

---

## Troubleshooting

 - **Origin not allowed**:
   - add the origin to `api.allowed_origins`
   - make sure you open the Web UI via `/login` (web routes bypass origin-check)
 - **Invalid credentials**:
   - verify `.env` is loaded (in compose it is wired via `env_file: .env`)
   - verify `web.default_admin.password_env_ref`
 - **Time series are not created**:
   - you need Redis with the RedisTimeSeries module (`TS.ADD` must be supported)
   - or disable `usage.redis_timeseries`
 - **MCP tools “filtered out” warning**:
   - tool names already use `_` instead of `.`

---

## Documentation in this repo

- `docs/setup.md` — installation and run
- `docs/api.md` — basic knowledge endpoints
- `docs/mcp-connection.md` — MCP connection
- `docs/auth-setup.md` — auth providers
- `docs/openapi.yaml` — baseline OpenAPI stub

