# AI gateway observability

AI gateway metrics are registered in PMQ's existing Prometheus registry. Scrape them through the authenticated `GET /_pmq/admin/metrics` endpoint; the route is protected by the admin bearer token and is not exposed on app hostnames.

| Metric | Meaning |
| --- | --- |
| `pmq_ai_requests_total` | Completed provider requests by provider, configured alias, outcome, and normalized error category |
| `pmq_ai_requests_in_flight` | Current provider calls by provider and configured alias |
| `pmq_ai_request_duration_seconds` | Provider request duration |
| `pmq_ai_time_to_first_token_seconds` | Time to first nonempty streamed content delta |
| `pmq_ai_tokens_total` | Provider-reported input/output token counts |
| `pmq_ai_estimated_cost_usd_total` | Estimated USD cost when operator-supplied prices are configured |
| `pmq_ai_usage_missing_total` | Successful calls without usable token usage metadata |
| `pmq_ai_limit_rejections_total` | Request, concurrency, and budget rejections (available with AI limits) |

The bounded `reason` label on the limit counter is one of `rate_limit`, `concurrency_limit`, or `budget_limit`.

Labels never contain app names, user identities, prompt text, credentials, upstream model IDs, provider error text, or pricing provenance. The model label is the finite configured alias set. Error categories are bounded (`none`, `request`, `provider`, `timeout`, `canceled`, `limit`). Usage is provider-reported. Estimated-cost samples have a bounded `basis` label (`reported_tokens` or `estimated_request`); pricing provenance is returned separately in the `X-PMQ-Pricing-Provenance` response header. Estimated costs are not billing data.

Tracing is disabled unless `PMQ_OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is set to an OTLP/HTTP traces endpoint. The endpoint must use HTTPS; loopback HTTP is allowed for local collectors. Example:

```sh
PMQ_OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=https://otel-collector.example/v1/traces
```

PMQ creates one `pmq.ai.request` span around the gateway call and propagates W3C `traceparent` to the provider. It drops incoming `tracestate` and `baggage` and does not record prompts, request bodies, app names, user identities, upstream model IDs, keys, or raw provider errors. Exporter connectivity is best effort; requests continue if the collector is unavailable. Keep the collector on a trusted network and apply its own access controls.

Example PromQL:

```promql
# Provider error ratio over five minutes
sum(rate(pmq_ai_requests_total{outcome="failure"}[5m])) / clamp_min(sum(rate(pmq_ai_requests_total[5m])), 1)

# p95 provider latency by provider and alias
histogram_quantile(0.95, sum by (le, provider, model_alias) (rate(pmq_ai_request_duration_seconds_bucket[5m])))

# Current provider saturation
sum by (provider, model_alias) (pmq_ai_requests_in_flight)

# Input and output token volume per hour
sum by (provider, model_alias, direction) (increase(pmq_ai_tokens_total[1h]))

# Estimated cost over a day, separated by the token-count basis
sum by (provider, model_alias, basis) (increase(pmq_ai_estimated_cost_usd_total[1d]))
```

Example alert expressions (choose thresholds and `for` windows for the local workload):

```promql
# Error rate above 5% while there is meaningful traffic
sum(rate(pmq_ai_requests_total{outcome="failure"}[5m])) / clamp_min(sum(rate(pmq_ai_requests_total[5m])), 0.01) > 0.05

# p95 provider latency above 10 seconds
histogram_quantile(0.95, sum by (le, provider) (rate(pmq_ai_request_duration_seconds_bucket[5m]))) > 10

# Any configured model alias remains saturated
pmq_ai_requests_in_flight > 20

# Operator-configured estimated daily spend exceeds a local threshold
sum(increase(pmq_ai_estimated_cost_usd_total[24h])) > 10
```

Estimated spend is exported only when operators configure prices; PMQ does not maintain a verified provider price catalog. Validate operator-supplied rates and their provenance before using these estimates for budgeting or alerts.
