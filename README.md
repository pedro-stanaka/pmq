# PMQ — Poor Man's Quick

**Build it. Drop it. Run it.**

PMQ hosts static applications, per-app JSON documents, object storage, and an optional OpenAI-compatible AI gateway in one Go process. Deploy a built directory over HTTP and reach it at its own hostname. SQLite and release files live on local disk. PMQ does not run application backend code or provide user accounts or hard tenant isolation.

**PMQ is a full-trust system. Deploy it only when you trust every user who can access PMQ and its applications.** It is designed for a trusted group, not mutually untrusted users. Authentication restricts access to that group; it does not provide per-user data isolation.

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
./bin/pmq status hello-world --server http://127.0.0.1:8080
./bin/pmq releases hello-world --server http://127.0.0.1:8080
./bin/pmq rollback hello-world --server http://127.0.0.1:8080
curl -H 'Host: hello-world.apps.localhost' http://127.0.0.1:8080/
```

Deploying again with the same `--name` replaces the app’s complete active set of files with the new directory: files omitted from the new deployment are no longer part of the active app. This happens without an overwrite prompt. Its persistent documents are preserved; previous releases remain on disk.

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

The `hello-world` example demonstrates both document persistence and object storage: add a note, choose a file, upload it, then use the listed link to download it or the button to delete it. Files are private to the app hostname and survive frontend redeployments.

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

For an existing Caddy reverse proxy, wildcard TLS, SSO identity forwarding, host/bridge network examples, and a routing smoke test, see the [Caddy integration recipe](docs/proxy/caddy.md). The regular app hostname blocks `/_pmq/admin/*`; admin requests use a separate private operator hostname and still require the PMQ bearer token.

For Docker, copy `.env.example` to `.env`, replace all values, especially the trusted address with the proxy source actually visible **inside the container**, then run `docker compose up -d --build`. The port is published on host loopback only. A host proxy may appear at a Docker gateway rather than `127.0.0.1`; verify this for your network. Trusting that gateway can also trust other host processes able to connect to the published port, so restrict access accordingly or use a dedicated proxy-to-PMQ network. Compose uses production mode, so a host proxy is required. No proxy service is included.

## Object storage configuration

PMQ initializes a Thanos objstore bucket at startup. The default `PMQ_STORAGE_PROVIDER=filesystem` stores objects under `data/objects/` (or `PMQ_DATA_DIR/objects/`) and needs no cloud credentials. Apps can upload, retrieve, inspect, list, and delete their own objects through `/_pmq/storage/*`; see [API](docs/api.md). Requests use the trusted proxy identity and Origin checks used by document writes and are scoped by the validated application hostname. Bucket metrics are registered in the process Prometheus registry. `GET /_pmq/admin/metrics` requires the deployment bearer token and should also be restricted at the proxy.

To select an existing S3-compatible bucket, set `PMQ_STORAGE_PROVIDER=s3`, `PMQ_S3_ENDPOINT` (host and optional port, without a scheme), `PMQ_S3_REGION`, `PMQ_S3_BUCKET`, `PMQ_S3_ACCESS_KEY`, and `PMQ_S3_SECRET_KEY`. An optional `PMQ_S3_SESSION_TOKEN` supports temporary credentials. TLS is enabled by default; `PMQ_S3_INSECURE=true` disables it. Set `PMQ_S3_PATH_STYLE=true` for services requiring path-style addressing. Store credentials as environment secrets, never in browser code or committed files. Compose forwards these variables; see `.env.example`. PMQ checks bucket listing access at startup and fails if configuration or credentials are invalid. The credentials need list access to the `apps/` prefix and read, write, and delete access to objects under it; provision the bucket separately.

Each validated app hostname maps to its own `apps/<app>/` prefix. Object keys are limited to 160 bytes and stored as encoded flat names; empty or traversal segments and backslashes are rejected. This is soft separation, not protection against someone with bucket or filesystem access. Keep the local data directory writable only by PMQ and trusted administrators: the filesystem provider is intended for local disks, not shared or distributed filesystems. Upstream describes its filesystem provider as experimental; it is not designed for distributed workloads.

Storage uploads and downloads stream through bounded readers and writers; uploads are capped at 32 MiB. GET supports one byte range and conditional headers, while HEAD returns full object metadata. Filesystem uploads stage to a swap file and rename only after a successful copy. S3-compatible uploads expose the previous completed object until the replacement completes. A canceled/failed upload therefore does not advertise a partial object as complete; unfinished multipart cleanup and durability depend on the configured provider. Bucket operation metrics include range reads and streamed bytes; request cancellations have a separate low-cardinality counter.

Back up the local `objects/` directory with the rest of PMQ data. With S3, objects live outside that directory: configure bucket backups or versioning and test restoring them separately. Changing providers does not migrate existing objects. Keep the bucket and local PMQ data in a coordinated backup and restore plan.

## AI Chat Completions gateway

The optional `POST /_pmq/ai/chat/completions` endpoint forwards a documented, non-streaming text subset of the OpenAI Chat Completions contract. It uses the app's same origin and existing proxy authentication and write-Origin checks. Provider selection and credentials are server-side; never put provider keys in app JavaScript. The gateway defaults to disabled.

Configure exactly one provider before starting PMQ:

```sh
# OpenAI
PMQ_AI_PROVIDER=openai
OPENAI_API_KEY=...
PMQ_AI_MODELS='fast=gpt-4o-mini'

# Or OpenRouter
PMQ_AI_PROVIDER=openrouter
OPENROUTER_API_KEY=...
PMQ_AI_MODELS='fast=openai/gpt-4o-mini'

# Or Amazon Bedrock using the AWS SDK credential chain (IAM role, SSO/profile,
# environment credentials, or workload credentials)
PMQ_AI_PROVIDER=bedrock
AWS_REGION=us-east-1
PMQ_BEDROCK_MODELS='fast=openai.gpt-oss-20b-1:0'
```

`PMQ_AI_MODELS` is a required comma-separated allowlist of `alias=model-id` entries for OpenAI and OpenRouter. Apps send the alias in `model`; PMQ sends the configured model ID upstream. Optional `PMQ_OPENAI_BASE_URL` and `PMQ_OPENROUTER_BASE_URL` allow a trusted compatible endpoint; production URLs must use HTTPS. For Bedrock, `PMQ_BEDROCK_MODELS` is a required comma-separated allowlist of `alias=model-id` or `model-id` entries. Only listed models are sent upstream, and the AWS Chat Completions endpoint may support fewer models than Converse; consult the [Bedrock model card's supported API list](https://docs.aws.amazon.com/bedrock/latest/userguide/inference-chat-completions.html). PMQ uses the recommended regional `bedrock-runtime` endpoint and standard AWS credential resolution, with no static AWS secrets required. For deployment-specific endpoints, set `PMQ_BEDROCK_BASE_URL`. See the [official OpenAI Go SDK](https://github.com/openai/openai-go) for its maintained provider client and Bedrock SigV4 integration.

Example request from application JavaScript (same-origin relative URL):

```js
const response = await fetch('/_pmq/ai/chat/completions', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({model: 'fast', messages: [{role: 'user', content: 'Hello'}]}),
});
const completion = await response.json();
```

Supported fields are `model`, text-only `messages` (`system`, `user`, `assistant`), `temperature`, `top_p`, `max_tokens`, and `stop`. If omitted, `max_tokens` defaults to 1024; values above 32768 are rejected. Streaming, tools, multimodal content, and provider-specific extensions are not supported. Bodies are limited to 1 MiB, upstream responses to 2 MiB, and provider requests time out after 45 seconds. See [AI API details](docs/api.md#ai-chat-completions).

## Architecture and operations

A validated hostname selects an app; its active immutable release serves files, while a separate SQLite database stores documents. Deployments stage and validate archives then atomically replace a release pointer. `pmq status APP` and `pmq releases APP` inspect active and retained releases; `pmq rollback APP` activates the previous release, while `pmq rollback APP --release ID` selects a specific release. Releases are retained indefinitely by default. Pruning is explicit: `pmq prune APP --keep 5` keeps the five newest releases, the active release, and the newest other release as a rollback target when one exists. Do not prune a release you may need to roll back to. All management commands use the deployment bearer token. See [architecture](docs/architecture.md). Data layout version 1 stores releases under `data/apps/` and databases under `data/state/`. Back up the **entire data directory**, including SQLite WAL files. Stop PMQ for a consistent filesystem backup, or use a SQLite-aware online backup plus a coordinated release snapshot. Test restores.

This is a single-node system for a fully trusted group, not a security sandbox. App JavaScript runs in users' browsers; all users of an app share its data. Hostname isolation relies on DNS and browser origin rules. Back up data, restrict deployment access, and keep the proxy and host patched. OAuth, account registration, server-side code execution, distributed operation, and arbitrary SQL are not part of this slice.
