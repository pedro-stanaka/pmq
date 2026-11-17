---
name: pmq-deploy
description: Build, deploy, verify, roll back, and troubleshoot static apps hosted by PMQ using its CLI and trusted proxy. Use when operating a PMQ app or preparing a verified PMQ deployment; not for provisioning public ingress or user accounts.
---

# Deploy and operate a PMQ app

PMQ serves static builds by hostname and stores data separately from releases. All users of PMQ must be trusted. Use an existing authenticated PMQ installation; production TLS and browser authentication belong to its trusted reverse proxy.

1. Read the app's build instructions and PMQ's `README.md` and `docs/api.md`. Identify the actual frontend build directory and the PMQ application hostname. Build and test the app, then verify the output contains a root `index.html` and only regular files/directories: PMQ rejects symlinks, special files, traversal and archives over its documented limits. An SPA must use asset paths appropriate to its hostname.
2. Obtain the admin origin and deployment token from the operator's secret mechanism. Set `PMQ_SERVER` to the HTTPS operator origin and `PMQ_DEPLOY_TOKEN` in the process environment. Do not put tokens in source, frontend bundles, committed files, logs, or command-line arguments. The operator origin and browser app origin may differ. Verify the token is present without printing it.
3. Deploy with `pmq deploy BUILD_DIR --name APP`. Reusing the same name atomically replaces the active static file set; omitted files disappear. Documents and local objects remain separate. Run `pmq list`, `pmq status APP`, and `pmq releases APP`. Check `https://APP.DOMAIN/` and representative same-origin APIs through the actual proxy and identity session. Health (`/_pmq/health`) only indicates the PMQ process is responding; it does not prove SSO or an app works.
4. For rollback, inspect `pmq releases APP`, then run `pmq rollback APP` for the previous release or `pmq rollback APP --release RELEASE_ID` for a specific retained release. Verify the active ID with `pmq status APP` and retest app and data. Pruning is explicit (`pmq prune APP --keep N`); never prune releases needed for recovery without the operator's intent.
5. Report the deployed URL, active release, checks actually run, and any external prerequisites that could not be verified. A successful build alone is not a verified deployment.

For a disposable local test, use the quick start in the repository README. `PMQ_DEV=true`/`--dev` is explicitly loopback-only and must not be used as production authentication. Use test credentials, never a production token. The skill's integration test is `go test ./cmd/pmq -run '^TestDeploySkillWorkflow$' -count=1 -v` from the repository root.

## Troubleshooting

- **DNS or TLS:** resolve the app and operator names from the client, verify the correct private address and certificate hostname, and check proxy logs. Do not bypass TLS verification for a production deployment.
- **401:** verify the browser's proxy session or the admin token supplied through the environment; static files and app APIs both require proxy authentication. A valid browser SSO session does not grant deployment privileges.
- **403:** a write must include an `Origin` matching the app host and scheme; check the proxy preserves `Host` and `Origin`, and verify operator network restrictions on admin routes.
- **404:** check app name, domain and active release; admin endpoints may intentionally be blocked on app hosts. A host with extra subdomain labels or a reserved name is invalid.
- **Bad build or failed deploy:** inspect the build output, size, root `index.html` and archive restrictions. A failed deploy should leave the previous release active; check status and the actual app before retrying.
- **Proxy identity:** PMQ trusts only configured proxy TCP peers and the configured identity header; do not infer trust from `X-Forwarded-For`. Check the actual peer address, header stripping/injection and network isolation with the operator.
