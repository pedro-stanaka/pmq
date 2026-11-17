#!/usr/bin/env bash
set -euo pipefail

# Run an isolated S3-compatible endpoint; no cloud account or credentials are used.
port="${PMQ_TEST_MOTO_PORT:-37892}"
moto_server -H 127.0.0.1 -p "$port" >"${TMPDIR:-/tmp}/pmq-moto-$$.log" 2>&1 &
server_pid=$!
trap 'kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; rm -f "${TMPDIR:-/tmp}/pmq-moto-$$.log"' EXIT

for attempt in {1..50}; do
  if curl --silent --fail "http://127.0.0.1:$port/" >/dev/null; then
    break
  fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    cat "${TMPDIR:-/tmp}/pmq-moto-$$.log" >&2
    exit 1
  fi
  sleep 0.1
done
curl --silent --show-error --fail -X PUT "http://127.0.0.1:$port/pmq-test-objects" >/dev/null
PMQ_TEST_S3_ENDPOINT="127.0.0.1:$port" \
PMQ_TEST_S3_REGION=us-east-1 \
PMQ_TEST_S3_BUCKET=pmq-test-objects \
PMQ_TEST_S3_ACCESS_KEY=pmq-test \
PMQ_TEST_S3_SECRET_KEY=pmq-test-secret \
  go test ./internal/storage -run '^TestStorageContract$' -count=1 -v
