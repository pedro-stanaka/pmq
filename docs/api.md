# API

All app endpoints use the deployed app's hostname and authenticated identity. Responses are JSON except successful DELETE (204). Errors have `{"error":{"code":"...","message":"..."}}`. Collection names use lowercase letters, digits, and interior hyphens, up to 63 characters; reserved names include `admin`, `api`, `www`, and `pmq`. IDs are generated 32-character lowercase hex strings.

- `POST /_pmq/db/{collection}`: create from a JSON object (up to 1 MiB); returns 201 and document.
- `GET /_pmq/db/{collection}`: returns `{ "documents": [...], "limit": 50, "offset": 0 }`. `limit` is 1–100; `offset` is 0–1000000. Results sort by creation timestamp and ID.
- `GET /_pmq/db/{collection}/{id}`: return document or 404.
- `PUT /_pmq/db/{collection}/{id}`: replace its entire data object; return document or 404.
- `DELETE /_pmq/db/{collection}/{id}`: delete or return 404.

A document has `id`, `collection`, `data`, `createdAt`, and `updatedAt`. There is no filtering, SQL, or partial update. Mutating app requests require an Origin matching the request Host (HTTPS in production). `/_pmq/ai/*` remains reserved and returns 404.

## Object storage

Objects are scoped to the application selected from the validated request Host. Keys are UTF-8 path-like names up to 160 bytes; empty segments, `.`/`..`, backslashes, control bytes, and NUL are rejected. Use percent encoding for reserved URL characters. Uploads are limited to 32 MiB. Successful PUT returns 201 with an empty body.

- `PUT /_pmq/storage/{key}`: create or replace the object. The request `Content-Type` must be syntactically valid; PMQ chooses the response type deterministically from the key extension (unknown extensions use `application/octet-stream`).
- `GET /_pmq/storage/{key}`: returns object bytes with `Content-Length`, `Last-Modified`, and the inferred `Content-Type`.
- `HEAD /_pmq/storage/{key}`: returns the same metadata headers without a body.
- `DELETE /_pmq/storage/{key}`: delete or return 404.
- `GET /_pmq/storage?prefix={prefix}&limit={n}&cursor={cursor}`: returns `{ "objects": [{"key":"...","size":123,"lastModified":"...","contentType":"..."}], "limit":100, "nextCursor":"..." }`. Limit defaults to 100 and must be 1–500. Pass `nextCursor` unchanged to get the next page; an empty cursor means there are no further results. Pages are ordered by raw key. Pagination is not a snapshot: writes and deletes during traversal can change later pages.

Unauthenticated requests fail. PUT and DELETE require a matching Origin (HTTPS in production), following the same trusted-proxy identity rules as document writes. A key from another app's namespace is not addressable through this API. Errors use the common JSON envelope, with 400 for invalid keys, 404 for missing objects, 413 for oversized uploads, and 500 for provider failures.

Storage bucket metrics are registered in the process Prometheus registry with the fixed logical `bucket="pmq"` label. Operations, failures, duration, and transferred bytes are measured by the objstore wrapper. `GET /_pmq/admin/metrics` exposes the scrape only with the deployment bearer token; keep this administrative endpoint restricted at the reverse proxy too. Metric labels never include application names, object keys, paths, or identities.

Administrative API: `POST /_pmq/admin/deploy/{name}` accepts a gzip-compressed tar of regular files and directories, at most 32 MiB compressed and uncompressed, with root `index.html`. Deploying an existing name replaces its entire active set of static files with the new archive without an overwrite prompt or merge; files omitted from the archive are no longer part of the active app. The app’s persistent data remains intact. `GET /_pmq/admin/apps` lists apps. Admin endpoints require `Authorization: Bearer <token>`. `GET /_pmq/health` is public and returns process health only. Do not expose the admin token to browser code.
