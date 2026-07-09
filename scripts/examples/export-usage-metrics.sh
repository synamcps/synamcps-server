#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Export usage summary and series JSON from the REST admin API.

Required rights:
  platform_admin or token visibility for the requested filters.

Environment:
  SYNA_BASE_URL   Base URL, for example http://localhost:8080
  SYNA_TOKEN      Bearer token for the admin principal
  OUT_DIR         Output directory, default: ./usage-export
  QUERY           Optional query string, for example 'tokenId=tok_123&from=2026-07-01T00:00:00Z'

Example:
  SYNA_BASE_URL=http://localhost:8080 SYNA_TOKEN="$ADMIN_TOKEN" OUT_DIR=/tmp/usage \
    scripts/examples/export-usage-metrics.sh

Expected output:
  $OUT_DIR/usage-summary.json
  $OUT_DIR/usage-series.json
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

OUT_DIR="${OUT_DIR:-./usage-export}"
QUERY="${QUERY:-}"
suffix=""
if [[ -n "$QUERY" ]]; then
  suffix="?$QUERY"
fi

mkdir -p "$OUT_DIR"

curl -fsS "$SYNA_BASE_URL/api/admin/usage/summary$suffix" \
  -H "Authorization: Bearer $SYNA_TOKEN" \
  -o "$OUT_DIR/usage-summary.json"

curl -fsS "$SYNA_BASE_URL/api/admin/usage/series$suffix" \
  -H "Authorization: Bearer $SYNA_TOKEN" \
  -o "$OUT_DIR/usage-series.json"

echo "wrote $OUT_DIR/usage-summary.json"
echo "wrote $OUT_DIR/usage-series.json"
