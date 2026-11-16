# AGENTS.md — PMQ (Poor Man's Quick)

## Project identity

**PMQ — Poor Man's Quick**

Tagline: **Build it. Drop it. Run it.**

PMQ is a tiny, self-hosted hosting platform for AI-generated web applications.

It provides:

1. Static application hosting with hostname-based routing.
2. JSON document persistence using SQLite.
3. File/object storage using the local filesystem.
4. AI model access through an OpenAI-compatible API gateway.
5. Shared authentication through a trusted reverse proxy.
6. A minimal deployment CLI.

Some capabilities may not yet be implemented. Consult the README and actual code to determine what currently exists.

The project is inspired by Shopify Quick, but is independently designed for small, trusted environments.

## Core philosophy

**The best infrastructure is the infrastructure you do not have to think about.**

PMQ exists to eliminate operational ceremony from deploying small applications.

A developer or coding agent should be able to build a static application and deploy it with a single command.

A new application should not require:

- A new container.
- A new reverse-proxy configuration.
- A new database server.
- A new OAuth application.
- A new infrastructure repository change.
- A new deployment pipeline.

PMQ is intentionally not a general-purpose PaaS.

### Guiding principles

1. **Simplicity over flexibility.** Prefer a small explicit implementation over a highly configurable framework.
2. **One binary where practical.** Avoid introducing long-running services without a compelling reason.
3. **Local-first.** SQLite and the filesystem are preferred over external infrastructure.
4. **Trusted environment, not trusted input.** Users may be trusted; HTTP requests, uploaded files, and generated JavaScript are not.
5. **Soft app isolation.** Separate data by application, but do not build a hardened multi-tenant platform.
6. **Agent-friendly by design.** APIs, commands, and errors should be predictable and easily understood by coding agents.
7. **Boring technology wins.** Prefer standard protocols and maintained libraries.
8. **No speculative abstractions.** Introduce abstractions only when justified by real use cases.
9. **End-to-end correctness.** A small working feature is better than a large incomplete framework.
10. **Explicit limitations.** Document what PMQ does not guarantee.

## Architecture

The intended high-level architecture is:

```text
Trusted LAN / Tailnet
         |
         v
  Reverse Proxy
  TLS + Authentication
         |
         v
    PMQ Go Server
         |
    +----+----------+----------+
    |               |          |
 Static Hosting   Documents   API Services
    |             (SQLite)    (Files, AI)
    |
 Per-app directories
```

Applications are static frontend builds.

PMQ does not execute arbitrary application backend code.

Apps use the PMQ API for persistence, files, and AI.

Each request is mapped to an application using its validated hostname.

Internal PMQ endpoints live under `/_pmq/`.

## Technology

- Language: Go.
- HTTP: standard `net/http`.
- Database: SQLite.
- API: JSON over HTTP.
- Storage: local filesystem.
- AI: OpenAI-compatible gateway.
- Deployment: Go CLI and HTTP deployment API.
- Hosting: Go's HTTP server.
- TLS and browser authentication: external reverse proxy.
- Containerization: Docker.
- Tests: Go standard testing package.

Prefer standard library packages unless a dependency substantially reduces complexity.

Avoid adding frameworks merely for familiarity.

## Repository conventions

- `cmd/` contains executable entry points.
- `internal/` contains implementation packages.
- `examples/` contains deployable sample applications.
- `docs/` contains architecture and operational documentation.

Keep packages cohesive.

Avoid circular dependencies.

Do not create empty packages for planned features.

Do not build generic framework abstractions before they are needed.

When introducing an interface, explain why the interface is useful.

Prefer concrete types when there is only one implementation.

## API conventions

Application APIs are exposed relative to the current application origin:

```text
/_pmq/db/*
/_pmq/storage/*
/_pmq/ai/*
```

Do not require applications to know the PMQ server's internal hostname.

Use consistent JSON responses and HTTP status codes.

Errors should be machine-readable and helpful to humans.

Example:

```json
{
  "error": {
    "code": "document_not_found",
    "message": "Document does not exist"
  }
}
```

Validate all user-controlled input.

Limit request sizes.

Avoid exposing arbitrary SQL.

Do not expose filesystem paths in public API responses.

Application namespaces must be derived from validated server-side routing, not arbitrary client-provided IDs.

## Data model

Each application has independent persistent data.

Prefer one SQLite database per application.

JSON document collections are the default abstraction.

Do not introduce application-specific SQL schemas into PMQ itself.

SQLite migrations for PMQ's internal storage are acceptable and should be versioned.

Use transactions when updating multiple related records.

Handle concurrent access correctly.

Keep persistent data separate from deployable application assets.

Frontend redeployments must never delete persistent app data.

## Authentication and trust boundaries

PMQ is designed for private trusted environments.

This does not mean authentication can be bypassed by default.

Production deployments must authenticate application requests through a trusted reverse proxy or another explicitly supported mechanism.

Never trust arbitrary identity headers from direct requests.

Never accept a spoofable `X-Forwarded-For` header as proof that a request came from a trusted proxy.

Prefer network-level restrictions, verified proxy identity, and explicit trusted-proxy configuration.

Administrative operations require separate credentials.

Do not expose deployment credentials or provider API keys to browser applications.

Prevent unauthorized access to backend APIs, not just static HTML.

Cookie-authenticated write operations must be protected against CSRF.

Fail closed when production authentication configuration is missing.

Any local development bypass must be explicit, loopback-only, and disabled by default.

Avoid creating a second user-management platform.

Do not implement hard tenant isolation unless requirements change.

## Security baseline

Even in a high-trust environment:

