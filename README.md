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
See [observability](docs/observability.md) for the restricted Prometheus scrape endpoint, metric labels, scrape configuration, and sample queries.

## Browser SDK

The small TypeScript client in `sdk/` calls documents, storage, and the AI gateway through relative `/_pmq/` URLs. It uses `credentials: "same-origin"` so the browser's existing reverse-proxy session is sent; it never accepts or stores a deployment token, provider key, or identity. See the [SDK guide](docs/sdk.md) for a plain JavaScript example and a runnable Vite app. AI stream iteration follows the server's SSE contract and reads on demand, so consumers can pause or stop without buffering the whole response.

## Building apps with coding agents

The reusable [PMQ app-builder skill](skills/pmq-app-builder/SKILL.md) includes vanilla JavaScript and React/Vite/TypeScript starters, a concise API reference, a validation checklist, and an independent end-to-end app. Run its acceptance test with `./skills/pmq-app-builder/scripts/acceptance.sh`.

## Production and proxy contract

Run behind an HTTPS reverse proxy that authenticates **every** application request. Set `PMQ_DOMAIN`, a long random `PMQ_DEPLOY_TOKEN`, and `PMQ_TRUSTED_PROXIES` to the exact source IPs or CIDRs of your proxy as observed by PMQ. `PMQ_IDENTITY_HEADER` defaults to `X-PMQ-User`. The proxy must remove all client-supplied instances of that header and set exactly one nonempty identity after successful authentication. It must preserve the application Host and original HTTPS Origin, and must not let unauthenticated traffic reach PMQ through a trusted source. PMQ checks the TCP peer, never `X-Forwarded-For`. Restrict direct network access to the server and proxy; a trusted source that forwards unfiltered traffic can forge identities. Do not trust all private addresses. Production startup fails without a token and configured trusted proxy; application static files and APIs require identity. The health endpoint is unauthenticated.

Writes require an Origin matching the Host, with HTTPS in production. Configure the proxy to route `/_pmq/admin/*` only to authorized operators as an additional restriction; those endpoints independently require the bearer deployment token and do not use the user identity. Use TLS for remote deployments and protect the token. Authentication establishes a shared trust boundary, not per-user authorization: authenticated users of an app can read and change all its documents.

For an existing Caddy reverse proxy, wildcard TLS, SSO identity forwarding, host/bridge network examples, and a routing smoke test, see the [Caddy integration recipe](docs/proxy/caddy.md). The regular app hostname blocks `/_pmq/admin/*`; admin requests use a separate private operator hostname and still require the PMQ bearer token.

For Docker, copy `.env.example` to `.env`, replace all values, especially the trusted address with the proxy source actually visible **inside the container**, then run `docker compose up -d --build`. The port is published on host loopback only. A host proxy may appear at a Docker gateway rather than `127.0.0.1`; verify this for your network. Trusting that gateway can also trust other host processes able to connect to the published port, so restrict access accordingly or use a dedicated proxy-to-PMQ network. Compose uses production mode, so a host proxy is required. No proxy service is included.

For a shared Google login across PMQ app subdomains using the existing Caddy Security portal, see the [Google SSO integration](docs/proxy/google-sso.md). It documents cookie scope, identity-header handling, trusted-proxy configuration, logout/expiry, and a manual end-to-end test matrix.

## Object storage configuration

PMQ initializes a Thanos objstore bucket at startup. The default `PMQ_STORAGE_PROVIDER=filesystem` stores objects under `data/objects/` (or `PMQ_DATA_DIR/objects/`) and needs no cloud credentials. Apps can upload, retrieve, inspect, list, and delete their own objects through `/_pmq/storage/*`; see [API](docs/api.md). Requests use the trusted proxy identity and Origin checks used by document writes and are scoped by the validated application hostname. Bucket metrics are registered in the process Prometheus registry. `GET /_pmq/admin/metrics` requires the deployment bearer token and should also be restricted at the proxy.

To select an existing S3-compatible bucket, set `PMQ_STORAGE_PROVIDER=s3`, `PMQ_S3_ENDPOINT` (host and optional port, without a scheme), `PMQ_S3_REGION`, `PMQ_S3_BUCKET`, `PMQ_S3_ACCESS_KEY`, and `PMQ_S3_SECRET_KEY`. An optional `PMQ_S3_SESSION_TOKEN` supports temporary credentials. TLS is enabled by default; `PMQ_S3_INSECURE=true` disables it. Set `PMQ_S3_PATH_STYLE=true` for services requiring path-style addressing. Store credentials as environment secrets, never in browser code or committed files. Compose forwards these variables; see `.env.example`. PMQ checks bucket listing access at startup and fails if configuration or credentials are invalid. The credentials need list access to the `apps/` prefix and read, write, and delete access to objects under it; provision the bucket separately.

