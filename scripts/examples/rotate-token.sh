#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Rotate an existing access token through the REST admin API.

Required rights:
  Token owner or platform_admin.

Environment:
  SYNA_BASE_URL   Base URL, for example http://localhost:8080
  SYNA_TOKEN      Bearer token for the admin principal
  TOKEN_ID        Token id to rotate

Example:
  SYNA_BASE_URL=http://localhost:8080 SYNA_TOKEN="$ADMIN_TOKEN" TOKEN_ID=tok_123 \
    scripts/examples/rotate-token.sh

Expected response:
  JSON token metadata plus a one-time raw token secret, depending on server policy.
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
require_env TOKEN_ID

curl -fsS -X POST "$SYNA_BASE_URL/api/admin/tokens/$TOKEN_ID/rotate" \
  -H "Authorization: Bearer $SYNA_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
echo
