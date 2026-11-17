# Observability

PMQ exposes one Prometheus registry at `/_pmq/admin/metrics`. The endpoint requires `Authorization: Bearer <PMQ_DEPLOY_TOKEN>` and is intended for a trusted, private scrape path. Do not publish it to the public internet. Configure the reverse proxy and network firewall to allow the Prometheus host only; the token is the deployment credential and must be stored as a secret. `/_pmq/health` is unauthenticated process health and does not reveal metrics or indicate application/database readiness.

Example Prometheus scrape configuration:

```yaml
scrape_configs:
  - job_name: pmq
    scheme: https
    metrics_path: /_pmq/admin/metrics
    static_configs:
      - targets: [pmq-admin.internal.example:443]
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/secrets/pmq-deploy-token
```

The credentials file contains the token only, with no `Bearer` prefix. Prometheus supports bearer authorization with a credentials file in its HTTP scrape configuration; see the [Prometheus configuration reference](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#http_config).

## Metric names

- `pmq_http_requests_total{route,method,status_class}` counts requests. Routes are fixed templates such as `/_pmq/db/{collection}/{id}`, `/_pmq/storage/{key}`, `/static`, and `/_pmq/admin/metrics`. Methods are standard HTTP methods or `OTHER`; status is a bounded class such as `2xx`.
- `pmq_http_request_duration_seconds{route,method}` and `pmq_http_requests_in_flight{route}` measure HTTP latency and concurrent requests.
- `pmq_sqlite_document_operations_total{operation,outcome}` counts `create`, `list`, `get`, `update`, and `delete`. Outcomes are `success`, `not_found`, `busy`, `locked`, `canceled`, `constraint`, or `other`.
- `pmq_sqlite_document_operation_duration_seconds{operation,outcome}` measures database operation duration. `pmq_sqlite_document_rows_returned_total{operation}` counts returned document rows; deletes do not return document rows.
- `pmq_sqlite_document_errors_total{operation,error}` counts bounded SQLite error categories: `busy`, `locked`, `canceled`, `constraint`, and `other`.
- `pmq_storage_http_operations_total{operation,outcome}` records storage API outcomes. Operation labels are `upload`, `download`, `head`, `list`, `delete`, or `other`; outcomes include `success`, `not_found`, `provider_error`, `canceled`, `too_large`, `unauthorized`, `forbidden`, and `rejected`.
- `pmq_storage_transfer_cancellations_total{operation}` counts request-context cancellations separately from provider errors.
- Thanos objstore metrics share the same registry and use the fixed `bucket="pmq"` label. They report bucket operation counts, failures, durations, and transferred bytes, including `get_range` where supported by the provider.

No metric label contains an application name, hostname, user identity, collection, document ID, object key, SQL statement, or file path. Do not add dynamic labels when extending this instrumentation.

## PromQL examples

95th percentile HTTP latency for document API requests:

```promql
histogram_quantile(0.95,
  sum by (le, route) (
    rate(pmq_http_request_duration_seconds_bucket{route=~"/_pmq/db/.*"}[5m])
  )
)
```

SQLite contention rate by operation and lock type:

```promql
sum by (operation, error) (
  rate(pmq_sqlite_document_errors_total{error=~"busy|locked"}[5m])
)
```

Object storage provider error ratio over the last five minutes:

```promql
sum(rate(pmq_storage_http_operations_total{outcome="provider_error"}[5m]))
/
clamp_min(sum(rate(pmq_storage_http_operations_total{operation!="other"}[5m])), 1e-9)
```

Transferred object bytes per second by operation:

```promql
sum by (operation) (
  rate(objstore_bucket_operation_transferred_bytes_sum{bucket="pmq",operation=~"upload|get|get_range"}[5m])
)
```

Useful initial alerts are sustained SQLite `busy`/`locked` errors, a sustained rise in the storage provider error ratio, and a PMQ target that is down. Tune thresholds to the deployment and scrape interval; the API is designed for one small trusted node rather than a high-throughput multi-tenant service.
