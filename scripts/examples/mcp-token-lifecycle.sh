#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Create, narrow, and revoke an access token via MCP admin tools.

Required rights:
  JWT/web principal with platform_admin, admin, or token.manage on STORAGE_ID.
  Access-token authentication cannot call admin_* MCP tools.

Environment:
  SYNA_BASE_URL   Base URL, for example http://localhost:8080
  SYNA_TOKEN      JWT/web bearer token for the admin principal
  STORAGE_ID      Storage to grant initially
  TOKEN_NAME      Optional token name, default: example-mcp-token
  KEEP_TOKEN      Optional. Set to 1 to skip revoke.

Example:
  SYNA_BASE_URL=http://localhost:8080 SYNA_TOKEN="$JWT" STORAGE_ID=default \
    scripts/examples/mcp-token-lifecycle.sh

Expected create result:
  {"content":[{"type":"text","text":"{\"tokenId\":\"...\",\"rawToken\":\"...\"...}"}]}
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

TOKEN_NAME="${TOKEN_NAME:-example-mcp-token}"

mcp_call() {
  local id="$1"
  local name="$2"
  local args="$3"
  curl -fsS "$SYNA_BASE_URL/mcp" \
    -H "Authorization: Bearer $SYNA_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":\"$id\",\"method\":\"tools/call\",\"params\":{\"name\":\"$name\",\"arguments\":$args}}"
}

create_args="$(python3 - <<PY
import json
print(json.dumps({"name": "$TOKEN_NAME", "mode": "read_write", "storageIds": ["$STORAGE_ID"]}))
PY
)"
create_response="$(mcp_call create admin_token_create "$create_args")"
echo "$create_response"

token_id="$(python3 -c 'import json,sys
r=json.load(sys.stdin)
text=r["result"]["content"][0]["text"]
print(json.loads(text)["tokenId"])' <<<"$create_response")"

scope_args="$(python3 - <<PY
import json
print(json.dumps({
  "tokenId": "$token_id",
  "storageScopes": [{"storageId": "$STORAGE_ID", "scopes": ["knowledge.read", "knowledge.search"]}],
  "mcpServers": []
}))
PY
)"
mcp_call scope admin_token_update_scopes "$scope_args"
echo

if [[ "${KEEP_TOKEN:-0}" == "1" ]]; then
  echo "kept token: $token_id"
  exit 0
fi

revoke_args="$(python3 - <<PY
import json
print(json.dumps({"tokenId": "$token_id"}))
PY
)"
mcp_call revoke admin_token_revoke "$revoke_args"
echo
