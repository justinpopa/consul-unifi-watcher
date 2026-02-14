Build a lightweight Go service that synchronizes Consul service catalog entries to Unifi static DNS records.

## Context
- Runs as a container in a Nomad cluster
- Consul is the service discovery backend for Traefik (HTTP reverse proxy)
- All HTTP services behind Traefik share a single anycast VIP
- Unifi gateway is authoritative for the `home.jpopa.com` DNS zone
- Unifi runs dnsmasq under the hood — no RFC 2136, no wildcard support
- The only way to manage Unifi DNS records programmatically is via the Unifi controller REST API

## Requirements

### Core loop
1. Poll Consul catalog for services with a configurable tag (e.g. `dns-register`)
2. For each tagged service, read a meta key or tag for the desired FQDN (e.g. `dns-name=jellyfin.home.jpopa.com`)
3. Query Unifi controller API for current static DNS records in the managed zone
4. Diff the two sets:
   - Service in Consul but no DNS record → create record pointing to Traefik VIP
   - DNS record exists but service gone from Consul → remove record
   - Record exists and matches → no-op
5. Sleep for a configurable interval (default 10s), repeat

### Unifi API integration
- Authenticate to Unifi controller (support both API key and username/password)
- Create/delete static DNS host records via the controller API
- Reference https://github.com/ubiquiti-community/external-dns-unifi-webhook for Unifi API patterns (auth flow, endpoints, payload format)
- Only manage records that the watcher created — use a description field or naming convention to avoid touching manually created records (e.g. set description to `managed:consul-dns-watcher`)

### Configuration (env vars)
- `CONSUL_HTTP_ADDR` — Consul address (default `http://localhost:8500`)
- `CONSUL_SERVICE_TAG` — tag to filter services (default `dns-register`)
- `UNIFI_HOST` — Unifi controller URL
- `UNIFI_USER` / `UNIFI_PASS` — credentials (or `UNIFI_API_KEY`)
- `UNIFI_SITE` — Unifi site name (default `default`)
- `TRAEFIK_VIP` — IP address all HTTP service records should point to
- `DNS_ZONE` — zone suffix (default `home.jpopa.com`)
- `POLL_INTERVAL` — reconciliation loop interval (default `10s`)
- `DRY_RUN` — log changes without applying (default `false`)

### Operational
- Structured JSON logging (slog)
- Log every create/delete action with service name and FQDN
- Graceful shutdown on SIGTERM/SIGINT
- Health check endpoint on `/health` for Nomad
- Minimal dependencies — stdlib + official Consul API client only, no frameworks
- Dockerfile with multi-stage build

### Non-goals
- No HTTPS certificate management
- No Consul Connect / service mesh integration
- No support for non-A records (no CNAME, SRV, etc.)
- No Consul KV usage — service catalog only
- Don't manage VIP records for non-HTTP services (TFTP, DNS, etc.) — those are static
