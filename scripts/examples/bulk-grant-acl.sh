#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Grant one ACL role to many subject keys.

Required rights:
  platform_admin or acl.manage on STORAGE_ID.

Environment:
  SYNA_BASE_URL   Base URL, for example http://localhost:8080
  SYNA_TOKEN      Bearer token for the admin principal
  STORAGE_ID      Storage id to update
  ROLE            ACL role: storage_owner, storage_admin, storage_writer, storage_reader
  SUBJECTS_FILE   Newline-delimited subject keys, for example user:alice or group:eng

Example:
  printf 'group:eng\nuser:alice\n' > /tmp/subjects.txt
  SYNA_BASE_URL=http://localhost:8080 SYNA_TOKEN="$ADMIN_TOKEN" STORAGE_ID=default \
    ROLE=storage_reader SUBJECTS_FILE=/tmp/subjects.txt scripts/examples/bulk-grant-acl.sh

Expected response:
  One JSON ACL grant result per subject.
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
require_env ROLE
require_env SUBJECTS_FILE

while IFS= read -r subject_key; do
  [[ -z "$subject_key" || "$subject_key" =~ ^# ]] && continue
  payload="$(python3 - <<PY
import json
print(json.dumps({"subjectKey": "$subject_key", "role": "$ROLE"}))
PY
)"
  echo "granting $ROLE to $subject_key on $STORAGE_ID" >&2
  curl -fsS -X PUT "$SYNA_BASE_URL/api/admin/storages/$STORAGE_ID/acl" \
    -H "Authorization: Bearer $SYNA_TOKEN" \
    -H "Content-Type: application/json" \
    -d "$payload"
  echo
done < "$SUBJECTS_FILE"
