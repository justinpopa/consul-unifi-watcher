# consul-unifi-watcher

A service that watches Consul for services tagged for DNS registration and automatically manages corresponding DNS A records on a UniFi controller. It discovers healthy Traefik instances in Consul, then creates or deletes DNS records in UniFi so that tagged service hostnames resolve to those Traefik IPs.

## Configuration

All configuration is via environment variables.

| Variable | Required | Default | Description |
|---|---|---|---|
| `UNIFI_HOST` | Yes | | UniFi controller hostname or IP |
| `DNS_ZONE` | Yes | | DNS zone to manage (e.g. `home.example.com`) |
| `UNIFI_API_KEY` | * | | UniFi API key (preferred over user/pass) |
| `UNIFI_USER` | * | | UniFi username (required if no API key) |
| `UNIFI_PASS` | * | | UniFi password (required if no API key) |
| `UNIFI_SITE` | No | `default` | UniFi site name |
| `UNIFI_SKIP_TLS` | No | `false` | Skip TLS verification for UniFi |
| `CONSUL_HTTP_ADDR` | No | `http://localhost:8500` | Consul HTTP address |
| `CONSUL_SERVICE_TAG` | No | `dns-register` | Consul service tag to watch |
| `TRAEFIK_SERVICE` | No | `traefik` | Consul service name for Traefik |
| `POLL_INTERVAL` | No | `10s` | How often to reconcile DNS records |
| `DRY_RUN` | No | `false` | Log changes without applying them |
| `HEALTH_ADDR` | No | `:8080` | Address for health/readiness endpoints |

\* Either `UNIFI_API_KEY` or both `UNIFI_USER` and `UNIFI_PASS` must be set.

## Quick start

### Docker

```sh
docker run -e UNIFI_HOST=unifi.local \
           -e UNIFI_API_KEY=your-key \
           -e DNS_ZONE=home.example.com \
           ghcr.io/justinpopa/consul-unifi-watcher:latest
```

### Build from source

```sh
go build -o consul-unifi-watcher .
./consul-unifi-watcher
```

## Health endpoints

- `GET /health` — always returns `200 OK`
- `GET /ready` — returns `200` after the first successful reconciliation, `503` otherwise

## License

[MIT](LICENSE)
