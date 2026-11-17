# Browser SDK

`@pmq-dev/client` is a dependency-light TypeScript client for application browser code. It is currently a private workspace package and is not published to a registry. The Vite example is runnable from this repository; see [examples/vite](https://github.com/pedro-stanaka/pmq/blob/main/examples/vite/README.md).

```ts
import { createPMQClient, PMQError } from "@pmq-dev/client";

const pmq = createPMQClient(); // Relative /_pmq URLs on this app's origin.
const note = await pmq.documents.create("notes", { title: "Hello", done: false });
const page = await pmq.documents.list("notes", { limit: 20 });
const loaded = await pmq.documents.get("notes", note.id);
await pmq.documents.update("notes", note.id, { ...loaded.data, done: true });

await pmq.storage.upload("attachments/greeting.txt", new Blob(["hello"]));
const download = await pmq.storage.get("attachments/greeting.txt");
console.log(await download.text());

const completion = await pmq.ai.complete({
  model: "fast",
  messages: [{ role: "user", content: "Summarize the note." }],
});
console.log(completion.choices[0].message.content);

for await (const chunk of pmq.ai.stream({
  model: "fast",
  messages: [{ role: "user", content: "Write a greeting." }],
})) {
  if (chunk.type === "chunk") {
    console.log(chunk.chunk.choices[0].delta.content ?? "");
  } else {
    console.log("AI usage", chunk.status, chunk.usage);
  }
}
```

Pass `signal` to any operation for cancellation:

```ts
const controller = new AbortController();
const pending = pmq.documents.list("notes", { signal: controller.signal });
controller.abort();
await pending; // Rejects with the Fetch AbortError.
```

For a stream, the signal aborts the request and exiting the `for await` loop cancels the response reader. Iteration reads incrementally, which lets a slow consumer apply backpressure instead of collecting the complete stream in memory. Events are discriminated: `{type: "chunk", chunk}` holds OpenAI-compatible completion chunks; `{type: "usage", ...}` holds PMQ's optional `pmq-usage` accounting event, including available token usage and cost metadata. Unknown named extension events are skipped. AI streaming requires a PMQ server build/provider alias that supports streaming; see the [AI API contract](api.md#ai-chat-completions).

## Vanilla JavaScript

The package ships ES modules and TypeScript declarations. Plain JavaScript can import its built module directly after building the SDK:

```js
import { createPMQClient, PMQError } from "@pmq-dev/client";

const pmq = createPMQClient();
try {
const { documents } = await pmq.documents.list("notes");
  document.querySelector("#count").textContent = String(documents.length);
} catch (error) {
  if (error instanceof PMQError) {
    console.error(`PMQ ${error.status} (${error.code}): ${error.message}`);
  } else {
    throw error;
  }
}
```

The SDK uses browser same-origin credentials and relative URLs by default. Keep apps on their PMQ application origin and put authentication at the trusted reverse proxy. Do not pass an admin/deploy token, provider key, or identity header from application code. PMQ's app trust model is shared: authenticated users of an app can access that app's data.

`PMQError` exposes the HTTP `status`, machine-readable `code`, and server message. A non-JSON gateway/proxy response gets a stable `http_error` code. Network errors and aborts retain the Fetch error behavior. `storage.get` returns the checked `Response` so the app can consume its body as a stream or use range response headers.

## Local development and checks

From the repository root:

```sh
npm install
npm run build
npm test
npm run dev --workspace @pmq-dev/example-vite
```

For the included example, run a local PMQ server in `--dev` mode on `127.0.0.1:8080`, deploy an app named `sdk-test` on the `apps.localhost` domain, then start Vite and open `http://sdk-test.apps.localhost:5173`:

```sh
npm run dev --workspace @pmq-dev/example-vite
```

The Vite proxy forwards `/_pmq/` to the matching PMQ app hostname and rewrites the Origin to that local target, allowing PMQ's write-Origin check to work in dev mode. Vite proxying is for local development only. The SDK integration suite builds and starts a real loopback PMQ process in a temporary data directory, deploys the hello-world app, and uses a local fake OpenAI-compatible endpoint; it does not contact production or an external AI provider. If run against a PMQ branch without AI streaming support, only the live streaming assertion is skipped; stream framing, incremental delivery, cancellation, usage events, and stream errors are still tested using a local Fetch stream fixture.

`baseUrl` and `fetch` constructor options exist for integration tests and custom runtimes. Browser applications should omit `baseUrl` so the SDK stays on the app origin. They are not credentials or authentication configuration.
