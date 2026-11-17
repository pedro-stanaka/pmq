# JSON Documents

Each application gets its own SQLite database (`data/state/<app>/data.sqlite`, WAL mode) exposed as simple JSON document collections over HTTP. There is no per-app SQL schema, no arbitrary SQL endpoint, and no queries — just documents.

## The model

- A **collection** is created implicitly by its first document. Names use lowercase letters, digits, and interior hyphens, up to 63 characters.
- A **document** has `id` (generated 32-hex), `collection`, `data` (your JSON object, up to 1 MiB), `createdAt`, and `updatedAt`.
- Operations: create, list (paginated, sorted by creation), get, replace (whole `data` object), and delete. There is no partial update, filtering, or aggregation.
- Access is scoped to the validated app hostname: one app's collections are not addressable from another app's origin.

## From application code

Call relative URLs from the app's own origin — the browser session established by your reverse proxy is the credential, and writes send a matching `Origin` automatically:

```js
const created = await fetch('/_pmq/db/tasks', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({title: 'Practice German', done: false}),
}).then(r => r.json());
```

curl needs the Host and Origin headers explicitly — see [Deploy Your First App](/getting-started/first-app).

## Boundaries

- Document stores are soft isolation: all trusted users of an app share its data. There is no per-user authorization.
- Concurrency is handled by per-app serialized SQLite connections with a busy timeout.
- Databases are part of the app's persistent state: they survive redeployments and must be included in backups (including WAL files). See [Production & Recovery](/guides/production).

The complete request/response contract is in the [HTTP API reference](/reference/api).
