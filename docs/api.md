# API

All app endpoints use the deployed app's hostname and authenticated identity. Responses are JSON except successful DELETE (204). Errors have `{"error":{"code":"...","message":"..."}}`. Collection names use lowercase letters, digits, and interior hyphens, up to 63 characters; reserved names include `admin`, `api`, `www`, and `pmq`. IDs are generated 32-character lowercase hex strings.

- `POST /_pmq/db/{collection}`: create from a JSON object (up to 1 MiB); returns 201 and document.
- `GET /_pmq/db/{collection}`: returns `{ "documents": [...], "limit": 50, "offset": 0 }`. `limit` is 1–100; `offset` is 0–1000000. Results sort by creation timestamp and ID.
- `GET /_pmq/db/{collection}/{id}`: return document or 404.
- `PUT /_pmq/db/{collection}/{id}`: replace its entire data object; return document or 404.
- `DELETE /_pmq/db/{collection}/{id}`: delete or return 404.

A document has `id`, `collection`, `data`, `createdAt`, and `updatedAt`. There is no filtering, SQL, or partial update. Mutating app requests require an Origin matching the request Host (HTTPS in production). `/_pmq/storage/*` and `/_pmq/ai/*` are reserved and return 404.

Administrative API: `POST /_pmq/admin/deploy/{name}` accepts a gzip-compressed tar of regular files and directories, at most 32 MiB compressed and uncompressed, with root `index.html`. Deploying an existing name replaces its entire active set of static files with the new archive without an overwrite prompt or merge; files omitted from the archive are no longer part of the active app. The app’s persistent documents remain intact. `GET /_pmq/admin/apps` lists apps. Both require `Authorization: Bearer <token>`. `GET /_pmq/health` is public and returns process health only. Do not expose the admin token to browser code.
