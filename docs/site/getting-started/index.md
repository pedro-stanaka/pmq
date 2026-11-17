# Introduction

**PMQ — Poor Man's Quick. Build it. Drop it. Run it.**

PMQ is a tiny, self-hosted hosting platform for AI-generated (and human-written) web applications. It serves static frontends per hostname and gives every app a JSON document store, object storage, and an optional OpenAI-compatible AI gateway — all from a single Go binary with SQLite and the local filesystem.

It exists to remove operational ceremony: deploying a new app requires no container, no reverse-proxy change, no database server, and no deployment pipeline. Build your app, `pmq deploy` it, done.

::: warning Full-trust system
PMQ is designed for a **fully trusted group** — every person who can reach PMQ or its applications must be trusted. Authentication restricts access to that group; it does not provide per-user data isolation. Do not deploy PMQ where users are mutually untrusted. See [Authentication & Trust Model](/concepts/security).
:::

## What PMQ gives you

- **Static application hosting** with hostname-based routing and atomic, versioned releases.
- **JSON document persistence** per app, backed by one SQLite database per application.
- **File/object storage** on the local filesystem or an S3-compatible bucket.
- **AI model access** through an OpenAI-compatible gateway with server-side provider keys.
- **Shared authentication** via a trusted reverse proxy, plus a deployment token for management.

## What PMQ does not do

PMQ does not execute application backend code, provide user accounts or hard tenant isolation, run per-app containers, or speak arbitrary SQL. Prefer a boring, small, explicit tool: if PMQ cannot do it, it probably does not belong in PMQ.

## Where to go next

- [Install PMQ](/getting-started/install) — binary, Docker, `go install`, or Arch package.
- [Deploy Your First App](/getting-started/first-app) — a five-minute loopback walkthrough.
- [Architecture](/concepts/architecture) — how hosting, documents, and storage fit together.
- [For AI Agents](/agents) — skills that let coding agents build and deploy PMQ apps.
