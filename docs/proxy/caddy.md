# Caddy reverse proxy

This recipe adds PMQ as an upstream to an **existing Caddy instance**. It does not add another PMQ container or change any other Caddy site. The example uses `*.slopper.tanaka.dev.br`; set `PMQ_DOMAIN` to the domain you control.

The application and operator routes use separate hostnames:

- `APP.PMQ_DOMAIN` authenticates every request through your existing SSO/forward-auth service. `/_pmq/admin/*` is blocked at Caddy on every app hostname.
- `pmq-admin.PMQ_DOMAIN` is a private operator hostname. Caddy allows only configured operator source IPs and forwards only `/_pmq/admin/*`. PMQ still requires its `Authorization: Bearer` deploy token; SSO alone grants no admin access.

Keep the operator hostname on tailnet/LAN DNS and firewall it from public ingress. If you proxy DNS through Cloudflare, `remote_ip` normally sees the Cloudflare edge. In that topology, either keep the operator route off the Cloudflare-proxied listener or configure Caddy's trusted-proxy/client-IP handling correctly and use its verified client IP matcher. Never treat an arbitrary forwarded-IP header as proof of operator identity.

## DNS and wildcard certificate

Create a DNS record for `*.slopper.tanaka.dev.br` pointing at Caddy (and an apex record only if you also serve the apex). Wildcard DNS does not create a certificate. Caddy needs the Cloudflare DNS provider module to complete ACME DNS-01. The existing certificate for `*.tanaka.dev.br` does **not** cover `*.slopper.tanaka.dev.br`: wildcard certificates cover one label below their named zone, and this is a separate, deeper name.

Build or install Caddy with `github.com/caddy-dns/cloudflare`; stock Caddy builds do not contain it. For a reproducible build matching the validation below, use `xcaddy build v2.11.9 --with github.com/caddy-dns/cloudflare@v0.2.4`. The provider's documented module name is `dns.providers.cloudflare`. Give Caddy a scoped Cloudflare API token with Zone:Read and DNS:Edit permissions for the `slopper.tanaka.dev.br` zone, and expose it as `CLOUDFLARE_API_TOKEN`. Keep the token out of the Caddyfile and source control. See the [Cloudflare provider module](https://github.com/caddy-dns/cloudflare) and [Caddy DNS challenge docs](https://caddyserver.com/docs/automatic-https#dns-challenge).

Caddy's automatic HTTPS obtains certificates for each configured site. The wildcard site in [the example Caddyfile](../../examples/caddy/Caddyfile) requires DNS-01; the operator hostname is covered by the same wildcard certificate. ACME issuance is not exercised by config validation because that requires live DNS and Cloudflare credentials.

## Authentication and request routing

Set these environment variables in the Caddy service:

```sh
PMQ_DOMAIN=slopper.tanaka.dev.br
PMQ_UPSTREAM=127.0.0.1:8080
PMQ_AUTH_UPSTREAM=127.0.0.1:4181
PMQ_OPERATOR_CIDRS=100.64.0.0/10
CLOUDFLARE_API_TOKEN=... # secret store, not a committed file
```

`PMQ_AUTH_UPSTREAM` is a forward-auth endpoint that returns success only for an authenticated user and includes one trusted `X-PMQ-User` response header. The Caddy route removes any client-supplied `X-PMQ-User` before auth, then copies that header from the auth response. Adapt the URI and returned identity header to your existing SSO service. Do not proxy application traffic unless the auth check succeeded. PMQ trusts the TCP peer and the configured identity header; it does not trust forwarded client IP headers.

Caddy passes incoming `Host` and `Origin` through to an HTTP upstream by default. PMQ uses the original application `Host` to select the app, and checks the original HTTPS `Origin` on browser writes. Since frontend and `/_pmq/db/*` calls remain on the same app hostname and scheme, no CORS configuration is needed. Do not rewrite either header.

On app hosts, admin paths return 404 before SSO or PMQ. On the operator hostname, non-admin paths return 404 and requests outside `PMQ_OPERATOR_CIDRS` return 403. A request from an allowed operator address still needs PMQ's bearer token. Point CLI admin commands at `https://pmq-admin.${PMQ_DOMAIN}`; browser apps must never receive that token.

