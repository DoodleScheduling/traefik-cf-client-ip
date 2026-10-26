# Traefik Cloudflare client IP

[![release](https://img.shields.io/github/release/DoodleScheduling/traefik-cf-client-ip/all.svg)](https://github.com/DoodleScheduling/traefik-cf-client-ip/releases)
[![report](https://goreportcard.com/badge/github.com/DoodleScheduling/traefik-cf-client-ip)](https://goreportcard.com/report/github.com/DoodleScheduling/traefik-cf-client-ip)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/DoodleScheduling/traefik-cf-client-ip/badge)](https://api.securityscorecards.dev/projects/github.com/DoodleScheduling/traefik-cf-client-ip)
[![Coverage Status](https://coveralls.io/repos/github/DoodleScheduling/traefik-cf-client-ip/badge.svg?branch=master)](https://coveralls.io/github/DoodleScheduling/traefik-cf-client-ip?branch=master)
[![license](https://img.shields.io/github/license/DoodleScheduling/traefik-cf-client-ip.svg)](https://github.com/DoodleScheduling/traefik-cf-client-ip/blob/master/LICENSE)

A Traefik middleware plugin that overwrites `X-Forwarded-For` and `X-Real-Ip` with the visitor IP from Cloudflare's `CF-Connecting-IP` header.

## Why

Behind an AWS NLB with `externalTrafficPolicy: Cluster`, kube-proxy rewrites the source address when traffic enters a node, so Traefik sees the node's internal IP (`100.96.x.x`) as the client. Without trusted IPs, Traefik also drops the incoming `X-Forwarded-For`. Backends that identify the visitor through it, such as Keycloak with `KC_PROXY_HEADERS=xforwarded`, then see the node instead of the visitor.

Cloudflare sets `CF-Connecting-IP` to the visitor's IP on every request, and rejects requests that try to send it. This middleware copies that value into the target headers. Traefik's proxy then appends its own peer address to `X-Forwarded-For`, so the backend receives `<visitor>, <node>` and the first entry is the visitor.

## Behaviour

| Request | Result |
|---|---|
| Exactly one valid IPv4 or IPv6 address in the source header | Target headers are overwritten with the normalised IP |
| Header missing, empty, repeated, a list, with a port, a CIDR, or not an IP | Request passes through unchanged; no empty values are written |
| `trustedIPs` set and the TCP peer is not in it | The source header is removed and the target headers are left as they are |

Only request headers are changed. Responses, the body and other headers are untouched, and websocket upgrades pass through.

## Configuration

| Option | Default | Description |
|---|---|---|
| `sourceHeader` | `CF-Connecting-IP` | Header that carries the visitor IP |
| `targetHeaders` | `X-Forwarded-For`, `X-Real-Ip` | Headers overwritten with that IP |
| `trustedIPs` | empty: every peer is trusted | IPs or CIDRs of the TCP peers allowed to set the source header |

Example:

```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: cf-client-ip
spec:
  plugin:
    cfClientIp:
      trustedIPs:
        - 100.96.0.0/11
```

Use it as an entrypoint middleware, first in the list, so later middlewares already see the visitor IP:

```yaml
ports:
  websecure:
    http:
      middlewares:
        - traefik-cf-client-ip@kubernetescrd
```

## Security notes

- **Who can set the header.** Anything that can reach Traefik without going through your Cloudflare zone can send its own `CF-Connecting-IP`. With `externalTrafficPolicy: Cluster`, the peer Traefik sees is a node address for every connection, so `trustedIPs` cannot tell Cloudflare apart from in-cluster callers. Restrict who reaches the public entrypoint with Cloudflare Authenticated Origin Pulls (mTLS). If Traefik sees Cloudflare's addresses, for example with `externalTrafficPolicy: Local` or PROXY protocol, set `trustedIPs` to Cloudflare's ranges.
- **Traefik's access log** still records the TCP peer as `ClientHost`. A plugin cannot change it. Log the header instead with `accessLog.fields.headers.names.CF-Connecting-IP: keep`.
- **Not handled here, unlike cloudflarewarp:** `CF-Visitor` to `X-Forwarded-Proto` and DNS-based trust. TLS terminates at Traefik, which already sets `X-Forwarded-Proto`, and there are no background goroutines.

## Development

```sh
make test   # fmt, vet, tidy, unit tests with -race and coverage
make lint   # golangci-lint
```
