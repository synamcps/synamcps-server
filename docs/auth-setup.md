# Authorization Setup

## Keycloak

1. Create realm `syna`.
2. Create client `syna-mcp`.
3. Configure issuer and audience in `oauth.providers`.

## Google

1. Register OAuth client in Google Cloud.
2. Set provider issuer `https://accounts.google.com`.
3. Restrict domains in `oauth.google_allowed_domains`.

## Generic OIDC

Add provider entry with `issuer`, `audience`, `jwks_url`.
Tokens are verified using provider JWKS and strict issuer/audience checks.

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
