---
name: SynaMCPs Operations
description: Operate, administer, and automate SynaMCPs through REST and MCP tools.
---

# SynaMCPs Operations Skill

Use this skill when configuring SynaMCPs, administering users/groups/storages/tokens/ACL, connecting MCP clients, or scripting automation against the REST API and MCP tools.

## Setup And Configuration

Local development:

```bash
make compose-up
```

The server starts at `http://localhost:8080`. Stop it with:

```bash
make compose-down
```

Configuration is loaded from `CONFIG_PATH`; Docker Compose uses `/app/configs/config.local.yaml`. The reference config is `configs/config.example.yaml`.

Key config sections:

- `server`: listen address and `dev_mode`.
- `web`: shared login, admin UI, and `web.user_ui.enabled` for the user-facing `/app`.
- `oauth` / `teleport`: identity providers. See `docs/auth-setup.md`.
- `metadata_catalog`: Postgres DSN and pool sizing.
- `s3`: object storage endpoint, bucket, credentials env refs, and large-document threshold.
- `vector_backend`: `pgvector` or `qdrant`.
- `redis`: sessions and rate-limit state for multi-instance deployments.
- `usage`: rate limits, metrics retention, and exporters.
- `mcp_proxy`: external MCP server proxy configuration.

Required local environment variables usually include:

```bash
S3_ACCESS_KEY=minio
S3_SECRET_KEY=miniostorage
EMBEDDING_API_KEY=dummy
SUMMARIZATION_API_KEY=dummy
```

For local unsigned JWT development, `jwks_url: insecure` requires `server.dev_mode: true` and loopback-only listen address. Never use this in production.

## Diagnostics

Use these first when an operation fails:

```bash
curl -fsS "$SYNA_BASE_URL/healthz"
curl -fsS "$SYNA_BASE_URL/readyz"
curl -fsS "$SYNA_BASE_URL/api/admin/status" -H "Authorization: Bearer $SYNA_TOKEN"
curl -fsS "$SYNA_BASE_URL/metrics"
```

Common responses:

- `401`: missing or invalid bearer token.
- `403`: caller lacks platform, storage, ACL, token, or visibility rights.
- `404`: object or MCP session not found.
- `413`: request exceeds `limits.max_upload_bytes`.
- `429`: per-token rate limit exceeded.

## Administration

Administrators can use REST endpoints under `/api/admin/*` or MCP `admin_*` tools. MCP admin tools are visible only for JWT/web principals. Access-token authentication intentionally hides and forbids admin tools.

Typical REST tasks:

- List storages: `GET /api/admin/storages`
- Create storage: `POST /api/admin/storages`
- List storage ACL: `GET /api/admin/storages/{storageId}/acl`
- Grant ACL: `PUT /api/admin/storages/{storageId}/acl`
- List tokens: `GET /api/admin/tokens`
- Create token: `POST /api/admin/tokens`
- Revoke token: `POST /api/admin/tokens/{tokenId}/revoke`
- Update rate limit: `PATCH /api/admin/tokens/{tokenId}/rate-limit`
- Set MCP scopes: `PATCH /api/admin/tokens/{tokenId}/mcp-scopes`
- Check status: `GET /api/admin/status`

MCP admin tools:

- Tokens: `admin_token_create`, `admin_token_list`, `admin_token_get`, `admin_token_revoke`, `admin_token_update_scopes`, `admin_token_update_rate_limit`
- Users/groups: `admin_user_list`, `admin_user_get`, `admin_user_disable`, `admin_group_list`, `admin_group_members`, `admin_group_add_member`, `admin_group_remove_member`
- ACL/storage: `admin_acl_list`, `admin_acl_grant`, `admin_acl_revoke`, `admin_storage_create`, `admin_storage_archive`
- MCP proxy: `admin_mcp_server_list`, `admin_mcp_server_test`, `admin_mcp_scope_set`

Token creation returns `rawToken` once. Store it immediately; subsequent API calls return only token metadata.

## Knowledge Workflows

REST knowledge endpoints:

- Save: `POST /api/knowledge`
- List: `GET /api/knowledge`
- Get: `GET /api/knowledge/{docId}`
- Search: `POST /api/knowledge/search`
- Delete: `DELETE /api/knowledge/{docId}`
- Ingest file: `POST /api/knowledge/ingest/file`
- Ingest link: `POST /api/knowledge/ingest/link`

MCP knowledge tools:

- `knowledge_save`
- `knowledge_search`
- `knowledge_get`
- `knowledge_delete`

## Agent Chat

The built-in agent uses selected SynaMCPs storages as memory. It searches only
the conversation datasets intersected with the caller's current ACL/token
rights, and it saves memories through the same knowledge write path.

Web UI routes:

- `/login`: shared login.
- `/admin`: administrator console; server-side admin role is required.
- `/app`: user chat app when `web.user_ui.enabled=true`.

REST agent endpoints:

- Create/list conversations: `POST /api/agent/conversations`, `GET /api/agent/conversations`
- Send message: `POST /api/agent/conversations/{id}/messages`
- Read history: `GET /api/agent/conversations/{id}/messages`

Message responses are SSE events: `conversation`, `documents`, optional
`saved_memory`, `message`, and `done`. Users can ask "remember ..." or
"запомни ..." to save a personal memory into the first selected dataset.

Visibility rules:

- `personal`: owner only.
- `group`: owner or listed groups.
- `public`: any caller with read access to the storage.

Ingest is asynchronous. New documents may return as `processing`; search only returns `ready` documents.

## MCP Client Connection

Streamable HTTP endpoint:

```text
$SYNA_BASE_URL/mcp
```

Use a bearer token in the MCP client config:

```json
{
  "mcpServers": {
    "syna-knowledge": {
      "url": "http://localhost:8080/mcp",
      "headers": {
        "Authorization": "Bearer <token>"
      }
    }
  }
}
```

See `docs/mcp-connection.md` for Cursor, Claude Desktop, Claude Code, and generic client notes.

## Automation Recipes

Examples live in `scripts/examples/`.

Run a syntax/help smoke check:

```bash
scripts/examples/smoke.sh
```

Run live examples by exporting:

```bash
export SYNA_BASE_URL=http://localhost:8080
export SYNA_TOKEN=<jwt-or-admin-token>
```

Examples:

- `scripts/examples/mcp-token-lifecycle.sh`: create, narrow, and revoke a token via MCP admin tools.
- `scripts/examples/rotate-token.sh`: rotate an existing token through REST.
- `scripts/examples/audit-unused-tokens.sh`: list tokens that have never been used or are older than a threshold.
- `scripts/examples/bulk-grant-acl.sh`: grant one ACL role to many subject keys.
- `scripts/examples/export-usage-metrics.sh`: export usage summary/series JSON.

Automation conventions:

- Treat create operations as non-idempotent unless the script accepts an existing ID.
- Prefer update/replace operations for repeatable narrowing (`admin_token_update_scopes`, `PATCH /mcp-scopes`).
- Do not print raw tokens in shared logs.
- Use `curl -fsS` so scripts fail on HTTP errors.
- Re-run diagnostics after `401`, `403`, `429`, or readiness failures.

## Maintenance Rule

Any PR changing public REST API, `docs/openapi.yaml`, MCP tool names/schemas, auth modes, token scopes, or admin workflows must update this `SKILLS.md` and relevant scripts under `scripts/examples/`.
