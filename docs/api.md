# API

All app endpoints use the deployed app's hostname and authenticated identity. Responses are JSON except successful DELETE (204). Errors have `{"error":{"code":"...","message":"..."}}`. Collection names use lowercase letters, digits, and interior hyphens, up to 63 characters; reserved names include `admin`, `api`, `www`, and `pmq`. IDs are generated 32-character lowercase hex strings.

- `POST /_pmq/db/{collection}`: create from a JSON object (up to 1 MiB); returns 201 and document.
- `GET /_pmq/db/{collection}`: returns `{ "documents": [...], "limit": 50, "offset": 0 }`. `limit` is 1–100; `offset` is 0–1000000. Results sort by creation timestamp and ID.
- `GET /_pmq/db/{collection}/{id}`: return document or 404.
- `PUT /_pmq/db/{collection}/{id}`: replace its entire data object; return document or 404.
- `DELETE /_pmq/db/{collection}/{id}`: delete or return 404.

A document has `id`, `collection`, `data`, `createdAt`, and `updatedAt`. There is no filtering, SQL, or partial update. Mutating app requests require an Origin matching the request Host (HTTPS in production).

## AI Chat Completions

When `PMQ_AI_MODELS` and `PMQ_AI_DEFAULT_MODEL` are configured, `POST /_pmq/ai/chat/completions` accepts a text-only subset of Chat Completions:

```json
{"model":"fast","messages":[{"role":"user","content":"Hello"}],"max_tokens":256,"temperature":0.7}
```

Allowed fields: `model` (a configured alias; omitted selects the default), optional `provider` (must match the alias), `messages`, `temperature` (0–2), `top_p` (0–1), `max_tokens` (1–32768, default 1024), `stop`, and `stream`. Message roles are `system`, `user`, or `assistant`, and content must be nonempty text. The normalized non-streaming response includes the standard completion ID, object, created timestamp, public model alias, choices, and `usage` only when the provider reports complete, consistent token counts. Missing, partial, or inconsistent counts are omitted rather than represented as zero. Streaming returns `text/event-stream` with OpenAI-compatible `data: {chunk JSON}` events, a terminal `event: pmq-usage` accounting event, and `data: [DONE]`. Chunks expose `choices[].delta.content` and `finish_reason`; provider-specific fields are omitted. The accounting event contains `status: reported` with usage when complete counts arrived, otherwise `status: unavailable`; it may also include an estimated cost when operator pricing is configured. Errors before the first chunk use the common JSON error envelope. Errors after streaming starts use `event: error` with a generic error object. Default bounds are 1 MiB request bodies, 2 MiB upstream responses/streams, and a 45-second deadline; operators may tune these within hard maxima.

Server configuration uses comma-separated `PMQ_AI_MODELS=alias=provider|model-id[|stream]` entries. Providers are `openai`, `openrouter`, or `bedrock`; keys and AWS credentials are server-side. `PMQ_AI_DEFAULT_MODEL` must be one of the aliases. An optional request provider value is accepted only when it matches the selected alias; callers cannot choose upstream URLs, credentials, or model IDs. OpenAI/OpenRouter support streaming; Bedrock aliases support it only when explicitly marked `|stream`. Select Bedrock models whose current model cards support Chat Completions and streaming; PMQ does not call Bedrock Runtime `GET /models`. PMQ uses the regional Bedrock Runtime endpoint and AWS SDK credential chain. See the README for configuration and examples. No credential or prompt contents are logged.

Errors use the common `{"error":{"code":"...","message":"..."}}` contract: invalid requests and model/provider mismatches return 400, an oversized request 413, an unconfigured gateway 503, an unsupported streaming alias 400, an upstream timeout 504, and other upstream failures 502. Admission rejections return 429 with codes `ai_rate_limited`, `ai_concurrency_limited`, or `ai_budget_exceeded`, plus `Retry-After`; rejected requests do not reach a provider. The gateway defaults to 120 requests/minute and 16 active calls globally, and 30 requests/minute and 4 active calls per app. Limits, response size, output-token ceiling, and timeout can be set with the `PMQ_AI_*` variables described in the README.

Optional operator prices (`PMQ_AI_PRICING`) enable cost estimates and daily global/per-app budget cutoffs. Before dispatch, PMQ estimates input as one token per UTF-8 byte plus four per message and reserves the configured maximum output tokens. Complete provider usage reconciles this reservation. Missing or invalid usage remains unavailable; PMQ never substitutes zero. Responses expose `X-PMQ-Usage-Status` and `X-PMQ-Cost-Status`, plus estimated cost and configured pricing provenance when priced. Streams expose the final usage and estimate in `event: pmq-usage`. Prices are operator supplied and estimates are not billing records; rates can vary by provider, region, route, and time. Budgets are process-local best-effort cutoffs that reset at UTC midnight and on restart, so in-flight calls, incomplete usage, and provider billing differences can exceed them. Rejections and accounting logs exclude prompts and credentials. The route follows the same per-app hostname, proxy identity, and Origin rules as the document API. Application code should call it through the relative URL `/_pmq/ai/chat/completions`.

