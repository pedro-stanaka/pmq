# Shared Google SSO for PMQ apps

This recipe uses the existing Caddy `caddy-security` authentication portal. It creates no PMQ accounts. If Caddy already has a Google identity provider or portal, **reuse it**: merge the PMQ cookie/identity settings into that existing portal, and add the PMQ authorization policy and site routes below. Do not declare duplicate providers, portals, or global `security` blocks. The complete [example Caddyfile](https://github.com/pedro-stanaka/pmq/blob/main/examples/caddy/PMQ-SSO.Caddyfile) is a standalone validation fixture; adapt its names and secrets to the live Caddy configuration.

The app wildcard sends every request through caddy-security `authorize` before proxying to PMQ. The policy verifies the shared portal's signed access token, allows only users assigned the dedicated `pmq/app-user` role, clears the destination identity header before authorization, and injects `X-PMQ-User` from the authenticated email claim after authorization. The route also deletes any inbound `X-PMQ-User` before the authorization handler. Static files, deep links, database APIs, and future PMQ APIs all pass through the same route. The dedicated role is separate from caddy-security's common `authp/user` role, which an existing portal may grant broadly.

PMQ itself must run in production mode with `PMQ_IDENTITY_HEADER=X-PMQ-User` and `PMQ_TRUSTED_PROXIES` set to the actual Caddy TCP peer address PMQ observes. PMQ rejects direct requests from other peers even if they supply `X-PMQ-User`; it never trusts `X-Forwarded-For` for this check. Firewall PMQ so only Caddy can connect. See the [Caddy network recipe](caddy.md) for host and bridge layouts.

## Reuse the portal and configure Google

Build the existing Caddy binary with caddy-security and the Cloudflare DNS module. The example was validated with Caddy v2.11.9, caddy-security v1.4.0, and caddy-dns/cloudflare v0.2.4:

```sh
xcaddy build v2.11.9 \
  --with github.com/greenpau/caddy-security@v1.4.0 \
  --with github.com/caddy-dns/cloudflare@v0.2.4
```

Register a Google OAuth web client with this exact authorized redirect URI:

```text
https://sso.slopper.tanaka.dev.br/oauth2/google/authorization-code-callback
```

Keep `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` in Caddy's secret environment, not in the Caddyfile, PMQ app assets, or browser JavaScript. The Cloudflare DNS token is likewise a Caddy-only secret. Do not put `PMQ_DEPLOY_TOKEN` in Caddy's static configuration or frontend; the CLI supplies it only to protected admin API calls.

Set the Caddy and PMQ environment values (change the allowlisted email to a real, trusted Google identity):

```sh
PMQ_DOMAIN=slopper.tanaka.dev.br
PMQ_COOKIE_DOMAIN=slopper.tanaka.dev.br
PMQ_ALLOWED_EMAIL=operator@example.com
PMQ_UPSTREAM=127.0.0.1:8080
PMQ_OPERATOR_CIDRS=100.64.0.0/10
PMQ_IDENTITY_HEADER=X-PMQ-User
PMQ_TRUSTED_PROXIES=127.0.0.1/32 # example only: use actual peer seen by PMQ
PMQ_SSO_SIGNING_KEY=<stable-random-secret>
GOOGLE_CLIENT_ID=<secret>
GOOGLE_CLIENT_SECRET=<secret>
CLOUDFLARE_API_TOKEN=<secret>
PMQ_DEPLOY_TOKEN=<secret kept by PMQ and trusted CLI operators>
```

`PMQ_ALLOWED_EMAIL` is an exact email allowlist entry. Duplicate the `transform user` stanza for each additional trusted Google account. The PMQ policy uses the separate `pmq/app-user` role; do not replace it with `authp/user`, because existing providers and transforms commonly assign that generic role to every authenticated user. When reusing an existing portal, audit every user transform, provider, and identity store to verify none grants `pmq/app-user` broadly. Keep that role exclusively for the PMQ email allowlist transforms. An unapproved user may complete Google login, but the PMQ authorization policy denies access unless that identity receives `pmq/app-user`.

Use the same signing key for the existing portal and PMQ's authorization policy. Keep it stable across Caddy reloads and restarts. If the existing portal uses a different token signing key, set the policy verification key to that exact key; do not rotate it without expecting existing sessions to require login again.

Set the portal's cookie domain to `slopper.tanaka.dev.br` (no leading dot), path `/`, SameSite `Lax`, Secure, and a bounded lifetime. The example uses a one-hour access token and cookie. This scope shares the cookie with `hello-world.slopper.tanaka.dev.br` and `another-app.slopper.tanaka.dev.br`, but not with unrelated `*.tanaka.dev.br` sites. If the existing portal serves unrelated apps, preserve their cookie settings and add a PMQ-scoped cookie domain/name configuration instead of widening their existing cookie scope.

The authorization policy and portal must agree on cookie names and signing key. In the example they are `PMQ_SESSION_ID` and `PMQ_ACCESS_TOKEN`. Preserve the original `Host` and `Origin` through Caddy: PMQ uses Host for application routing and requires HTTPS Origin to match Host for browser writes. Same-origin calls to `/_pmq/db/*` work without CORS rules.

## Admin separation

The app wildcard returns 404 for `/_pmq/admin` and its descendants before SSO or PMQ. The separate `pmq-admin.${PMQ_DOMAIN}` site allows requests only from `PMQ_OPERATOR_CIDRS`, strips `X-PMQ-User`, and proxies only admin paths. PMQ independently requires `Authorization: Bearer <PMQ_DEPLOY_TOKEN>`. Therefore app SSO does not authorize deployment or release management. Keep the operator hostname on tailnet/LAN DNS and firewall it from public ingress; if Caddy is behind Cloudflare, a normal `remote_ip` matcher sees Cloudflare's peer address unless you configure and use a verified client-IP matcher.

Point deployment CLI admin calls at `https://pmq-admin.${PMQ_DOMAIN}`. Browser apps use only their own app host and never receive the deploy token.

## Logout and expiration

The shared portal cookie lets the browser use the second PMQ app without another Google prompt while the local access token remains valid. The example's token and cookie expire after one hour; the next protected request returns to the shared portal, and Google may silently complete sign-in if its own session is still active. Use the portal's `/logout` flow to clear the Caddy session cookie for the shared PMQ cookie domain. This does not sign the user out of Google. A copied, already-issued stateless access token may remain usable until its expiry; do not treat cookie deletion as immediate revocation at every protected app. Reducing the token lifetime bounds that window. Portal logout/session APIs and external Google logout are separate features; configure and test them explicitly if immediate revocation is required.

## Validation

Use a Caddy build containing both modules. `caddy validate` checks Caddyfile parsing and module provisioning; it does not contact Google, Cloudflare, or PMQ. The manual matrix below is the acceptance check for the live environment.

| Check | Request/action | Expected |
| --- | --- | --- |
| First app login | In a fresh browser, open `https://hello-world.${PMQ_DOMAIN}/` | Redirect to the shared portal and Google; successful callback returns to app A |
| Second app | In that browser, open `https://another-app.${PMQ_DOMAIN}/` | App B opens without a second Google prompt while the shared token is valid |
| API read | From app A, `fetch('/_pmq/db/notes')` | PMQ JSON response on the same origin |
| API write | From app A, create a document with `fetch('/_pmq/db/notes', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({title:'sso test'})})` | 201 and document; browser sends the matching app Origin automatically |
| Direct PMQ request | From a machine whose TCP address is not in `PMQ_TRUSTED_PROXIES`, request static `/` and `/_pmq/db/notes` directly from PMQ, with no identity header | Both return 401; neither serves assets or data |
| Forged identity | Repeat the direct request with `X-PMQ-User: attacker@example.com` | Still 401 because PMQ checks the TCP peer, not the caller's claim |
| Header spoof through Caddy | In a fresh unauthenticated browser/curl request to an app host, send `X-PMQ-User: attacker@example.com` | Request is redirected/denied by caddy-security; caller header cannot grant access. With an authenticated session, use a temporary header-echo upstream in staging to verify the forwarded value is the authenticated Google email, not the forged value |
| Cross-site write | From a staging browser with an app session, submit a write to the app host from an unrelated origin; confirm the request has `Origin: https://attacker.invalid` | Request is denied (normally 403 `csrf_rejected`) and no document is created. Keep Host and Origin unchanged in Caddy |
| Admin from app SSO | Request `https://hello-world.${PMQ_DOMAIN}/_pmq/admin/apps` after signing into Google | Caddy returns 404; app SSO alone cannot reach admin APIs |
| Admin token | From an allowlisted operator network, request `https://pmq-admin.${PMQ_DOMAIN}/_pmq/admin/apps` without a bearer token, then with the deploy token | First request 401 from PMQ; second succeeds. From a non-allowlisted network, Caddy returns 403 |
| Secret check | Inspect deployed app files, browser devtools network/source, and static Caddy config | No OAuth client secret or PMQ deployment token appears in frontend assets or public responses |

For a direct-peer test, call PMQ from an untrusted machine; localhost requests on a host-network deployment originate from the trusted host itself and are not a valid test of the network boundary. For the CSRF row, use a valid browser session so that the request reaches PMQ; otherwise the auth redirect can mask the Origin check.
