# Object Storage

Apps can upload, download, inspect, list, and delete their own objects through `/_pmq/storage/*`. Objects are scoped by the validated app hostname; keys from another app's namespace are not addressable.

## Providers

- **filesystem** (default): objects live under `data/objects/` in the PMQ data directory. No credentials. Intended for local disks — not shared or distributed filesystems.
- **s3**: an existing S3-compatible bucket. Configure `PMQ_STORAGE_PROVIDER=s3` with `PMQ_S3_ENDPOINT`, `PMQ_S3_REGION`, `PMQ_S3_BUCKET`, and credentials (see the [configuration reference](/reference/config)). PMQ checks listing access at startup and fails fast on invalid configuration.

Each app maps to its own `apps/<app>/` prefix in the bucket. Changing providers does not migrate existing objects.

## Semantics worth knowing

- Keys are UTF-8 path-like names up to 160 bytes; traversal segments, backslashes, and control bytes are rejected.
- Uploads stream through bounded readers and are capped at 32 MiB. Downloads support a single byte range and conditional headers (`If-Match`, `If-None-Match`, `If-Modified-Since`, `If-Unmodified-Since`, `If-Range`); `HEAD` returns full metadata.
- Filesystem uploads stage to a swap file and rename only after success. S3 uploads expose the previous completed object until the replacement completes — a canceled or failed upload never advertises a partial object.
- Bucket operations, failures, durations, and transferred bytes are measured in the process Prometheus registry (see [Observability](/guides/observability)).

## Backups

Back up the local `objects/` directory with the rest of PMQ data. With S3, objects live outside that directory: configure bucket backups or versioning and test restoring them separately. Keep the bucket and local PMQ data in one coordinated backup plan.

This is soft separation, not protection against someone with bucket or filesystem access. The full endpoint contract is in the [HTTP API reference](/reference/api).