## Object storage

Objects are scoped to the application selected from the validated request Host. Keys are UTF-8 path-like names up to 160 bytes; empty segments, `.`/`..`, backslashes, control bytes, and NUL are rejected. Use percent encoding for reserved URL characters. Uploads are limited to 32 MiB. Successful PUT returns 201 with an empty body.

- `PUT /_pmq/storage/{key}`: create or replace the object. The request `Content-Type` must be syntactically valid; PMQ chooses the response type deterministically from the key extension (unknown extensions use `application/octet-stream`).
- `GET /_pmq/storage/{key}`: streams object bytes with `Content-Length`, `Last-Modified`, and the inferred `Content-Type`. A single `Range: bytes=start-end`, open-ended range, or suffix range returns 206 with `Content-Range`; unsatisfiable or unsupported multi-ranges return 416 and `Content-Range: bytes */{size}`.
- `HEAD /_pmq/storage/{key}`: returns the full-object metadata headers without a body; `Range` is ignored.
- `DELETE /_pmq/storage/{key}`: delete or return 404.
- `GET /_pmq/storage?prefix={prefix}&limit={n}&cursor={cursor}`: returns `{ "objects": [{"key":"...","size":123,"lastModified":"...","contentType":"..."}], "limit":100, "nextCursor":"..." }`. Limit defaults to 100 and must be 1–500. Pass `nextCursor` unchanged to get the next page; an empty cursor means there are no further results. Pages are ordered by raw key. Pagination is not a snapshot: writes and deletes during traversal can change later pages.

Unauthenticated requests fail. PUT and DELETE require a matching Origin (HTTPS in production), following the same trusted-proxy identity rules as document writes. A key from another app's namespace is not addressable through this API. Errors use the common JSON envelope, with 400 for invalid keys, 404 for missing objects, 413 for oversized uploads, and 500 for provider failures.

GET and HEAD support `If-Match`, `If-None-Match`, `If-Modified-Since`, and `If-Unmodified-Since`; `If-Range` may use an ETag or date. ETags are returned only when the configured provider supplies an ETag version. A mismatched `If-Range` causes a full 200 response. Upload and download bodies are copied as streams with a 32 MiB upload cap; PMQ does not buffer an entire object in memory. The objstore provider controls overwrite atomicity: filesystem writes a swap object and renames it after a successful copy, while S3-compatible PUT exposes the previous completed object until the replacement completes. Failed or canceled uploads do not become completed objects; backend cleanup and durability remain provider responsibilities.

Storage bucket metrics are registered in the process Prometheus registry with the fixed logical `bucket="pmq"` label. Operations, failures, duration, and transferred bytes, including `get_range`, are measured by the objstore wrapper. `pmq_storage_transfer_cancellations_total{operation="upload|download"}` counts request-context cancellations separately; canceled operations are not treated as provider failures. `GET /_pmq/admin/metrics` exposes the scrape only with the deployment bearer token; keep this administrative endpoint restricted at the reverse proxy too. Metric labels never include application names, object keys, paths, or identities.

Administrative API: `POST /_pmq/admin/deploy/{name}` accepts a gzip-compressed tar of regular files and directories, at most 32 MiB compressed and uncompressed, with root `index.html`. Deploying an existing name replaces its entire active set of static files with the new archive without an overwrite prompt or merge; files omitted from the archive are no longer part of the active app. The app’s persistent documents remain intact.

- `GET /_pmq/admin/status/{name}` reports the active release ID.
- `GET /_pmq/admin/releases/{name}` lists release IDs, creation times and the active marker, newest first.
- `POST /_pmq/admin/rollback/{name}` accepts optional `{"release":"<id>"}`; without an ID it activates the previous release. A missing or invalid release leaves the active pointer unchanged.
- `POST /_pmq/admin/prune/{name}?keep=N` explicitly removes releases outside the N newest, always preserving the active release and the newest other release as a rollback target when one exists. N must be 1–1000. Deployment never prunes releases implicitly.
- `GET /_pmq/admin/apps` lists apps.

All administrative endpoints require `Authorization: Bearer <token>`. Deploy, rollback, and pruning operations are serialized to prevent conflicting pointer changes. `GET /_pmq/health` is public and returns process health only. Do not expose the admin token to browser code.

The reverse proxy should deny `/_pmq/admin/*` on ordinary app hostnames and expose admin routes only on a separately protected operator path/hostname. This is an additional ingress boundary; PMQ still requires the bearer deployment token. See the [Caddy recipe](proxy/caddy.md).
