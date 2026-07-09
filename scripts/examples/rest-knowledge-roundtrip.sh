#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Save, search, get, and optionally delete a knowledge item through REST.

Required rights:
  knowledge.write for save, knowledge.search for search, knowledge.read for get,
  and knowledge.delete for cleanup on STORAGE_ID.

Environment:
  SYNA_BASE_URL   Base URL, for example http://localhost:8080
  SYNA_TOKEN      Bearer token with knowledge scopes
  STORAGE_ID      Storage id for the document
  TITLE           Optional title
  TEXT            Optional text
  DELETE_DOC      Optional. Set to 1 to delete the created document.

Example:
  SYNA_BASE_URL=http://localhost:8080 SYNA_TOKEN="$TOKEN" STORAGE_ID=default \
    scripts/examples/rest-knowledge-roundtrip.sh

Expected response:
  Created document JSON, search results JSON, and fetched document JSON.
EOF
}

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  usage
  exit 0
fi

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "missing required environment variable: $name" >&2
    usage >&2
    exit 2
  fi
}

require_env SYNA_BASE_URL
require_env SYNA_TOKEN
require_env STORAGE_ID

TITLE="${TITLE:-Example runbook}"
TEXT="${TEXT:-SynaMCPs example knowledge roundtrip text.}"

payload="$(python3 - <<PY
import json
print(json.dumps({
  "storageId": "$STORAGE_ID",
  "title": "$TITLE",
  "text": "$TEXT",
  "mimeType": "text/plain",
  "visibility": "personal",
  "source": "script-example",
  "sourceUrl": "https://example.invalid/synamcps/rest-knowledge-roundtrip"
}))
PY
)"

created="$(curl -fsS -X POST "$SYNA_BASE_URL/api/knowledge" \
  -H "Authorization: Bearer $SYNA_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$payload")"
echo "$created"

doc_id="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["docId"])' <<<"$created")"

curl -fsS -X POST "$SYNA_BASE_URL/api/knowledge/search" \
  -H "Authorization: Bearer $SYNA_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$(python3 - <<PY
import json
print(json.dumps({"query": "roundtrip", "storageId": "$STORAGE_ID", "limit": 5}))
PY
)"
echo

curl -fsS "$SYNA_BASE_URL/api/knowledge/$doc_id" \
  -H "Authorization: Bearer $SYNA_TOKEN"
echo

if [[ "${DELETE_DOC:-0}" == "1" ]]; then
  curl -fsS -X DELETE "$SYNA_BASE_URL/api/knowledge/$doc_id" \
    -H "Authorization: Bearer $SYNA_TOKEN"
  echo
fi
