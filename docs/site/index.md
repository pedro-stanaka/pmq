---
layout: home

hero:
  name: PMQ
  text: Poor Man's Quick
  tagline: Build it. Drop it. Run it. Self-hosted hosting for small apps — static frontends, per-app JSON documents, object storage, and an AI gateway, all in one Go binary.
  actions:
    - theme: brand
      text: Get Started
      link: /getting-started/
    - theme: alt
      text: View on GitHub
      link: https://github.com/pedro-stanaka/pmq

features:
  - icon: 📦
    title: One-command deploys
    details: Build a static app, run pmq deploy, and it is live at its own hostname. No new containers, proxy configs, databases, or pipelines per app.
  - icon: 🗄️
    title: JSON documents per app
    details: Per-app SQLite-backed collections behind /_pmq/db — create, list, get, replace, and delete documents over plain HTTP.
  - icon: 🪣
    title: Object storage
    details: Streamed uploads, downloads, ranges, and conditional requests behind /_pmq/storage, on the local filesystem or an S3-compatible bucket.
  - icon: 🤖
    title: OpenAI-compatible AI gateway
    details: A text-subset Chat Completions endpoint with streaming, rate limits, budgets, and cost estimates. Provider keys stay server-side.
  - icon: 🔐
    title: Trusted-proxy authentication
    details: One shared login boundary through your existing reverse proxy. No second user-management platform to operate.
  - icon: 🧩
    title: Agent-friendly by design
    details: Predictable CLI, consistent JSON errors, and ready-made agent skills, so coding agents can build and operate apps unaided.
---
