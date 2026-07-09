#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Save, search, get, and optionally delete a knowledge item through MCP tools.

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
    scripts/examples/mcp-knowledge-roundtrip.sh

Expected response:
  MCP JSON-RPC responses for knowledge_save, knowledge_search, and knowledge_get.
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

TITLE="${TITLE:-Example MCP runbook}"
TEXT="${TEXT:-SynaMCPs MCP example knowledge roundtrip text.}"

mcp_call() {
  local id="$1"
  local name="$2"
  local args="$3"
  curl -fsS "$SYNA_BASE_URL/mcp" \
    -H "Authorization: Bearer $SYNA_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":\"$id\",\"method\":\"tools/call\",\"params\":{\"name\":\"$name\",\"arguments\":$args}}"
}

save_args="$(python3 - <<PY
import json
print(json.dumps({
  "storageId": "$STORAGE_ID",
  "title": "$TITLE",
  "text": "$TEXT",
  "mimeType": "text/plain",
  "visibility": "personal",
  "source": "mcp-script-example",
  "sourceUrl": "https://example.invalid/synamcps/mcp-knowledge-roundtrip"
}))
PY
)"
save_response="$(mcp_call save knowledge_save "$save_args")"
echo "$save_response"

doc_id="$(python3 -c 'import json,sys
r=json.load(sys.stdin)
text=r["result"]["content"][0]["text"]
print(json.loads(text)["docId"])' <<<"$save_response")"

search_args="$(python3 - <<PY
import json
print(json.dumps({"query": "roundtrip", "storageId": "$STORAGE_ID", "limit": 5}))
PY
)"
mcp_call search knowledge_search "$search_args"
echo

get_args="$(python3 - <<PY
import json
print(json.dumps({"docId": "$doc_id"}))
PY
)"
mcp_call get knowledge_get "$get_args"
echo

if [[ "${DELETE_DOC:-0}" == "1" ]]; then
  delete_args="$(python3 - <<PY
import json
print(json.dumps({"docId": "$doc_id"}))
PY
)"
  mcp_call delete knowledge_delete "$delete_args"
  echo
fi
