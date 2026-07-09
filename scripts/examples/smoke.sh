#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
EXAMPLES_DIR="$ROOT_DIR/scripts/examples"

for script in "$EXAMPLES_DIR"/*.sh; do
  [ "$(basename "$script")" = "smoke.sh" ] && continue
  bash -n "$script"
  "$script" --help >/dev/null
done

echo "example smoke check passed"
