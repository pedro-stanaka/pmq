#!/bin/sh
# Smoke-check the deployed Caddy/PMQ boundary. Run from a client whose source
# IP is allowlisted for the private operator hostname.
set -eu

: "${PMQ_DOMAIN:=slopper.tanaka.dev.br}"
: "${PMQ_SMOKE_APP:=hello-world}"
: "${PMQ_ADMIN_HOST:=pmq-admin.${PMQ_DOMAIN}}"
: "${PMQ_APP_HOST:=${PMQ_SMOKE_APP}.${PMQ_DOMAIN}}"
: "${PMQ_PROBE_IP:=127.0.0.1}"

status() {
  host=$1 path=$2
  curl --silent --show-error --insecure --output /dev/null --write-out '%{http_code}' \
    --resolve "${host}:443:${PMQ_PROBE_IP}" "https://${host}${path}"
}
expect() {
  host=$1 path=$2 expected=$3 actual
  actual=$(status "$host" "$path")
  if [ "$actual" != "$expected" ]; then
    echo "${host}${path}: expected HTTP ${expected}, got ${actual}" >&2
    exit 1
  fi
  echo "${host}${path}: HTTP ${actual} (expected)"
}

# App host reaches its app authentication boundary; identity-free requests
# must be rejected or redirected to the configured SSO login.
code=$(status "$PMQ_APP_HOST" "/")
case "$code" in
  401|403|302|303|307|308) echo "app SSO boundary: HTTP ${code} (expected)" ;;
  *) echo "app SSO boundary: expected 401/403 or login redirect, got HTTP ${code}" >&2; exit 1 ;;
esac
# The regular wildcard app origin must never proxy administrative routes.
expect "$PMQ_APP_HOST" "/_pmq/admin/apps" 404
# A hostname outside the one-label app wildcard must not be routed to PMQ.
# Depending on the listener's automatic-HTTPS behavior Caddy may return 404,
# 421, or reject the TLS handshake before an HTTP status exists.
code=$(status "not-an-app.invalid" "/" 2>/dev/null || printf '000')
case "$code" in
  000|400|403|404|421) echo "unmatched host: denied by Caddy (HTTP ${code})" ;;
  *) echo "unmatched host: expected denial, got HTTP ${code}" >&2; exit 1 ;;
esac
# From an allowed operator source, the route reaches PMQ and is still gated by
# its independent bearer token (intentionally omitted here).
expect "$PMQ_ADMIN_HOST" "/_pmq/admin/apps" 401
