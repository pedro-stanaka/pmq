# PMQ frontend API reference v1

This is a short snapshot of the current PMQ HTTP contract. API paths are not version-negotiated. Check the [full API docs](../../../docs/api.md) when the server branch changes.

## Shared rules

- Use the deployed app's origin and relative `/_pmq/...` paths. PMQ selects the app namespace from the validated hostname.
- In production, a trusted reverse proxy authenticates every app request. The browser should not send identity headers. All users accessing an app share its data.
- Browser writes require a matching Origin (HTTPS in production); same-origin `fetch` provides this automatically. Do not use cross-origin API URLs.
- Responses use JSON errors: `{"error":{"code":"...","message":"..."}}`. Show a useful message and retain form state when a request fails.
- Collection names are lowercase letters, digits, and interior hyphens, up to 63 characters; `admin`, `api`, `www`, and `pmq` are reserved.

## Documents

`POST /_pmq/db/{collection}` creates a document from a JSON object (up to 1 MiB) and returns 201:

```json
{"id":"32-lowercase-hex-characters","collection":"tasks","data":{"text":"Plan the app","done":false},"createdAt":"...","updatedAt":"..."}
```

The `data` value must be an object. Collections are created implicitly by writing a document. Use:

- `GET /_pmq/db/{collection}` → `{ "documents": [...], "limit": 50, "offset": 0 }`; `limit` 1–100, `offset` 0–1000000.
- `GET /_pmq/db/{collection}/{id}` → document or 404.
- `PUT /_pmq/db/{collection}/{id}` → replace the entire `data` object or 404. Send every field that should remain.
- `DELETE /_pmq/db/{collection}/{id}` → 204 or 404.

Common codes include `document_not_found`, `invalid_json`, `invalid_path`, and `body_too_large`. There is no query language, filtering, partial patch, or client-selected app namespace.

## Objects

- `PUT /_pmq/storage/{key}` uploads or replaces a key; max 32 MiB, returns 201.
- `GET /_pmq/storage/{key}` downloads bytes. `HEAD` returns size/type/time. GET supports one byte range; HEAD ignores Range.
- `DELETE /_pmq/storage/{key}` deletes an object or returns 404.
- `GET /_pmq/storage?prefix={prefix}&limit={n}&cursor={cursor}` lists keys. Limit defaults to 100 and is 1–500; pass `nextCursor` unchanged.

Keys are UTF-8 path-like names up to 160 bytes. Empty, `.` and `..` segments, backslash, NUL and control bytes are rejected. Percent-encode reserved URL characters, for example `encodeURIComponent(key)` in browser code. Keep user-visible keys predictable; do not use untrusted path fragments without validation.

## AI chat

The optional `POST /_pmq/ai/chat/completions` accepts a text-only Chat Completions subset when the server is configured with a provider and model allowlist. For a normal response, read the normalized completion:

```js
const response = await fetch('/_pmq/ai/chat/completions', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({
    model: 'fast',
    messages: [{role: 'user', content: 'Suggest a short title'}],
    max_tokens: 128,
  }),
});
if (!response.ok) throw new Error('AI request failed');
const completion = await response.json();
const text = completion.choices[0].message.content;
```

Allowed fields are `model` (server-configured alias; omitted selects the default), optional matching `provider`, text `messages` with `system`, `user`, or `assistant` roles, `temperature` (0–2), `top_p` (0–1), `max_tokens` (1–32768, default 1024), `stop`, and `stream`. Non-streaming responses have a normalized completion object. With `stream: true`, the response is SSE (`text/event-stream`); parse `data:` events and stop on `[DONE]`:

```js
const response = await fetch('/_pmq/ai/chat/completions', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({model: 'fast', stream: true, messages: [{role: 'user', content: 'Hello'}]}),
});
if (!response.ok) throw new Error('AI request failed'); // Before the first event, errors are JSON.
const reader = response.body.getReader();
const decoder = new TextDecoder();
let pending = '';
let finished = false;
while (!finished) {
  const {value, done} = await reader.read();
  pending += decoder.decode(value || new Uint8Array(), {stream: !done});
  const lines = pending.split(/\r?\n/);
  pending = lines.pop() || '';
  for (const line of lines) {
    if (line.startsWith('event: error')) throw new Error('AI stream failed');
    if (!line.startsWith('data:')) continue;
    const data = line.slice(5).trim();
    if (data === '[DONE]') { finished = true; break; }
    const event = JSON.parse(data);
    renderChunk(event.choices?.[0]?.delta?.content || '');
  }
  if (done) finished = true;
}
```

OpenAI and OpenRouter aliases support streaming; Bedrock aliases must be configured with the `|stream` capability marker. Model aliases and the default model are configured server-side. Provider credentials remain server-side. Tools, multimodal input and provider-specific extensions are not supported. AI can be disabled; handle 503 and upstream errors in the UI. See [AI API details](../../../docs/api.md#ai-chat-completions) and [server configuration](../../../README.md#ai-chat-completions-gateway).

See [full endpoint details](../../../docs/api.md), [server configuration](../../../README.md#ai-chat-completions-gateway), and the [proxy trust contract](../../../README.md#production-and-proxy-contract).
