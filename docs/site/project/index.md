# Roadmap & Limitations

## Shipped

- Static app hosting with hostname routing, atomic versioned releases, rollback, and pruning.
- Per-app JSON document collections on per-app SQLite databases.
- Object storage on the local filesystem or an S3-compatible bucket.
- Optional OpenAI-compatible Chat Completions gateway (text subset, streaming, limits, budgets).
- Trusted-proxy authentication with strict Origin/CSRF rules; deployment-token administration.
- Prometheus metrics and optional OTLP tracing.
- Multi-platform releases, GHCR images, and an Arch source package (see [Releases & Distribution](/reference/distribution)).

## Planned / exploratory

Nothing is promised by listing it here. Candidate areas from the issue tracker include broader agent-skill coverage and distribution polish. Treat the [GitHub issues](https://github.com/pedro-stanaka/pmq/issues) as the source of truth for planned work — this site documents only what exists.

## Known limitations

- **Single node.** One PMQ process per data directory; no distributed locking or clustering.
- **Full trust.** No per-user authorization or hard tenant isolation — every reachable user must be trusted. See [Authentication & Trust Model](/concepts/security).
- **No backend code execution.** PMQ serves static files only; apps use `/_pmq/*` APIs.
- **Documents are simple.** No filtering, partial updates, aggregation, or SQL.
- **Soft app isolation.** Hostname scoping protects against cross-app requests from browsers, not against a hostile operator or local user with data-directory access.
- **AI budgets are best-effort.** In-memory cutoffs that reset daily and on restart; they are not billing controls.
- **macOS/Windows binaries** are build-tested in CI but not executed there.

## Contributing

PMQ development is deliberately small: Go standard library first, one binary, SQLite and the local filesystem. Read the [AGENTS.md](https://github.com/pedro-stanaka/pmq/blob/main/AGENTS.md) conventions and [Developing PMQ](/project/development) before proposing changes. Bug reports and reproducible failure reports are the most valuable contributions.

## License

MIT — see [LICENSE](https://github.com/pedro-stanaka/pmq/blob/main/LICENSE).