Each validated app hostname maps to its own `apps/<app>/` prefix. Object keys are limited to 160 bytes and stored as encoded flat names; empty or traversal segments and backslashes are rejected. This is soft separation, not protection against someone with bucket or filesystem access. Keep the local data directory writable only by PMQ and trusted administrators: the filesystem provider is intended for local disks, not shared or distributed filesystems. Upstream describes its filesystem provider as experimental; it is not designed for distributed workloads.

Storage uploads and downloads stream through bounded readers and writers; uploads are capped at 32 MiB. GET supports one byte range and conditional headers, while HEAD returns full object metadata. Filesystem uploads stage to a swap file and rename only after a successful copy. S3-compatible uploads expose the previous completed object until the replacement completes. A canceled/failed upload therefore does not advertise a partial object as complete; unfinished multipart cleanup and durability depend on the configured provider. Bucket operation metrics include range reads and streamed bytes; request cancellations have a separate low-cardinality counter.

Back up the local `objects/` directory with the rest of PMQ data. With S3, objects live outside that directory: configure bucket backups or versioning and test restoring them separately. Changing providers does not migrate existing objects. Keep the bucket and local PMQ data in a coordinated backup and restore plan.

## AI Chat Completions gateway

The optional `POST /_pmq/ai/chat/completions` endpoint implements a documented text subset of the OpenAI Chat Completions contract, including SSE streaming. It uses the app's same origin and existing proxy authentication and write-Origin checks. Provider selection and credentials are server-side; never put provider keys in app JavaScript. The gateway defaults to disabled.

Configure public aliases and a default before starting PMQ. Multiple providers can be configured together:

```sh
# Alias=provider|upstream-model-id
PMQ_AI_DEFAULT_MODEL=fast
PMQ_AI_MODELS='fast=openai|gpt-4o-mini,router=openrouter|openai/gpt-4o-mini'
OPENAI_API_KEY=...
OPENROUTER_API_KEY=...

# Optional Bedrock alias. Add `|stream` only when the selected Bedrock model
# card lists Chat Completions streaming support.
AWS_REGION=us-east-1
PMQ_AI_MODELS="$PMQ_AI_MODELS,aws=bedrock|openai.gpt-oss-20b-1:0"
```

`PMQ_AI_MODELS` is a comma-separated allowlist whose entries are `alias=provider|model-id` or `alias=provider|model-id|stream`. Supported providers are `openai`, `openrouter`, and `bedrock`; only Bedrock requires the `stream` capability marker, and streaming will be rejected for Bedrock aliases without it. `PMQ_AI_DEFAULT_MODEL` must name a configured alias. An app may choose another configured alias in the `model` field; an optional `provider` field must match that alias's configured provider. PMQ never accepts caller-supplied URLs, credentials, or upstream model IDs. Provider keys (`OPENAI_API_KEY`, `OPENROUTER_API_KEY`) and AWS credentials stay server-side. OpenAI and OpenRouter may both be configured. Optional `PMQ_OPENAI_BASE_URL` and `PMQ_OPENROUTER_BASE_URL` set trusted compatible endpoints; production URLs must use HTTPS. Bedrock uses the recommended regional `bedrock-runtime` endpoint and AWS SDK credential chain (IAM roles, SSO/profile, environment, or workload credentials). For Docker, use a container role or mount the AWS config/credentials files read-only when using a local profile. Select models whose AWS model cards list Chat Completions support, and add `|stream` only where streaming is listed; model discovery is not attempted through Bedrock Runtime `GET /models`. See [Bedrock Chat Completions](https://docs.aws.amazon.com/bedrock/latest/userguide/inference-chat-completions.html) and the [official OpenAI Go SDK](https://github.com/openai/openai-go).

Limits default to 120 requests/minute and 16 active requests globally, 30 requests/minute and 4 active requests per app, 1 MiB request bodies, 2 MiB provider responses/streams, 1024 output tokens by default (maximum 32768), and a 45 second timeout. Tune with `PMQ_AI_GLOBAL_RPM`, `PMQ_AI_PER_APP_RPM`, `PMQ_AI_GLOBAL_CONCURRENCY`, `PMQ_AI_PER_APP_CONCURRENCY`, `PMQ_AI_MAX_REQUEST_BYTES`, `PMQ_AI_MAX_RESPONSE_BYTES`, `PMQ_AI_DEFAULT_OUTPUT_TOKENS`, `PMQ_AI_MAX_OUTPUT_TOKENS`, and `PMQ_AI_TIMEOUT`. Hard maxima are 8 MiB request bodies, 16 MiB provider responses, 32768 output tokens, and 10 minutes. The HTTP server write deadline follows the AI timeout with a 15 second response margin.

Optional operator-supplied prices and daily budgets:

```sh
PMQ_AI_PRICING='{"fast":{"input_usd_per_million":2.5,"output_usd_per_million":10,"provenance":"Provider price page checked 2026-11-17"}}'
PMQ_AI_GLOBAL_BUDGET_USD=25
PMQ_AI_PER_APP_BUDGET_USD=5
```

