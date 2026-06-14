# Synamcps — Marketing Description & Pipeline Scenarios

## The product, in one line

**Synamcps is the secure gateway that turns your company's scattered knowledge into a single, permission-aware MCP endpoint that any LLM client or AI agent can safely query.**

---

## Marketing description

### The pitch

Your enterprise knowledge lives everywhere — runbooks, specs, wikis, support tickets, PDFs, and a growing pile of internal MCP servers. Your AI agents and copilots need that knowledge, but giving them raw access is a security nightmare: no access control, no audit trail, no rate limits, and no way to stop one token from reading everything.

**Synamcps sits between your AI and your knowledge.** It's an MCP server *and* a knowledge gateway in one: ingest documents, files, and links into governed storages; expose them through the open **Model Context Protocol** so Claude, Cursor, Claude Code, and your custom agents connect in seconds; and enforce **who can see what** down to the individual document.

### What makes it different

| Capability | What it means for you |
|---|---|
| **Permission-narrowing tokens** | Tokens never *expand* access — they only intersect a user's existing ACL with token scopes. A leaked token can't do more than its owner, and usually far less (read-only, specific storages, specific tools). |
| **Document-level visibility** | `personal` / `group` / `public` is enforced *on top of* storage access. Reading a storage is necessary but never sufficient — private docs stay private even to other storage readers. |
| **Dynamic tool exposure** | MCP `tools/list` is computed per-token. Each agent sees only the tools and storages it's allowed to touch — nothing to discover, nothing to probe. |
| **MCP proxy / federation** | Register upstream HTTP/SSE MCP servers in the Admin UI, auto-discover their tools/resources/prompts, and re-expose them behind the same ACL and rate limits. One front door for every MCP. Upstream secrets stored encrypted. |
| **Governed ingestion** | Add knowledge as text, file upload, or link. Files and links run through extraction → summary → embeddings automatically. Link ingestion has built-in **SSRF protection** (refuses loopback, link-local, cloud-metadata `169.254.169.254`, and private ranges — even across redirects). |
| **Built-in RAG search** | Embedding-based semantic search over pgvector or Qdrant, with source/sourceUrl/storage filters and pagination. |
| **Enterprise auth** | OIDC / Keycloak / Google / Teleport Proxy JWT, plus internal login for the Admin UI. |
| **Rate limits & metrics** | Per-token minute/hour/day + burst limits on *both* MCP and REST. Prometheus `/metrics`, Redis TimeSeries usage accounting, component status dashboard. |
| **Admin UI** | Server-rendered console for users, groups, storages, ACLs, tokens, ingestion, search, and an **MCP Connect wizard** that generates ready-to-paste client config. |

### Who it's for

Platform and ML teams who want to ship internal copilots and agentic workflows **without** writing their own auth, RBAC, rate limiting, ingestion pipeline, and MCP plumbing — and without their security team blocking the launch.

### Positioning statement

> **Synamcps is the governance layer for enterprise RAG and agents.** Bring any LLM client; we handle the identity, the permissions, the knowledge ingestion, and the MCP transport — so your AI gets exactly the context it's allowed to have, and nothing more.

---

## Scenarios: Corporate RAG pipelines

### 1. Governed knowledge base for an internal copilot

Centralize runbooks, architecture docs, and policies into per-team **storages**. Ingest existing docs via `POST /api/knowledge/ingest/file` (PDF/specs) and `ingest/link` (wiki URLs) — extraction, summarization, and embeddings happen automatically. Your copilot calls `POST /api/knowledge/search` (or the `knowledge_*` MCP tools) for semantic retrieval. **Document visibility** keeps HR's `personal`/`group` docs out of an engineer's results even when both share a storage.

### 2. Multi-tenant RAG with hard isolation

Give each customer or business unit its own storage (its own S3 prefix + vector scope). Issue tokens scoped to `storageIds` with `maxMode: read`. Because tokens *narrow* rather than grant, a misconfigured client physically cannot retrieve another tenant's vectors. CORS allowlist + per-token rate limits round out the isolation.

### 3. Continuous ingestion from external systems

Wire your Confluence/Notion/Jira exporters or a nightly crawler to `POST /api/knowledge` with stable `source` and `sourceUrl` labels for traceability. Re-ingest updates idempotently by source URL; filter retrieval by `source` to scope a query to "only Jira" or "only the security wiki." SSRF guard means even an attacker-supplied link can't pivot to cloud metadata.

### 4. Compliance-grade retrieval with audit

Every call flows through tokenized auth with Prometheus metrics and Redis TimeSeries usage events. Security gets per-token usage series, error counters, and a `/metrics` feed — answering "which agent read which storage, how often" without bolting on a separate observability stack.

---

## Scenarios: Agentic pipelines

### 5. One MCP front door for a fleet of agents

Point Claude Code, Cursor, and custom agents at a single `/mcp` endpoint. Each agent authenticates with its own bearer token, and `tools/list` returns a **different toolset per token** — a read-only research agent sees only `knowledge_search`; an authoring agent additionally sees write tools. No agent can call a tool it wasn't granted.

### 6. MCP federation / proxy hub

Your org already runs several MCP servers (a Jira MCP, a GitHub MCP, an internal data MCP). Register them all in the **MCP Servers** tab. Synamcps discovers their tools/resources/prompts, namespaces them (`{slug}__{tool}`), and re-exposes them behind unified ACLs, per-token scopes, and rate limits — with upstream credentials encrypted at rest. Agents connect once and reach everything they're entitled to.

### 7. Least-privilege agents with rotatable credentials

Provision short-lived, narrowly-scoped tokens per agent run: specific `toolAllowlist`, `storageIds`, and rate limits. Rotate or revoke instantly via the Admin API (`/tokens/{id}/rotate`, `/revoke`) when a workflow ends or a key is suspected leaked. Burst + per-window limits cap runaway loops so a misbehaving agent gets `429`'d instead of hammering downstream systems.

### 8. Write-back / knowledge-curating agents

An agent that summarizes incidents can persist its findings back as `group`-visible knowledge items (`POST /api/knowledge`), feeding the next retrieval cycle — a self-improving knowledge loop, with the agent's write scope bounded by `maxMode: read_write` and its allowed storages.

### 9. Human-in-the-loop onboarding via MCP Connect

Non-technical teammates use the Admin UI's **MCP Connect wizard** to generate copy-paste client config for Claude Desktop / Cursor, picking storages and tokens from name dropdowns — no raw IDs, no hand-editing JSON. Self-service agent enablement without a platform ticket.
