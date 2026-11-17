# CLI Reference

`pmq` is both the server and the deployment client. `pmq help` prints this overview; every command accepts `--help` for its flags. See also the [configuration reference](/reference/config) for environment variables.

## `pmq serve`

Runs the PMQ server.

| Flag | Environment default | Description |
|------|--------------------|-------------|
| `--listen` | `PMQ_LISTEN` (default `127.0.0.1:8080`) | Listen address. |
| `--data-dir` | `PMQ_DATA_DIR` (default `./data`) | Data directory for releases, databases, and objects. |
| `--domain` | `PMQ_DOMAIN` (default `apps.localhost`) | Application domain used for hostname routing. |
| `--dev` | `PMQ_DEV=true` | Explicit loopback-only development mode (fixed identity). Disabled by default. |
| `--tls-cert`, `--tls-key` | — | Development TLS; both required together, only with `--dev` on loopback. |
| `--token` | `PMQ_DEPLOY_TOKEN` | Deployment token. Prefer the environment variable; `--token` can appear in process listings. |
| `--identity-header` | `PMQ_IDENTITY_HEADER` (default `X-PMQ-User`) | Header your trusted proxy uses to convey identity. |
| `--trusted-proxies` | `PMQ_TRUSTED_PROXIES` | Comma-separated IPs or CIDRs of the proxy as observed by PMQ. Required in production. |

Production mode fails to start without a token and trusted-proxy configuration.

## `pmq deploy DIR --name NAME`

Uploads a built directory (must contain `index.html`) as a new release of `NAME`. Deploying an existing name replaces its entire active file set without a merge or overwrite prompt; persistent data is preserved.

## `pmq list`

Lists deployed apps.

## `pmq status APP` / `pmq releases APP`

`status` shows the app's active release; `releases` lists release IDs, creation times, and the active marker, newest first.

## `pmq rollback APP [--release ID]`

Activates the previous release, or a specific release ID. A missing or invalid ID leaves the active pointer unchanged.

## `pmq prune APP --keep N`

Explicitly removes releases outside the newest `N` (1–1000). The active release is always retained, plus the newest other release as a rollback target when one exists. Deployment never prunes implicitly.

## `pmq version` / `pmq help`

`version` prints the version, commit, and build date (release builds; development builds report `dev`). `help` prints the command overview.

## Client flags (deploy, list, status, releases, rollback, prune)

| Flag | Environment default | Description |
|------|--------------------|-------------|
| `--server` | `PMQ_SERVER` (default `http://127.0.0.1:8080`) | PMQ server URL. |
| `--token` | `PMQ_DEPLOY_TOKEN` | Deployment bearer token. |

All management commands require the token and return machine-readable errors on failure (e.g. HTTP 401 with a JSON error body).
