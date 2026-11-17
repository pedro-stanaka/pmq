#!/usr/bin/env bash
set -euo pipefail

skill_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd "$skill_dir/../.." && pwd)"
tmp="$(mktemp -d)"
token="pmq-skill-acceptance-token"
app="skill-acceptance"
app_host="$app.apps.localhost"

cleanup() {
  if [[ -n "${server_pid:-}" ]]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  python3 -c 'import shutil,sys; shutil.rmtree(sys.argv[1], ignore_errors=True)' "$tmp"
}
trap cleanup EXIT

command -v go >/dev/null
command -v curl >/dev/null
command -v python3 >/dev/null

port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
base="http://127.0.0.1:$port"
app_url="$base"
origin="http://$app_host"
app_data="$skill_dir/tests/acceptance/independent-app"

go build -o "$tmp/pmq" ./cmd/pmq
PMQ_DEPLOY_TOKEN="$token" "$tmp/pmq" serve --dev --listen "127.0.0.1:$port" --domain apps.localhost --data-dir "$tmp/data" >"$tmp/server.log" 2>&1 &
server_pid=$!

ready=false
for _ in $(seq 1 40); do
  if curl --silent --fail "$base/_pmq/health" >/dev/null; then
    ready=true
    break
  fi
  sleep 0.25
done
if [[ "$ready" != true ]]; then
  cat "$tmp/server.log" >&2
  echo "PMQ did not become ready" >&2
  exit 1
fi

PMQ_DEPLOY_TOKEN="$token" "$tmp/pmq" deploy "$app_data" --name "$app" --server "$base"

curl --silent --show-error --fail -H "Host: $app_host" "$app_url/" >"$tmp/page.html"
grep -q 'Independent PMQ Contacts' "$tmp/page.html"
# PMQ should return index.html on a direct nested SPA-style route too.
curl --silent --show-error --fail -H "Host: $app_host" "$app_url/contacts" >"$tmp/nested.html"
grep -q 'Independent PMQ Contacts' "$tmp/nested.html"

post_code="$(curl --silent --show-error -o "$tmp/response.json" -w '%{http_code}' \
  -H "Host: $app_host" -H "Origin: $origin" -H 'Content-Type: application/json' \
  --data '{"name":"Avery Example","email":"avery@example.test"}' "$app_url/_pmq/db/contacts")"
[[ "$post_code" == 201 ]]
id="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <"$tmp/response.json")"

curl --silent --show-error --fail -H "Host: $app_host" "$app_url/_pmq/db/contacts/$id" >"$tmp/response.json"
python3 -c 'import json,sys; assert json.load(sys.stdin)["data"]["name"] == "Avery Example"' <"$tmp/response.json"
curl --silent --show-error --fail -H "Host: $app_host" "$app_url/_pmq/db/contacts" >"$tmp/response.json"
python3 -c 'import json,sys; assert len(json.load(sys.stdin)["documents"]) == 1' <"$tmp/response.json"

put_code="$(curl --silent --show-error -o "$tmp/response.json" -w '%{http_code}' \
  -X PUT -H "Host: $app_host" -H "Origin: $origin" -H 'Content-Type: application/json' \
  --data '{"name":"Avery Updated","email":"avery@example.test"}' "$app_url/_pmq/db/contacts/$id")"
[[ "$put_code" == 200 ]]
delete_code="$(curl --silent --show-error -o /dev/null -w '%{http_code}' \
  -X DELETE -H "Host: $app_host" -H "Origin: $origin" "$app_url/_pmq/db/contacts/$id")"
[[ "$delete_code" == 204 ]]
missing_code="$(curl --silent --show-error -o /dev/null -w '%{http_code}' -H "Host: $app_host" "$app_url/_pmq/db/contacts/$id")"
[[ "$missing_code" == 404 ]]

put_object_code="$(curl --silent --show-error -o /dev/null -w '%{http_code}' \
  -X PUT -H "Host: $app_host" -H "Origin: $origin" -H 'Content-Type: text/plain' \
  --data-binary 'storage survives too' "$app_url/_pmq/storage/acceptance.txt")"
[[ "$put_object_code" == 201 ]]
curl --silent --show-error --fail -H "Host: $app_host" "$app_url/_pmq/storage/acceptance.txt" >"$tmp/object.txt"
[[ "$(cat "$tmp/object.txt")" == 'storage survives too' ]]
curl --silent --show-error --fail -H "Host: $app_host" "$app_url/_pmq/storage" >"$tmp/objects.json"
python3 -c 'import json,sys; assert any(o["key"] == "acceptance.txt" for o in json.load(sys.stdin)["objects"])' <"$tmp/objects.json"

persistent_code="$(curl --silent --show-error -o "$tmp/response.json" -w '%{http_code}' \
  -H "Host: $app_host" -H "Origin: $origin" -H 'Content-Type: application/json' \
  --data '{"name":"Survives redeploy","email":"persist@example.test"}' "$app_url/_pmq/db/contacts")"
[[ "$persistent_code" == 201 ]]
persistent_id="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <"$tmp/response.json")"
PMQ_DEPLOY_TOKEN="$token" "$tmp/pmq" deploy "$app_data" --name "$app" --server "$base" >/dev/null
curl --silent --show-error --fail -H "Host: $app_host" "$app_url/_pmq/db/contacts/$persistent_id" >"$tmp/response.json"
python3 -c 'import json,sys; assert json.load(sys.stdin)["data"]["name"] == "Survives redeploy"' <"$tmp/response.json"
curl --silent --show-error --fail -H "Host: $app_host" "$app_url/_pmq/storage/acceptance.txt" >"$tmp/object.txt"
[[ "$(cat "$tmp/object.txt")" == 'storage survives too' ]]

delete_object_code="$(curl --silent --show-error -o /dev/null -w '%{http_code}' \
  -X DELETE -H "Host: $app_host" -H "Origin: $origin" "$app_url/_pmq/storage/acceptance.txt")"
[[ "$delete_object_code" == 204 ]]

echo "PASS: independent app deployed; SPA fallback, document CRUD, and object storage work."
