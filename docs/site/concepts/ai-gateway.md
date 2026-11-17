# AI Gateway

The optional `POST /_pmq/ai/chat/completions` endpoint implements a documented **text-only subset** of the OpenAI Chat Completions contract, including SSE streaming. It uses the app's own origin, the same proxy authentication and write-Origin checks as documents, and **server-side provider credentials** — provider keys never appear in app JavaScript.

## Configuration

The gateway is disabled until an operator configures public model aliases and a default:

```sh
# Alias=provider|upstream-model-id
PMQ_AI_DEFAULT_MODEL=fast
PMQ_AI_MODELS='fast=openai|gpt-4o-mini,router=openrouter|openai/gpt-4o-mini'
OPENAI_API_KEY=...        # stays server-side
OPENROUTER_API_KEY=...
```

Supported providers are `openai`, `openrouter`, and `bedrock`. Apps choose among the *configured aliases* (via the `model` field) but can never supply URLs, credentials, or upstream model IDs. All tuning variables — rate limits, concurrency, size caps, timeouts, pricing, and budgets — are in the [configuration reference](/reference/config).

## From application code

Same-origin relative URL, the browser session is the credential:

```js
const response = await fetch('/_pmq/ai/chat/completions', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({messages: [{role: 'user', content: 'Hello'}]}),
});
const completion = await response.json();
```

Set `stream: true` to receive `text/event-stream` chunks; streams end with a `pmq-usage` accounting event and `data: [DONE]`. The [Browser SDK](/reference/sdk) wraps this contract, including on-demand stream iteration.

## Limits, budgets, and privacy

- Defaults: 120 requests/minute and 16 active requests globally, 30/minute and 4 active per app, 1 MiB request bodies, 2 MiB provider responses, 1024 default output tokens (32768 max), 45-second timeout.
- Optional operator-supplied pricing (`PMQ_AI_PRICING`) enables conservative cost estimates and daily UTC budgets (`PMQ_AI_GLOBAL_BUDGET_USD`, `PMQ_AI_PER_APP_BUDGET_USD`). Budgets are in-memory best-effort cutoffs, not billing records.
- Prompts and credentials are never logged; usage and cost metrics use bounded labels (see [AI Observability](/guides/ai-observability)).

The complete request/response contract is in the [HTTP API reference](/reference/api).
