#!/usr/bin/env bash
# Configure local DNS entries and a mkcert certificate for PMQ development.
set -euo pipefail

usage() {
  printf 'Usage: %s [--hosts-file PATH] [APP ...]\n' "$0"
  printf 'Adds pmq.dev and APP.pmq.dev hosts entries (default APP: hello-world).\n'
}
hosts_file=/etc/hosts
if [[ ${1:-} == --help || ${1:-} == -h ]]; then usage; exit 0; fi
if [[ ${1:-} == --hosts-file ]]; then
  [[ $# -ge 2 && -n $2 ]] || { usage >&2; exit 1; }
  hosts_file=$2
  shift 2
fi
if [[ $# -eq 0 ]]; then set -- hello-world; fi
command -v mkcert >/dev/null || { echo 'Install mkcert first: https://github.com/FiloSottile/mkcert#installation' >&2; exit 1; }
[[ -f $hosts_file ]] || { printf 'Hosts file does not exist: %s\n' "$hosts_file" >&2; exit 1; }

names=(pmq.dev)
for app in "$@"; do
  if [[ ${#app} -gt 63 || ! $app =~ ^[a-z0-9]+(-[a-z0-9]+)*$ || $app == www || $app == api || $app == admin || $app == pmq ]]; then
    printf 'Invalid or reserved app name: %s\n' "$app" >&2; exit 1
  fi
  names+=("$app.pmq.dev")
done

# Refuse conflicting entries instead of silently overriding a user's configuration.
missing=()
for name in "${names[@]}"; do
  result=$(awk -v name="$name" '
    { sub(/#.*/, ""); for (i=2; i<=NF; i++) if (tolower($i)==name) {
        if ($1 != "127.0.0.1") conflict=1; else found=1
      } }
    END { if (conflict) print "conflict"; else if (found) print "found"; else print "missing" }
  ' "$hosts_file")
  case $result in
    conflict) printf 'Conflicting hosts entry for %s in %s; resolve it first.\n' "$name" "$hosts_file" >&2; exit 1 ;;
    missing) missing+=("$name") ;;
  esac
done

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cert_dir="$repo_dir/.local/certs"
umask 077
mkdir -p "$cert_dir"
cert_tmp=$(mktemp "$cert_dir/.cert.XXXXXX")
key_tmp=$(mktemp "$cert_dir/.key.XXXXXX")
trap 'rm -f -- "$cert_tmp" "$key_tmp"' EXIT
printf 'Installing the mkcert local CA into this user/system trust store (may request a password).\n'
mkcert -install
mkcert -cert-file "$cert_tmp" -key-file "$key_tmp" pmq.dev '*.pmq.dev' localhost 127.0.0.1
chmod 600 "$key_tmp"
mv -f -- "$cert_tmp" "$cert_dir/pmq.dev.pem"
mv -f -- "$key_tmp" "$cert_dir/pmq.dev-key.pem"

if (( ${#missing[@]} )); then
  # Deduplicate names in this invocation; never replace or remove existing lines.
  declare -A added=()
  entries=''
  for name in "${missing[@]}"; do
    if [[ ! ${added[$name]+yes} ]]; then
      entries+="127.0.0.1 $name # PMQ local development"$'\n'
      added[$name]=1
    fi
  done
  printf 'Adding loopback entries to %s (may request a password).\n' "$hosts_file"
  if [[ -w $hosts_file ]]; then
    printf '\n%s' "$entries" >> "$hosts_file"
  else
    printf '\n%s' "$entries" | sudo tee -a "$hosts_file" >/dev/null
  fi
fi
printf '\nCertificate: %s\nPrivate key: %s\n' "$cert_dir/pmq.dev.pem" "$cert_dir/pmq.dev-key.pem"
printf 'Start PMQ with: ./bin/pmq serve --dev --listen 127.0.0.1:8443 --domain pmq.dev --tls-cert .local/certs/pmq.dev.pem --tls-key .local/certs/pmq.dev-key.pem\n'
printf 'Deploy with: ./bin/pmq deploy ./examples/hello-world --name hello-world --server https://pmq.dev:8443\n'
