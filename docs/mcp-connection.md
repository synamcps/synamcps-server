# MCP Connection Guide

This server supports:

- Streamable HTTP transport (`/mcp`)
- Legacy HTTP+SSE transport (`/sse` + `/messages`) when enabled

## Auth Options

### Claude Desktop Connectors (OAuth 2.1)

When `oauth_as.enabled` is true, SynaMCPs acts as an OAuth 2.1 Authorization Server for MCP clients:

1. Deploy SynaMCPs on a **public HTTPS** URL (Anthropic cloud egress cannot reach localhost).
2. In Claude Desktop: Settings → Connectors → Add custom connector.
3. Paste `https://<host>/mcp` (no bearer token).
4. Claude discovers OAuth metadata, registers a public client (DCR), and opens a browser login.
5. Sign in with an internal Syna user/password or a configured OIDC provider (`oauth.providers` with `client_id` / `client_secret_env_ref`).
6. Claude receives a Syna opaque access token and calls `/mcp` with `Authorization: Bearer …`.

Unauthenticated `/mcp` requests return:

```http
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer resource_metadata="https://<host>/.well-known/oauth-protected-resource"
```

### Static bearer tokens (Cursor / Claude Code / legacy)

- Create a scoped access token in Admin UI or via API.
- Put `Authorization: Bearer <token>` in the MCP client config.
- Keycloak / Google / generic OIDC JWTs and Teleport Proxy JWT are still accepted when configured.

## Streamable HTTP Example

1. Obtain bearer token (OAuth Connectors flow or static access token).
2. Send initialize request:

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":"1","method":"initialize","params":{}}'
```

3. Save returned `Mcp-Session-Id`.
4. Open SSE stream. `GET /mcp` (and `DELETE /mcp`) are authenticated, and the
   session can only be accessed by the principal that created it, so send the
   bearer token alongside the session id:

```bash
curl -N http://localhost:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: <session_id>"
```

## Legacy HTTP+SSE

If `transport.legacy_sse=true`:

- connect to `GET /sse`
- post messages to `/messages`

## Administrative MCP Tools

JWT/Web principals can administer SynaMCPs directly from MCP clients. Access-token
authentication intentionally hides and forbids administrative tools: access
tokens narrow access and must not manage token policy or ACLs.

Tool visibility:

- `platform_admin`: all `admin_*` tools.
- storage owners/admins: token, ACL, and storage archive tools for their storage.
- regular JWT users: own-token lifecycle tools only.

Token lifecycle from an MCP client:

1. `admin_token_create` with `name`, `mode`, and `storageIds`.
2. Save `rawToken` immediately. It is returned only once.
3. `admin_token_update_scopes` to narrow storage scopes or add MCP proxy scopes.
4. `admin_token_revoke` to revoke the token.

Administrative mutation tools write audit events with an `mcp.` action prefix.

Common tools:

- tokens: `admin_token_create`, `admin_token_list`, `admin_token_get`,
  `admin_token_revoke`, `admin_token_update_scopes`,
  `admin_token_update_rate_limit`
- users/groups: `admin_user_list`, `admin_user_get`, `admin_user_disable`,
  `admin_group_list`, `admin_group_members`, `admin_group_add_member`,
  `admin_group_remove_member`
- ACL/storage: `admin_acl_list`, `admin_acl_grant`, `admin_acl_revoke`,
  `admin_storage_create`, `admin_storage_archive`
- MCP proxy: `admin_mcp_server_list`, `admin_mcp_server_test`,
  `admin_mcp_scope_set`

## Troubleshooting

- `401`: missing/invalid token (including on `GET`/`DELETE /mcp`); with OAuth AS enabled, check `WWW-Authenticate` for `resource_metadata`
- `403`: issuer/audience/scope/policy mismatch, or the session belongs to a different principal
- `404` on `/mcp` stream: expired or unknown session ID
- `429`: per-token rate limit exceeded
- Claude Desktop OAuth fails on localhost: remote connectors require a public HTTPS Syna URL
