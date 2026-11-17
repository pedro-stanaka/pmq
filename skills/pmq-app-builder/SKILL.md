---
name: pmq-app-builder
description: Build, improve, and validate static frontend apps hosted by PMQ, including same-origin document, object storage, and configured AI API use.
---

# PMQ app builder

Use this skill when creating or changing a frontend intended to deploy to PMQ. PMQ serves static files and does not run app server code. Keep app state and behavior in the browser; use PMQ's same-origin APIs for persistence and configured AI access.

## Build the app

- Prefer the smallest stack that fits the request. Use the vanilla starter for a small app; use the React/Vite/TypeScript starter when component state or an existing React codebase benefits from it. Copy a starter into the app workspace and adapt it; do not edit the skill's template in place.
- For a Vite app, deploy the generated `dist/` directory. It must contain `index.html` and all referenced assets. Other static build systems should deploy their equivalent output directory.
- To use the React/Vite template, install from its lockfile with `npm ci` and build with `npm run build`; it requires Node `^20.19.0` or `>=22.12.0`. Deploy `dist/`, never the development server or source tree.
- PMQ serves app assets by hostname and falls back to the deployed `index.html` for unknown static paths. Client-side SPA routes can therefore load directly, but still test a refresh on a nested route.
- Call app APIs using relative URLs such as `fetch('/_pmq/db/tasks')`. Do not hard-code the PMQ server hostname or make browser requests directly to provider services.
- Use raw `fetch` for a small dependency-free app, or the typed browser client for projects that benefit from typed errors and streaming. The client is a private workspace package, not a published registry package; see the repository's [SDK guide](../../docs/sdk.md) and confirm compatibility with the deployed server.

## Trust and data rules

- PMQ's reverse proxy authenticates each app request in production. Do not build a second login flow or send identity headers from JavaScript. All users of an app share its documents and objects; PMQ is for a trusted group, not mutually untrusted tenants.
- Browser writes need a same-origin `Origin`; normal browser `fetch` supplies it. Do not disable the check or route writes through a cross-origin proxy. Local `--dev` mode is an explicit loopback-only development bypass, not a production auth setup.
- Never put the deployment token, proxy credentials, AI provider keys, or other secrets in source, `VITE_*` variables, HTML, or the built bundle. AI keys and model allowlists are configured on the PMQ server.
- Store data under collections and object keys, not in local storage when it must survive browsers/devices or deployments. Never ask clients to supply an app ID; PMQ scopes requests from the validated app hostname.

## API and stack references

- Read [PMQ API v1](references/api-v1.md) for the short current HTTP contract and examples. It links to the repository's [full API documentation](../../docs/api.md).
- Read [validation checklist](references/validation-checklist.md) before calling an app done.
- For a plain JavaScript app, start from [vanilla](assets/templates/vanilla/).
- For React and TypeScript, start from [React/Vite](assets/templates/react-vite-ts/).

The `v1` reference names this skill's snapshot of the current API paths; it does not mean that PMQ negotiates an HTTP API version. Check the linked API docs and server before relying on a capability. AI chat is text-only; streaming is available only for configured model aliases whose provider supports it. The typed browser SDK is optional; raw `fetch` works with the same same-origin API.

## Validate

Build the app, deploy the actual output directory, and exercise it under its app hostname. Verify at least one successful create/read/update/delete flow, user-visible loading and error states, and that the app still works after redeployment. When changing storage or AI features, test the relevant API path too. The independent end-to-end acceptance app lives in `tests/acceptance/`; run it with `scripts/acceptance.sh` from the repository root.