## Network layouts

`PMQ_TRUSTED_PROXIES` must be the **actual TCP source address PMQ observes for Caddy**, not the browser, a guessed bridge gateway, or a value copied from `X-Forwarded-For`. Restrict the PMQ listener so other machines cannot connect from that trusted address.

### Caddy and PMQ on the host

Bind PMQ to loopback and let the existing host Caddy connect locally:

```sh
PMQ_LISTEN=127.0.0.1:8080
PMQ_DOMAIN=slopper.tanaka.dev.br
PMQ_TRUSTED_PROXIES=127.0.0.1/32
PMQ_DEPLOY_TOKEN=<secret>
```

Set `PMQ_UPSTREAM=127.0.0.1:8080`. Confirm Caddy's upstream connection uses `127.0.0.1`; if Caddy runs in a separate network namespace, it does not.

### Existing Caddy container and PMQ container

Attach both services to a dedicated user-defined Docker network. Do not publish PMQ's port to the host or public interfaces. Give the Caddy container a stable address on that network and use exactly that address as `PMQ_TRUSTED_PROXIES`; set `PMQ_UPSTREAM` to `pmq:8080` (or the PMQ container's service DNS name). PMQ listens on `0.0.0.0:8080` inside its container. Verify the TCP peer inside the PMQ container before choosing the CIDR: Docker networking, NAT, and proxy namespaces can change the observed address. If the source address is dynamic, assign a stable Caddy address or a narrowly scoped dedicated subnet and prevent unrelated containers from joining it.

A bridge-network sketch for the existing Compose files (adapt names and network ownership; do not expose this network to untrusted containers). In PMQ's current Compose file, remove its loopback `ports` mapping for this topology and keep only `expose`; otherwise the host-published port remains reachable outside the dedicated proxy network.

```yaml
services:
  pmq:
    expose: ["8080"]
    networks:
      pmq_proxy:
        ipv4_address: 172.30.0.2
  caddy:
    networks:
      pmq_proxy:
        ipv4_address: 172.30.0.3
networks:
  pmq_proxy:
    name: pmq_proxy
    ipam:
      config:
        - subnet: 172.30.0.0/24
```

For this example PMQ would use `PMQ_TRUSTED_PROXIES=172.30.0.3/32`. The network must be created/owned by the existing Caddy deployment, and existing Caddy routing must remain intact. This is illustrative only; inspect the peer address in your actual deployment.

## Validate and smoke-test

The Caddyfile is a drop-in site snippet and expects the Cloudflare provider module plus a forward-auth service. Adaptation can be checked without issuing a certificate:

```sh
export PMQ_DOMAIN=slopper.tanaka.dev.br
export PMQ_UPSTREAM=127.0.0.1:8080
export PMQ_AUTH_UPSTREAM=127.0.0.1:4181
export PMQ_OPERATOR_CIDRS=127.0.0.1/32
export CLOUDFLARE_API_TOKEN=0123456789abcdefghijklmnopqrstuvwxyzABCD # token-shaped dummy; no DNS request
caddy validate --config examples/caddy/Caddyfile --adapter caddyfile
```

Use a Caddy binary built with `github.com/caddy-dns/cloudflare`; a stock binary reports that `dns.providers.cloudflare` is not registered. `caddy validate` checks parsing/provisioning but does not contact Cloudflare or the auth service. To test live routes, first deploy `hello-world`, configure the existing SSO service, and run Caddy on HTTPS. From an address included in `PMQ_OPERATOR_CIDRS`, run:

```sh
PMQ_DOMAIN=slopper.tanaka.dev.br PMQ_SMOKE_APP=hello-world \
PMQ_ADMIN_HOST=pmq-admin.slopper.tanaka.dev.br PMQ_PROBE_IP=127.0.0.1 \
  ./scripts/check-caddy-routing.sh
```

Set `PMQ_PROBE_IP` to the Caddy listener address and run from the operator network. The smoke check verifies unauthenticated app rejection/SSO redirect, admin denial on the app hostname, a non-app host 404, and the PMQ bearer-token challenge on the operator host. It does not submit credentials or create data.
