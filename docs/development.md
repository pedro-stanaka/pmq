# Development

Use Go 1.27 or later. `make build`, `make test`, `make lint` (gofmt and go vet), and `make check` are available. `make run` starts development mode and requires `PMQ_DEPLOY_TOKEN`. Run `go test -race ./...` for concurrent changes. Tests use temporary directories and do not require a proxy or external database.

Configuration is available through flags on `pmq serve` or environment variables: `PMQ_LISTEN`, `PMQ_DATA_DIR`, `PMQ_DOMAIN`, `PMQ_DEPLOY_TOKEN`, `PMQ_DEV=true`, `PMQ_IDENTITY_HEADER`, and `PMQ_TRUSTED_PROXIES`. Run `pmq serve -h` for flags. Deploy and list accept `PMQ_SERVER` and `PMQ_DEPLOY_TOKEN` or `--server` and `--token`. Set `--dev` only with a literal loopback listen address; it is not intended for containers or shared hosts.

For a trusted local HTTPS certificate and explicit host entries, see “Local HTTPS with pmq.dev” in the README. The setup script accepts `--hosts-file PATH` before app names for testing with an alternate hosts file; it still runs `mkcert -install`. TLS flags must be supplied together and are accepted only in loopback development mode.

## S3 integration test

The storage contract always runs against a temporary filesystem bucket. To also run it against an existing disposable S3-compatible bucket, provide credentials with list, read, write, and delete access, then run:

```sh
PMQ_TEST_S3_ENDPOINT=127.0.0.1:9000 PMQ_TEST_S3_REGION=us-east-1 \
PMQ_TEST_S3_BUCKET=pmq-test PMQ_TEST_S3_ACCESS_KEY=test-access \
PMQ_TEST_S3_SECRET_KEY=test-secret go test ./internal/storage -run TestStorageContract -v
```

The test uses HTTP and path-style addressing and deletes its uniquely named objects. Run only against a dedicated local test service; do not use production credentials.
