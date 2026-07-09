#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Report tokens that have never been used or have not been used recently.

Required rights:
  Token owner, storage token.manage, or platform_admin.

Environment:
  SYNA_BASE_URL   Base URL, for example http://localhost:8080
  SYNA_TOKEN      Bearer token for the admin principal
  DAYS_UNUSED     Optional age threshold, default: 30

Example:
  SYNA_BASE_URL=http://localhost:8080 SYNA_TOKEN="$ADMIN_TOKEN" DAYS_UNUSED=14 \
    scripts/examples/audit-unused-tokens.sh

Expected response:
  JSON array of matching token records.
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

DAYS_UNUSED="${DAYS_UNUSED:-30}"

curl -fsS "$SYNA_BASE_URL/api/admin/tokens" \
  -H "Authorization: Bearer $SYNA_TOKEN" |
python3 - "$DAYS_UNUSED" <<'PY'
import datetime as dt
import json
import sys

days = int(sys.argv[1])
payload = json.load(sys.stdin)
tokens = payload.get("tokens", payload if isinstance(payload, list) else [])
cutoff = dt.datetime.now(dt.timezone.utc) - dt.timedelta(days=days)

def parse_time(value):
    if not value:
        return None
    value = value.replace("Z", "+00:00")
    try:
        return dt.datetime.fromisoformat(value)
    except ValueError:
        return None

unused = []
for token in tokens:
    last_used = parse_time(token.get("lastUsedAt") or token.get("last_used_at"))
    if last_used is None or last_used < cutoff:
        unused.append(token)

print(json.dumps(unused, indent=2, sort_keys=True))
PY
