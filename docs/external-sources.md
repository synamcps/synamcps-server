# External Source Integration

Use API with bearer token to import knowledge from external systems.

## Create Knowledge

`POST /api/knowledge`

Fields:

- `title`
- `text`
- `visibility`
- optional `groupIds`
- optional `source`
- optional `sourceUrl`

If `source` is omitted for API calls, backend defaults it to `api`.

## Search and Filters

`POST /api/knowledge/search`

- `filters.source` exact match
- `filters.sourceUrl` exact or partial (`filters.sourceUrlMode=partial`)

## Link Import (`POST /api/knowledge/ingest/link`)

The server fetches the URL on your behalf, so it is restricted to prevent SSRF:

- only `http`/`https` schemes are accepted
- internal targets are refused (loopback, link-local incl. cloud metadata
  `169.254.169.254`, multicast and private ranges), including across redirects

## Batch Import Tips

- keep documents below `limits.max_upload_bytes` — the server enforces this and
  returns `413` for oversized request bodies
- rate limits apply to the REST API per token; back off on `429`
- provide stable `sourceUrl` for traceability
- include source-specific labels via `source`
