# Backlog

## Evaluate Official MCP Go SDK Migration

**Status:** deferred.

The server/proxy currently keeps the custom MCP implementation and focuses on typed JSON-RPC plus spec-compatible Streamable HTTP behavior.

Revisit migration to `github.com/modelcontextprotocol/go-sdk` when one of these is true:

- MCP protocol compatibility work starts taking sustained maintenance time.
- A target client requires behavior that is already covered by the official SDK.
- The SDK offers a cleaner fit for dynamic per-principal capabilities, auth context propagation, and proxy upstream sessions.

### Scope For A Future Spike

- Prototype the server side behind the existing `auth.Gateway`.
- Verify dynamic tools/resources/prompts filtered by ACL and token scopes.
- Verify usage/rate-limit accounting hooks.
- Prototype proxy upstream calls using the SDK client transport.
- Run e2e checks with Cursor and Claude Code.

### Risks To Reassess

- Dynamic capabilities may require one SDK server/session per authenticated request.
- SDK session handling must not bypass existing `Mcp-Session-Id` ownership checks.
- Protocol version defaults may differ from existing upstream servers.
- Proxy discovery results still need to map into local storage models and allowlists.
