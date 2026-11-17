# Private ingress and internal DNS

PMQ is for a trusted group. DNS controls name resolution, not network access: a public DNS record, an unguessable name, a wildcard certificate, or SSO alone does not make a service private. Restrict the listener and network paths first. The [Caddy recipe](caddy.md) enforces the application and operator authentication boundaries.

## Topology

Choose a private address for the existing Caddy listener: for example `192.168.10.10` on the LAN and `100.101.102.103` on the tailnet. Replace these examples everywhere. Permit HTTPS from trusted LAN and tailnet interfaces only, and keep PMQ itself bound to loopback or an isolated Docker network reachable solely by Caddy. Do not enable Tailscale Funnel or forward public router ports to Caddy or PMQ. If Caddy also serves public sites, use a separate private listener or enforce equivalent source restrictions **before** the PMQ site routes; DNS alone cannot separate traffic reaching a shared listener. Audit IPv6 as well as IPv4.

The repository Compose file binds PMQ to `127.0.0.1:8080` on the host. Validate the resolved configuration, including overrides:

```sh
PMQ_DOMAIN=apps.example.test PMQ_DEPLOY_TOKEN=test-only PMQ_TRUSTED_PROXIES=127.0.0.1/32 \
  docker compose config
```

For container-to-container proxying use the dedicated network in the [Caddy guide](caddy.md), remove the host `ports` mapping, and set `PMQ_TRUSTED_PROXIES` to the **observed** Caddy peer IP or the narrowest dedicated proxy subnet. A broad Docker gateway or CIDR can allow other containers on that network to forge the identity header. Do not trust `X-Forwarded-For` for proxy identity. Exclude untrusted containers and direct clients from any trusted proxy network. Test that a direct connection without a validated trusted peer and identity is rejected for both static and API requests.

## Split DNS and certificates

1. In your authoritative public DNS zone, configure the DNS-01 credentials for Caddy as described in the [Caddy guide](caddy.md). DNS-01 works without exposing HTTP or HTTPS for the ACME challenge. A public A/AAAA record is not required for DNS-01; remove any public records that point PMQ at a public listener unless deliberately needed for a separate purpose.
2. In your LAN DNS server (for example AdGuard Home DNS rewrites), configure `*.apps.example.test` to resolve to the private LAN Caddy address and an explicit `pmq-admin.apps.example.test` record to the same private address. If the resolver does not support wildcard rewrites, create explicit records for each app and admin host, or use an authoritative private wildcard zone. Use a domain you control for a publicly trusted certificate; `.test` is for examples only.
3. Configure Tailscale split DNS for your PMQ domain to use a tailnet-reachable DNS server that serves the wildcard and operator records. The resolver must return a tailnet-reachable Caddy address to tailnet clients; use DNS views or a separate tailnet resolver if LAN and tailnet addresses differ. Check the resolver's wildcard behavior and Tailscale DNS settings on actual clients. Restrict the DNS service itself to trusted LAN/tailnet clients.
4. Configure Caddy's Cloudflare DNS module and scoped token through secrets. A certificate for `*.example.com` does not cover `*.apps.example.com`; request the appropriate wildcard. Do not commit credentials.

## Firewall and listener checks

Restrict Caddy's PMQ listener to the relevant LAN and tailnet interfaces or addresses. Apply corresponding ingress firewall rules for TCP 443 only from those networks; deny public ingress, and keep SSH and other existing access intact. Docker-published ports can bypass ordinary UFW rules via Docker's own chains: use loopback-only publishing, dedicated networks, and host firewall rules appropriate to your Docker setup, including IPv6. Consult the firewall's rules before applying changes; do not apply a blanket example remotely without an out-of-band recovery path. Verify no router NAT, public cloud firewall, or Tailscale Funnel exposes PMQ. The example Compose file does not publish port 443 and binds 8080 to loopback only.

From the server, inspect listeners and effective container port bindings:

```sh
ss -lntp
sudo nft list ruleset
sudo ufw status verbose
PMQ_DOMAIN=apps.example.test PMQ_DEPLOY_TOKEN=test-only PMQ_TRUSTED_PROXIES=127.0.0.1/32 docker compose ps
```

Use the firewall tool actually deployed on the host. Check Docker chains, router forwarding and external firewall policy separately.

## Verify from each network

From a LAN client and a tailnet client respectively, query the intended resolver and test a deployed app. Replace all sample names and addresses:

```sh
nslookup hello.apps.example.com LAN_OR_TAILNET_DNS_IP
curl -v https://hello.apps.example.com/
curl -v https://hello.apps.example.com/_pmq/db/notes
```

Unauthenticated requests should redirect to SSO or return 401/403; after sign-in, static content and the API should both work. From a client outside the LAN and tailnet (for example mobile data with VPN disabled), test IPv4 and IPv6 paths to the actual public addresses, bypassing local DNS if necessary:

```sh
curl -4 --connect-timeout 5 --resolve hello.apps.example.com:443:PUBLIC_IPV4 https://hello.apps.example.com/
curl -6 --connect-timeout 5 --resolve 'hello.apps.example.com:443:[PUBLIC_IPV6]' https://hello.apps.example.com/
```

Both attempts must fail to reach a PMQ response. A timeout, refused connection, or explicit network firewall denial can be acceptable; a login page or PMQ response proves the listener is reachable. Run from a genuinely external network, not the PMQ host or a VPN client. Check the admin hostname and direct PMQ port as well. In the actual environment, record the source, destination, address family, command and observed result; do not infer a successful firewall test from configuration alone.

To check PMQ's own proxy boundary, from a non-proxy host on a controlled test network try a direct request with a forged identity header against the actual PMQ listener and app host; it must be blocked at the network layer or return 401, for static content and `/_pmq/db/notes`. Never trust a header as proof of proxy origin. These network and SSO tests require access to the real deployment and are not performed by the repository's local tests.
