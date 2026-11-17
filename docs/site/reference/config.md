# Configuration Reference

PMQ is configured by flags on `pmq serve` (see the [CLI reference](/reference/cli)) and environment variables. Flags take precedence over environment defaults. The [Compose file](https://github.com/pedro-stanaka/pmq/blob/main/compose.yaml) forwards all of these into the container.

## Core

| Variable | Default | Description |
|----------|---------|-------------|
| `PMQ_LISTEN` | `127.0.0.1:8080` | Listen address. |
| `PMQ_DATA_DIR` | `./data` | Releases, SQLite databases, and local objects. |
| `PMQ_DOMAIN` | `apps.localhost` | Application domain for hostname routing. |
| `PMQ_DEPLOY_TOKEN` | — (required) | Long random bearer token for deployment and admin APIs. |
| `PMQ_DEV` | unset | `true` enables loopback-only development mode. Never in production. |
| `PMQ_IDENTITY_HEADER` | `X-PMQ-User` | Identity header set by the trusted proxy. |
| `PMQ_TRUSTED_PROXIES` | — (required in production) | Comma-separated IPs or CIDRs of the proxy, as observed by PMQ. |

## Client

| Variable | Default | Description |
|----------|---------|-------------|
| `PMQ_SERVER` | `http://127.0.0.1:8080` | Server URL for CLI commands. |

## Object storage

| Variable | Default | Description |
|----------|---------|-------------|
| `PMQ_STORAGE_PROVIDER` | `filesystem` | `filesystem` or `s3`. |
| `PMQ_S3_ENDPOINT` | — | Host (and optional port) without a scheme. |
| `PMQ_S3_REGION` | — | Bucket region. |
| `PMQ_S3_BUCKET` | — | Bucket name; PMQ needs list access to `apps/` and read/write/delete under it. |
| `PMQ_S3_ACCESS_KEY` / `PMQ_S3_SECRET_KEY` | — | Credentials; keep them as environment secrets. |
| `PMQ_S3_SESSION_TOKEN` | — | Optional temporary credentials. |
| `PMQ_S3_INSECURE` | `false` | `true` disables TLS (local emulators only). |
| `PMQ_S3_PATH_STYLE` | `false` | `true` for services requiring path-style addressing. |

## AI gateway

All optional; the gateway is disabled until `PMQ_AI_MODELS` and `PMQ_AI_DEFAULT_MODEL` are set.

| Variable | Default | Description |
|----------|---------|-------------|
| `PMQ_AI_MODELS` | — | Comma-separated `alias=provider|model-id` (or `|stream` for Bedrock streaming aliases). Providers: `openai`, `openrouter`, `bedrock`. |
| `PMQ_AI_DEFAULT_MODEL` | — | Must name a configured alias. |
| `PMQ_AI_PRICING` | — | JSON of per-alias prices with `input_usd_per_million`, `output_usd_per_million`, `provenance`. Enables cost estimates and budgets. |
| `PMQ_AI_GLOBAL_BUDGET_USD` / `PMQ_AI_PER_APP_BUDGET_USD` | `0` | Daily (UTC) best-effort budget cutoffs; require pricing. |
| `PMQ_AI_GLOBAL_RPM` / `PMQ_AI_PER_APP_RPM` | `120` / `30` | Requests per minute. |
| `PMQ_AI_GLOBAL_CONCURRENCY` / `PMQ_AI_PER_APP_CONCURRENCY` | `16` / `4` | Active requests. |
| `PMQ_AI_MAX_REQUEST_BYTES` | `1048576` | Request body cap (hard max 8 MiB). |
| `PMQ_AI_MAX_RESPONSE_BYTES` | `2097152` | Provider response/stream cap (hard max 16 MiB). |
| `PMQ_AI_DEFAULT_OUTPUT_TOKENS` / `PMQ_AI_MAX_OUTPUT_TOKENS` | `1024` / `32768` | Output token default and ceiling. |
| `PMQ_AI_TIMEOUT` | `45s` | Provider deadline (hard max 10m). |
| `PMQ_OPENAI_BASE_URL` / `PMQ_OPENROUTER_BASE_URL` | — | Trusted compatible endpoints; production URLs must use HTTPS. |

Provider credentials stay server-side: `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, and the AWS credential chain (including `AWS_REGION`) for Bedrock. Never put them in app code.

## Observability

| Variable | Default | Description |
|----------|---------|-------------|
| `PMQ_OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | unset | Enables optional OTLP trace export. |

Prometheus metrics are served at `GET /_pmq/admin/metrics` with the deployment bearer token — see [Observability](/guides/observability).
