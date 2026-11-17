# Production and recovery

Use the optional [production Compose example](../../examples/production/compose.yaml) with an existing private Caddy/SSO installation; PMQ does not require Komodo. Follow [private ingress](private-ingress.md) and the [Caddy recipe](caddy.md) first. All users must be trusted. Do not enable dev mode in production.

## Deploy with Compose or Komodo

Build the repository Dockerfile in CI, publish it to your trusted registry, and use a tested immutable image digest for `PMQ_IMAGE`. The supplied image runs as its unprivileged `pmq` user. The named data volume must be writable by that user; Docker initializes an empty named volume from `/data` in the image. If using a bind mount or a pre-existing volume, set its ownership based on the actual image UID/GID before startup. The example uses a read-only root filesystem, writable `/data` and `/tmp`, dropped capabilities, restart policy and a process-health check. Health does not validate SSO or upstream providers.

In Komodo create a Stack pointing at this repository and `examples/production/compose.yaml`; set the server, branch/revision and project name according to your Komodo installation. Supply `PMQ_IMAGE`, `PMQ_DOMAIN`, `PMQ_TRUSTED_PROXIES`, and `PMQ_DEPLOY_TOKEN` using Komodo's secret/environment facilities. Optionally provide the documented S3 and AI variables and provider credentials the same way. Use actual peer IPs, not `X-Forwarded-For`, and do not place credentials in source, image layers, frontend bundles, or public logs. Compose environment variables are visible to privileged host/container administrators; restrict host and Komodo access. Do not commit a populated `.env` file.

The sample binds PMQ's host port only to `127.0.0.1`. This suits a host Caddy reaching PMQ over loopback. For container Caddy, remove the host port and use a dedicated private network with the observed Caddy address, as in the [Caddy guide](caddy.md). Existing Caddy routes and other Komodo stacks need no PMQ-driven changes. The example intentionally does not set up OAuth, DNS, certificates, cloud credentials or firewall rules.

Before applying, use placeholder values to validate without revealing real secrets:

```sh
PMQ_IMAGE=example.invalid/pmq@sha256:0000000000000000000000000000000000000000000000000000000000000000 \
PMQ_DOMAIN=apps.example.com PMQ_TRUSTED_PROXIES=127.0.0.1/32 PMQ_DEPLOY_TOKEN=placeholder \
  docker compose -f examples/production/compose.yaml config --quiet
```

After an authorized rollout, check container health, proxy identity, a deployed app's static files and API, and denial of direct/forged requests. Pin the previous image digest and keep a verified backup. For an image rollback, stop PMQ and restore the matching backup if the data layout changed; never assume an older binary can read a newer data layout. Restart with the earlier pinned image and verify both apps and documents. Do not claim a live deployment has been validated until those checks actually ran.

## Consistent backup and restore

The data volume includes `VERSION`, app releases and active pointers, SQLite documents (including any `-wal` and `-shm` files), and local object files. Stop PMQ cleanly before making a filesystem archive so SQLite, releases, and object state form one consistent snapshot. Take the entire data volume; copying only `.db` files while PMQ is running is not a safe WAL backup. For online backup, use SQLite's backup API and coordinate it with a release/object snapshot; this example uses the simpler offline method. Store backups encrypted and access-controlled outside the host, keep retention copies, and test restoration periodically.

One approach, after confirming the exact Compose project and volume name, is to stop **only** PMQ and archive the volume using a pinned, trusted helper image. Choose an absolute backup directory with restricted permissions; these commands do not delete the source volume:

```sh
# Replace placeholders and use the same Compose project/secret environment as the deployment.
docker compose -f examples/production/compose.yaml stop pmq
docker run --rm -v ACTUAL_PMQ_VOLUME:/source:ro -v /ABSOLUTE/BACKUP/DIR:/backup \
  PINNED_TRUSTED_HELPER_IMAGE tar -C /source -czf /backup/pmq-data.tar.gz .
docker compose -f examples/production/compose.yaml start pmq
```

Restore into a **new empty** isolated volume with a pinned helper image, preserving permissions, then start a separate PMQ instance with that volume and appropriate private test routing. Verify `VERSION`, at least two application releases and their active pointers, documents for both apps, and local objects before any production cutover. Never extract an untrusted archive or overwrite the live volume during a drill. For S3, local `/data` backup does not include remote objects: configure and test bucket versioning, replication or independent backup and restore according to your provider's semantics, and coordinate its recovery point with the stopped PMQ snapshot.

For a repeatable no-credentials local drill of two apps, releases, documents and local objects, run:

```sh
go test ./internal/pmq -run '^TestOfflineBackupRestoreTwoApps$' -count=1 -v
```

This local drill uses temporary directories; it does not test your Komodo deployment, backup destination, S3 provider, or actual production recovery point.