Budgets require a price and provenance entry for every configured alias. PMQ estimates input tokens conservatively as one token per UTF-8 byte plus four per message and reserves the configured maximum output tokens before dispatch. Provider-reported complete token usage reconciles the reservation; missing or partial usage remains unavailable and is never exposed as zero. Non-streaming responses include `X-PMQ-Usage-Status`, `X-PMQ-Cost-Status`, and, when priced, an estimated-cost header. Streams end with an `event: pmq-usage` metadata event; absent usage is marked `unavailable` and its estimated cost is labeled as a request estimate. These figures are estimates, not provider billing. Prices can differ by provider, region, route, and time, especially Bedrock and OpenRouter. Budgets are in-memory best-effort cutoffs that reset at UTC midnight and on PMQ restart; concurrent usage, price changes, provider rounding, and incomplete usage can exceed them. Rejections return 429 with a reason code and `Retry-After`; logs include only app/alias, rejection reason, usage status, and estimated cost, never prompts.

Example request from application JavaScript (same-origin relative URL):

```js
const response = await fetch('/_pmq/ai/chat/completions', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({messages: [{role: 'user', content: 'Hello'}]}), // uses default alias
});
const completion = await response.json();
```

Override the default with a configured alias: `JSON.stringify({model: 'router', messages: [...]})`. To stream chunks:

```js
const response = await fetch('/_pmq/ai/chat/completions', {
  method: 'POST', headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({model: 'fast', stream: true, messages: [{role: 'user', content: 'Hello'}]}),
});
if (!response.ok) throw new Error(await response.text());
const reader = response.body.getReader();
const decoder = new TextDecoder();
let pending = '';
while (true) {
  const {value, done} = await reader.read();
  if (done) break;
  pending += decoder.decode(value, {stream: true});
  const events = pending.split('\n\n');
  pending = events.pop();
  for (const event of events) {
    const dataLine = event.split('\n').find(line => line.startsWith('data: '));
    if (!dataLine) continue;
    const data = dataLine.slice(6);
    if (event.startsWith('event: error')) throw new Error(JSON.parse(data).error.message);
    if (data === '[DONE]') continue;
    const chunk = JSON.parse(data);
    document.querySelector('#answer').textContent += chunk.choices?.[0]?.delta?.content || '';
  }
}
```

Supported fields are `model`, optional matching `provider`, text-only `messages` (`system`, `user`, `assistant`), `temperature`, `top_p`, `max_tokens`, `stop`, and `stream`. If omitted, `max_tokens` defaults to 1024; values above 32768 are rejected. Tools, multimodal content, and provider-specific extensions are not supported. Bodies are limited to 1 MiB, upstream responses and streams to 2 MiB, and provider calls time out after 45 seconds. See [AI API details](docs/api.md#ai-chat-completions).

## Architecture and operations

A validated hostname selects an app; its active immutable release serves files, while a separate SQLite database stores documents. Deployments stage and validate archives then atomically replace a release pointer. `pmq status APP` and `pmq releases APP` inspect active and retained releases; `pmq rollback APP` activates the previous release, while `pmq rollback APP --release ID` selects a specific release. Releases are retained indefinitely by default. Pruning is explicit: `pmq prune APP --keep 5` keeps the five newest releases, the active release, and the newest other release as a rollback target when one exists. Do not prune a release you may need to roll back to. All management commands use the deployment bearer token. See [architecture](docs/architecture.md). Data layout version 1 stores releases under `data/apps/` and databases under `data/state/`. Back up the **entire data directory**, including SQLite WAL files. Stop PMQ for a consistent filesystem backup, or use a SQLite-aware online backup plus a coordinated release snapshot. Test restores.

This is a single-node system for a fully trusted group, not a security sandbox. App JavaScript runs in users' browsers; all users of an app share its data. Hostname isolation relies on DNS and browser origin rules. Back up data, restrict deployment access, and keep the proxy and host patched. OAuth, account registration, server-side code execution, distributed operation, and arbitrary SQL are not part of this slice.

For private LAN/tailnet ingress, split DNS, and reachability checks, see [private ingress](docs/proxy/private-ingress.md).

For the optional production Compose/Komodo recipe and offline backup/restore drill, see [production and recovery](docs/proxy/production.md).

Agents can use the [deployment and operations skill](skills/pmq-deploy/SKILL.md) for build, verification, rollback, and troubleshooting.

## One-command local smoke test

Run `./scripts/smoke-test.sh` from the repository root. It builds the vanilla example into a temporary output directory, deploys it to a disposable local PMQ HTTP server, exercises documents, local objects and mocked AI, redeploys, and rolls back. It also runs the deployment skill's CLI failure checks and the app-builder skill's independent example. Go, Bash, curl and Python 3 are required; no homelab, OAuth credentials, paid provider, or persistent user data are used. For production checks and backup verification, use the [production and recovery checklist](docs/proxy/production.md).
