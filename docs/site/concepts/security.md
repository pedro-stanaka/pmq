# Authentication & Trust Model

PMQ is a **full-trust system for private environments**. Every person who can access PMQ or its applications must be trusted. Authentication controls access to that trusted group; it does **not** provide per-user data isolation. Do not deploy PMQ where users are mutually untrusted — it is not a hardened multi-tenant platform.

## The proxy contract

In production, an HTTPS reverse proxy sits in front of PMQ and authenticates **every** request:

- The proxy terminates TLS, authenticates users, **strips all client-supplied instances of the identity header**, and sets exactly one nonempty identity after successful authentication (`PMQ_IDENTITY_HEADER`, default `X-PMQ-User`).
- `PMQ_TRUSTED_PROXIES` must list the exact source IPs or CIDRs of the proxy **as observed by PMQ** (inside the container for Docker). PMQ checks the TCP peer — it never trusts `X-Forwarded-For`.
- The proxy must preserve the application Host and the original HTTPS Origin, and must not let unauthenticated traffic reach PMQ through a trusted source.
- Production startup **fails closed** without a deployment token and trusted-proxy configuration. Application files and APIs require identity; only `/_pmq/health` is unauthenticated.

A trusted source that forwards unfiltered traffic can forge identities: restrict direct network access to the server and proxy, and do not blanket-trust private address ranges. See [Private Ingress](/guides/private-ingress) and the [Caddy recipe](/guides/caddy).

## Management authentication

Deployment and management operations (`pmq deploy`, `/_pmq/admin/*`) use a separate long random bearer token (`PMQ_DEPLOY_TOKEN`). The reverse proxy should additionally route `/_pmq/admin/*` only to authorized operators; the app hostname blocks admin routes, and admin endpoints independently require the bearer token. Never expose the token to browser code.

## CSRF and Origin rules

Cookie-authenticated writes require an `Origin` matching the request Host (HTTPS in production). This is why curl writes must send an explicit `Origin` header. AI calls follow the same rule.

## What authentication is not

- No per-user authorization: authenticated users of an app share all its data.
- No hard tenant isolation: app separation relies on validated hostname routing and browser origin rules, not sandboxing.
- No protection against a hostile local user who can alter PMQ's data directory — restrict filesystem access to the PMQ operator.

## Development mode

`--dev` is an explicit, loopback-only development bypass with a fixed development identity, disabled by default. Never enable it in production or containers. See [Deploy Your First App](/getting-started/first-app).
