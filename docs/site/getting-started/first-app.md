# Deploy Your First App

This walkthrough runs PMQ on loopback in explicit development mode, deploys the bundled `hello-world` example, exercises the document API, and cleans up. It needs no proxy, DNS, cloud credentials, or provider keys. For local HTTPS with `pmq.dev` certificates instead, see “Local HTTPS with pmq.dev” in the [README](https://github.com/pedro-stanaka/pmq/blob/main/README.md).

## 1. Generate a deployment token

```sh
export PMQ_DEPLOY_TOKEN="$(openssl rand -hex 32)"
```

The token authorizes deployment and management API calls. Prefer the environment variable over `--token`, which can appear in process listings.

## 2. Start a development server

If you built from source: `make build` and use `./bin/pmq` below.

```sh
pmq serve --dev --listen 127.0.0.1:8080 --domain apps.localhost --data-dir ./data
```

`--dev` is disabled by default, permits a fixed development identity, and requires a literal loopback listen address. The deploy token is still required. Production mode instead requires a reverse proxy and trusted-proxy configuration — see [Production & Recovery](/guides/production).

## 3. Deploy hello-world

From the repository root (or any directory containing a built app with an `index.html`):

```sh
pmq deploy ./examples/hello-world --name hello-world --server http://127.0.0.1:8080
pmq list --server http://127.0.0.1:8080
pmq status hello-world --server http://127.0.0.1:8080
```

No CLI at hand? Open the PMQ domain itself (`http://apps.localhost:8080`) for the built-in web console: name the app, drop a `.zip` with a root `index.html`, paste the token, and it deploys — the same request the CLI makes.

Deploying an existing name replaces its entire active set of files with the new directory — files omitted from the upload are no longer part of the app. Persistent documents are preserved; previous releases stay on disk for rollback.

## 4. Open the app

Visit `http://hello-world.apps.localhost:8080` if your resolver maps `localhost` subdomains to loopback; otherwise curl with a Host header:

```sh
curl -H 'Host: hello-world.apps.localhost' http://127.0.0.1:8080/
```

The example demonstrates documents and object storage: add a note, upload a file, download it via the listed link, delete it. Files are private to the app hostname and survive redeployments.

## 5. Exercise the document API

Browser writes automatically send an `Origin` header; curl must supply one that matches the host:

```sh
curl -H 'Host: hello-world.apps.localhost:8080' \
  -H 'Origin: http://hello-world.apps.localhost:8080' -H 'Content-Type: application/json' \
  -d '{"title":"Practice German","done":false}' http://127.0.0.1:8080/_pmq/db/tasks
curl -H 'Host: hello-world.apps.localhost:8080' http://127.0.0.1:8080/_pmq/db/tasks
```

The full contract — documents, object storage, the AI gateway, and admin endpoints — is in the [HTTP API reference](/reference/api).

## 6. Roll back and clean up

Redeploy a change, then return to the previous release:

```sh
pmq deploy ./examples/hello-world --name hello-world --server http://127.0.0.1:8080
pmq rollback hello-world --server http://127.0.0.1:8080
pmq prune hello-world --keep 5 --server http://127.0.0.1:8080   # optional
```

When you are done, stop the server and delete the throwaway data directory (`./data` in this walkthrough). Nothing else persists.

## Troubleshooting

- **401 responses** — the token passed to the CLI does not match `PMQ_DEPLOY_TOKEN` on the server, or it is missing.
- **Origin/CSRF errors on writes** — the `Origin` header must match the request Host (and use HTTPS in production).
- **Deploy rejected: no index.html** — deploy a *built* directory containing `index.html` at its root.
- **Hostname not resolving** — `/etc/hosts` does not support wildcards; add each app name, or use the `Host` header as shown above.
- **Nothing served after rollback** — releases are immutable; `pmq releases APP` shows which release is active.
