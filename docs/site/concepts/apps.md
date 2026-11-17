# Static Apps & Hostname Routing

PMQ hosts **built static frontends**: a directory with `index.html` at its root and any assets beside it. It does not execute application backend code. Apps get their persistence, files, and AI access from the PMQ API under `/_pmq/*` on their own origin, so no app needs to know the PMQ server's internal hostname.

## Hostname routing

Each application is selected from the **validated request Host**. With `PMQ_DOMAIN=apps.example.com`, an app named `hello-world` is served at `hello-world.app.example.com`:

```text
https://hello-world.apps.example.com/            → static files of the active release
https://hello-world.apps.example.com/_pmq/db/... → that app's documents
https://hello-world.apps.example.com/_pmq/storage/... → that app's objects
https://hello-world.apps.example.com/_pmq/ai/... → AI gateway (same origin)
```

The application namespace is derived from server-side routing, never from a client-provided app ID. App names use lowercase letters, digits, and interior hyphens (up to 63 characters); `admin`, `api`, `www`, and `pmq` are reserved.

## Releases

Deployments are atomic and versioned:

- A deployment uploads and validates a complete directory, then atomically replaces the app's active release pointer. No partially uploaded release is ever served.
- Deploying an existing name **replaces the entire active set** of files — there is no merge and no overwrite confirmation. Files omitted from the new upload are gone from the active app.
- Previous releases are retained on disk (indefinitely by default), so `pmq rollback` is always available. `pmq prune APP --keep N` removes old releases explicitly.
- Persistent data (documents, objects) lives **outside** release directories and survives every deployment.

## Deploying from CI or agents

`pmq deploy` talks to the deployment API (`POST /_pmq/admin/deploy/{name}`) with the bearer deployment token; it accepts a built directory or a `.zip` archive on disk. The token authorizes management — keep it out of browser code and repositories.

## Deploying from a browser

The bare PMQ domain serves a built-in console: name an app, drop a `.zip`, paste the deploy token, and the upload goes to the same endpoint. The token lives only in that page for the session and is never stored server-side. Operators can also reach the console at `/_pmq/admin/` on the admin origin.

See the [CLI reference](/reference/cli) and [For AI Agents](/agents).
