# Authorization Setup

## Keycloak

1. Create realm `syna`.
2. Create client `syna-mcp`.
3. Configure issuer and audience in `oauth.providers`.
4. For browser login during MCP OAuth AS authorize, also set `client_id` and `client_secret_env_ref` on that provider.

## Google

1. Register OAuth client in Google Cloud.
2. Set provider issuer `https://accounts.google.com`.
3. Restrict domains in `oauth.google_allowed_domains`.
4. Set `client_id` / `client_secret_env_ref` if using Google as an authorize login option.

## Generic OIDC

Add provider entry with `issuer`, `audience`, `jwks_url`.
Tokens are verified using provider JWKS and strict issuer/audience checks.

## MCP OAuth 2.1 Authorization Server (`oauth_as`)

Enable SynaMCPs as the Authorization Server that Claude Desktop Connectors discover:

```yaml
server:
  public_base_url: "https://syna.example.com"

oauth_as:
  enabled: true
  # MUST be the public HTTPS origin for Claude Connectors (not localhost in production).
  public_base_url: "https://syna.example.com"  # optional; falls back to server.public_base_url
  authorization_code_ttl_seconds: 300
  access_token_ttl_hours: 24
  refresh_token_ttl_hours: 720
  default_token_mode: "read_write"
  allowed_redirect_uri_patterns:
    - "http://localhost*"
    - "http://127.0.0.1*"
    - "https://*"
```

Endpoints:

- `GET /.well-known/oauth-protected-resource`
- `GET /.well-known/oauth-authorization-server`
- `POST /oauth/register` (dynamic client registration)
- `GET /oauth/authorize` (+ login / OIDC)
- `POST /oauth/token` (authorization_code + PKCE S256, refresh_token)
- `GET /oauth/callback` (IdP callback)

Successful token exchange mints a normal Syna opaque access token bound to the logged-in user (readable storages, `default_token_mode`). `/mcp` validation is unchanged.

Login options on `/oauth/authorize`:

- Internal username/password (`web.default_admin` and access users)
- Configured OIDC providers that have `client_id` set

Claude Desktop remote connectors require a public HTTPS Syna URL.

**Required for production:** set `server.public_base_url` and/or `oauth_as.public_base_url`
to the public HTTPS origin clients use (example: `https://ares.synamcps.net`). If this stays
`http://localhost:8080`, OAuth metadata advertises localhost and Claude fails with
“Couldn’t register with … sign-in service” because Anthropic cloud cannot reach localhost.

When the configured base URL is loopback, SynaMCPs falls back to `X-Forwarded-Proto` /
`X-Forwarded-Host` / `Host` for metadata and `WWW-Authenticate`, but you should still set
the public URL explicitly.

Also allow Anthropic egress (`160.79.104.0/21`) and user agents such as `Claude-User` /
`python-httpx` through Cloudflare Bot Fight Mode / WAF for `/mcp`, `/oauth/*`, `/register`,
and `/.well-known/*`.

## Development: unsigned JWT (`jwks_url: insecure`)

For local development only, a provider may set `jwks_url: insecure` to skip JWT
signature verification. This mode is **blocked unless** `server.dev_mode: true`
is set in config, and it is **rejected** when `listen_addr` binds to a non-loopback
interface. On startup the server logs a prominent warning when insecure mode is active.

Never enable `dev_mode` or `jwks_url: insecure` in production.

## Teleport Proxy JWT

Enable `teleport.enabled`.

- configure trusted `teleport.issuer`
- configure `teleport.audience`

If request token matches issuer/audience, auth source is `teleport_proxy`.

## Access Tokens

- user delegated tokens are supported
- service tokens should be scope-limited (`knowledge.read`, `knowledge.write`, `knowledge.search`, `knowledge.delete`)
- managing a token (delete/revoke/rotate/rate-limit/mcp-scopes) requires being the token **owner** or `platform_admin`
- storage ACL changes require `acl.manage` on that storage; group membership changes require `platform_admin`
