# API Guide

## Auth Modes

- `cookieAuth`: web UI session (`session_id` cookie)
- `bearerAuth`: `Authorization: Bearer <access-token>`

## Knowledge Endpoints

- `GET /api/knowledge`
- `GET /api/knowledge/{docId}`
- `POST /api/knowledge`
- `POST /api/admin/knowledge` (web admin create path, defaults `source=admin`)
- `POST /api/knowledge/search`
- `DELETE /api/knowledge/{docId}`

## Source Metadata

- `source`: optional source type
  - default by channel: `mcp`, `api`, `admin`
- `sourceUrl`: optional source URL
- filtering:
  - `source`: exact match only
  - `sourceUrl`: exact + optional partial (controlled by `search.filters.source_url.allow_partial_match`)

## Example: Create

```bash
curl -X POST http://localhost:8080/api/knowledge \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title":"Runbook",
    "text":"Long knowledge text...",
    "mimeType":"text/plain",
    "visibility":"personal",
    "source":"api",
    "sourceUrl":"https://docs.example.com/runbook"
  }'
```

## Visibility

Read access to a storage is necessary but not sufficient. Per-document `visibility`
is also enforced on `GET /api/knowledge`, `GET /api/knowledge/{docId}` and
`POST /api/knowledge/search`:

- `public` — visible to anyone who can read the storage
- `personal` — visible only to the document owner
- `group` — visible to the owner or members of the document's groups

Listing is paginated server-side: `total`/`hasNext` already account for these
filters.

## Error Codes

- `401` unauthorized
- `403` forbidden (insufficient storage/document permission, or visibility)
- `404` not found
- `409` conflict
- `413` request body exceeds `limits.max_upload_bytes`
- `422` validation error
- `429` rate limit exceeded (per-token limits, applies to the REST API too)
