# PMQ — Poor Man's Quick

**Build it. Drop it. Run it.**

PMQ hosts static applications and per-app JSON documents in one Go process. Deploy a built directory over HTTP and reach it at its own hostname. SQLite and release files live on local disk. PMQ does not run application backend code or provide user accounts, hard tenant isolation, object storage, or AI access.

## Quick start

Requires Go 1.27 or later. Build and run on loopback with an explicit development identity:

```sh
make build
export PMQ_DEPLOY_TOKEN="$(openssl rand -hex 32)"
./bin/pmq serve --dev --listen 127.0.0.1:8080 --domain apps.localhost --data-dir ./data
```

In another terminal (use the same token):

```sh
./bin/pmq deploy ./examples/hello-world --name hello-world --server http://127.0.0.1:8080
./bin/pmq list --server http://127.0.0.1:8080
curl -H 'Host: hello-world.apps.localhost' http://127.0.0.1:8080/
```

Visit `http://hello-world.apps.localhost:8080` if your resolver maps localhost subdomains to loopback. `--dev` is disabled by default, permits a fixed development identity, and requires a literal loopback listen address. The deploy token is still required. Prefer the environment variable to `--token`, which can appear in process listings. A CLI deployment URL uses the server URL's scheme and port; if administrative ingress differs from application ingress, use your actual public app URL instead.

## Local HTTPS with pmq.dev

With [mkcert](https://github.com/FiloSottile/mkcert) installed, run:

```sh
./scripts/setup-local.sh                  # adds pmq.dev and hello-world.pmq.dev
# For additional apps: ./scripts/setup-local.sh hello-world writing-buddy
export PMQ_DEPLOY_TOKEN="$(openssl rand -hex 32)"
make build
./bin/pmq serve --dev --listen 127.0.0.1:8443 --domain pmq.dev \
  --tls-cert .local/certs/pmq.dev.pem --tls-key .local/certs/pmq.dev-key.pem
```

In another terminal with the same token:

```sh
./bin/pmq deploy ./examples/hello-world --name hello-world --server https://pmq.dev:8443
```

Open `https://hello-world.pmq.dev:8443`. The script installs mkcert's local CA into the trust store and may request a password, generates a certificate for `pmq.dev`, `*.pmq.dev`, `localhost`, and `127.0.0.1`, and appends loopback entries to `/etc/hosts` (using sudo when needed). It refuses conflicting entries and can be rerun. `/etc/hosts` does not support wildcard names: rerun the script with each additional application name. The private key and certificate are stored in ignored `.local/certs/`; do not share the key or mkcert's CA key. To undo setup, remove the lines marked `# PMQ local development` from `/etc/hosts`, delete `.local/certs/`, and run `mkcert -uninstall` if you no longer need its local CA for anything else. A browser may need a restart to pick up trust changes.

Built-in TLS is available only with `--dev` on a literal loopback address; it does not authenticate users and is not a production TLS option. The deployment token is still required. `.dev` uses HTTPS-only browser policies, so use the HTTPS URL above.

Create and list documents using the app origin. Browser writes automatically send an Origin; curl must supply one:

```sh
curl -H 'Host: hello-world.apps.localhost:8080' \
  -H 'Origin: http://hello-world.apps.localhost:8080' -H 'Content-Type: application/json' \
  -d '{"title":"Practice German","done":false}' http://127.0.0.1:8080/_pmq/db/tasks
curl -H 'Host: hello-world.apps.localhost:8080' http://127.0.0.1:8080/_pmq/db/tasks
```

See [API](docs/api.md) and [development](docs/development.md) for the complete contract.

## Production and proxy contract

Run behind an HTTPS reverse proxy that authenticates **every** application request. Set `PMQ_DOMAIN`, a long random `PMQ_DEPLOY_TOKEN`, and `PMQ_TRUSTED_PROXIES` to the exact source IPs or CIDRs of your proxy as observed by PMQ. `PMQ_IDENTITY_HEADER` defaults to `X-PMQ-User`. The proxy must remove all client-supplied instances of that header and set exactly one nonempty identity after successful authentication. It must preserve the application Host and original HTTPS Origin, and must not let unauthenticated traffic reach PMQ through a trusted source. PMQ checks the TCP peer, never `X-Forwarded-For`. Restrict direct network access to the server and proxy; a trusted source that forwards unfiltered traffic can forge identities. Do not trust all private addresses. Production startup fails without a token and configured trusted proxy; application static files and APIs require identity. The health endpoint is unauthenticated.

Writes require an Origin matching the Host, with HTTPS in production. Configure the proxy to route `/_pmq/admin/*` only to authorized operators as an additional restriction; those endpoints independently require the bearer deployment token and do not use the user identity. Use TLS for remote deployments and protect the token. Authentication establishes a shared trust boundary, not per-user authorization: authenticated users of an app can read and change all its documents.

For Docker, copy `.env.example` to `.env`, replace all values, especially the trusted address with the proxy source actually visible **inside the container**, then run `docker compose up -d --build`. The port is published on host loopback only. A host proxy may appear at a Docker gateway rather than `127.0.0.1`; verify this for your network. Trusting that gateway can also trust other host processes able to connect to the published port, so restrict access accordingly or use a dedicated proxy-to-PMQ network. Compose uses production mode, so a host proxy is required. No proxy service is included.

## Architecture and operations

A validated hostname selects an app; its active immutable release serves files, while a separate SQLite database stores documents. Deployments stage and validate archives then atomically replace a release pointer. See [architecture](docs/architecture.md). Data layout version 1 stores releases under `data/apps/` and databases under `data/state/`. Back up the **entire data directory**, including SQLite WAL files. Stop PMQ for a consistent filesystem backup, or use a SQLite-aware online backup plus a coordinated release snapshot. Test restores. Releases are retained indefinitely; there is no rollback, deletion, or garbage collection command yet.

This is a single-node system for a trusted environment, not a security sandbox. App JavaScript runs in users' browsers; all users of an app share its data. Hostname isolation relies on DNS and browser origin rules. Back up data, restrict deployment access, and keep the proxy and host patched. Reserved storage and AI routes currently return 404. Future milestones may add object storage, an AI gateway, rollback, and deployment management; OAuth, account registration, server-side code execution, distributed operation, and arbitrary SQL are not part of this slice.
