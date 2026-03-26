#!/usr/bin/env bash
set -euo pipefail

KEY="${OPENAI_API_KEY:-}"
if [[ -z "$KEY" ]]; then
  echo "OPENAI_API_KEY is required" >&2
  exit 1
fi

ENV_FILE="${1:-.env}"
LOG_FILE="$(mktemp)"

cleanup() {
  rm -f "$ENV_FILE" "$LOG_FILE"
}
trap cleanup EXIT

printf 'OPENAI_API_KEY=%s\n' "$KEY" > "$ENV_FILE"
unset OPENAI_API_KEY

go run ./cmd/mcp-smoke-agent agent \
  --env-file "$ENV_FILE" \
  --server "go run ./cmd/mcp-test-server" \
  --prompt "add 41 and 1" \
  2>&1 | tee "$LOG_FILE"

grep -Fq 'tool> add {"a":41,"b":1}' "$LOG_FILE"
grep -Eq '\b42\b' "$LOG_FILE"