- Reject path traversal.
- Reject unsafe upload paths and symlinks.
- Validate app and collection names.
- Prevent access to application data directories through static hosting.
- Restrict administrative endpoints.
- Avoid logging secrets.
- Use bounded request bodies and sensible timeouts.
- Ensure AI provider keys remain server-side.
- Prevent arbitrary remote code execution.
- Avoid shell interpolation of untrusted input.
- Keep applications from invoking privileged management APIs.

Security should be proportionate to the deployment model, but never based solely on the assumption that generated application code is harmless.

## Deployment

Deployments must be:

- Atomic.
- Repeatable.
- Idempotent where practical.
- Recoverable after interruption.
- Independent of persistent app data.

Never expose partially uploaded releases.

Use versioned releases.

The eventual CLI should support deploy, list, status, rollback, and delete operations.

Do not add container-per-app deployment functionality without an explicit product decision.

## Development workflow

Before making changes:

1. Read this file.
2. Read the relevant existing code.
3. Identify the smallest coherent implementation.
4. Check existing patterns and dependencies.
5. Avoid unrelated refactoring.

During implementation:

1. Implement a complete vertical slice.
2. Add tests.
3. Run formatting and static analysis.
4. Run relevant tests.
5. Fix failures.
6. Update documentation.
7. Review the diff.

After implementation:

1. Summarize what changed.
2. Report tests run.
3. Identify unverified behavior.
4. Document genuine limitations.
5. Provide exact next steps.

## Standard commands

Use the repository's Makefile when available:

```bash
make build
make test
make lint
make check
```

Otherwise, at minimum run:

```bash
gofmt -w .
go vet ./...
go test ./...
go build ./...
```

Use the Go race detector for code involving concurrency when supported:

```bash
go test -race ./...
```

Do not claim a test passed unless it was executed.

## Testing expectations

All new behavior should have appropriate tests.

Prefer:

- Unit tests for pure logic.
- HTTP handler tests using `httptest`.
- Temporary directories for filesystem tests.
- Temporary SQLite databases for persistence tests.
- Integration tests for complete user workflows.

Test failure paths, not only happy paths.

In particular, test:

- Invalid hostnames.
- Namespace confusion.
- Unauthorized API access.
- Path traversal.
- Malformed JSON.
- Oversized requests.
- Concurrent writes.
- Interrupted deployments.
- Persistent data surviving redeployment.

External AI providers should be mocked in automated tests.

Tests must not depend on the author's personal homelab.

## Coding style

Write idiomatic Go.

Prefer:

- Small focused functions.
- Explicit error handling.
- Context propagation.
- Descriptive names.
- Structured logging.
- Minimal dependencies.
- Readable control flow.
- Errors with useful context.
- Standard library HTTP APIs.

Avoid:

- Excessive interfaces.
- Service locator patterns.
- Reflection-heavy frameworks.
- Global mutable state.
- Clever abstractions.
- Premature optimization.
- Unnecessary concurrency.
- Silent error handling.
- Large generated codebases.
- Overly generic configuration systems.

Comments should explain why something exists, not merely restate the code.

## Dependencies

Before adding a dependency, consider:

1. Can the standard library solve it simply?
2. Is the library actively maintained?
3. Does it significantly reduce complexity?
4. Does it increase operational requirements?
5. Is it necessary for the current milestone?

Use pinned module versions and reproducible builds.

Do not introduce external infrastructure without explicit justification.

## Documentation

Documentation is part of the implementation.

Update the README whenever user-facing behavior changes.

Keep documentation actionable.

Prioritize:

- Quick start.
- CLI examples.
- API examples.
- Configuration.
- Authentication.
- Deployment.
- Backups.
- Recovery.
- Known limitations.

Do not document aspirational features as implemented functionality.

## Working with coding agents

PMQ should be easy for coding agents to understand and operate.

Prefer clear conventions over extensive configuration.

For any new feature, consider:

- Can a coding agent discover how to use it?
- Are errors actionable?
- Is the API consistent?
- Can the behavior be tested locally?
- Does the implementation require a human to perform repetitive configuration?

Avoid introducing requirements that every generated application must understand complicated infrastructure.

## Scope discipline

Before adding a major feature, ask:

**Does this make deploying or operating small applications substantially easier?**

If not, it probably does not belong in PMQ.

Do not turn PMQ into:

- Kubernetes.
- Heroku.
- Supabase.
- A general-purpose workflow engine.
- A container orchestration platform.
- An authentication provider.
- A serverless execution framework.

When unsure, implement the smallest solution that satisfies the current requirement.

## Task completion and stop conditions

When given an implementation task:

- Do not stop after writing a plan.
- Do not stop after creating partial scaffolding.
- Do not stop because one test fails; investigate and fix it.
- Do not silently skip acceptance criteria.
- Do not replace a requested feature with a mock and call it complete.
- Do not repeatedly request confirmation for ordinary implementation decisions.
- Do not claim success based solely on code inspection.
- Do not leave unfinished work without explaining precisely what remains.

Continue until all achievable acceptance criteria are satisfied and verified.

If a genuine external blocker exists, such as unavailable credentials or infrastructure access:

1. Complete all unblocked work.
2. Add local tests or mocks where appropriate.
3. Document the exact blocker.
4. Provide instructions for completing the blocked verification.
5. Do not claim the blocked part is complete.

Never push, merge, or deploy to production unless authorized.

## Definition of done

A task is complete when:

- The implementation satisfies the stated requirements.
- Relevant tests pass.
- Formatting and static checks pass.
- Documentation is updated.
- Existing functionality remains intact.
- Security boundaries are respected.
- The resulting code is maintainable.
- The final response reports verification accurately.

**PMQ should remain something one developer can understand, operate, and maintain without needing a platform engineering team.**
