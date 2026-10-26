# Traefik Cloudflare client IP

[![release](https://img.shields.io/github/release/DoodleScheduling/traefik-cf-client-ip/all.svg)](https://github.com/DoodleScheduling/traefik-cf-client-ip/releases)
[![report](https://goreportcard.com/badge/github.com/DoodleScheduling/traefik-cf-client-ip)](https://goreportcard.com/report/github.com/DoodleScheduling/traefik-cf-client-ip)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/DoodleScheduling/traefik-cf-client-ip/badge)](https://api.securityscorecards.dev/projects/github.com/DoodleScheduling/traefik-cf-client-ip)
[![Coverage Status](https://coveralls.io/repos/github/DoodleScheduling/traefik-cf-client-ip/badge.svg?branch=master)](https://coveralls.io/github/DoodleScheduling/traefik-cf-client-ip?branch=master)
[![license](https://img.shields.io/github/license/DoodleScheduling/traefik-cf-client-ip.svg)](https://github.com/DoodleScheduling/traefik-cf-client-ip/blob/master/LICENSE)

A Traefik middleware plugin that overwrites `X-Forwarded-For` and `X-Real-Ip` with the visitor IP from Cloudflare's `CF-Connecting-IP` header.

## Why

Behind an AWS NLB with `externalTrafficPolicy: Cluster`, kube-proxy rewrites the source address when traffic enters a node. Traefik therefore sees the node's internal IP (`100.96.x.x`) as the client, drops the incoming `X-Forwarded-For` and writes the node IP instead. Backends that identify the visitor through these headers, such as Keycloak with `KC_PROXY_HEADERS=xforwarded`, then see the node instead of the visitor.

Cloudflare sets `CF-Connecting-IP` to the visitor's IP on every request. This middleware copies it into `X-Forwarded-For` and `X-Real-Ip`. Traefik's proxy then appends its own peer address to `X-Forwarded-For`, so the backend receives `<visitor>, <node>`.

## Behaviour

| `CF-Connecting-IP` | Result |
|---|---|
| Exactly one valid IPv4 or IPv6 address | `X-Forwarded-For` and `X-Real-Ip` are replaced with the normalised IP; any previous values are removed |
| Missing, empty, repeated, a list, with a port, a CIDR, or not an IP | The request continues unchanged; nothing is rejected and no empty values are written |

Only these two request headers are changed. The middleware has no options.

## Usage

```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: cf-client-ip
spec:
  plugin:
    cfClientIp: {}
```

Put it first in the entrypoint middlewares, so later middlewares already see the visitor IP:

```yaml
ports:
  websecure:
    http:
      middlewares:
        - traefik-cf-client-ip@kubernetescrd
```

## Security

The middleware trusts `CF-Connecting-IP` as it arrives. It does not check where the request came from. Anything that can open a connection to the entrypoint where it runs, without going through Cloudflare, can choose the IP the backends see. That includes in-cluster pods, internal load balancers and NodePorts.

Deploy it only where the connection is authenticated as coming from Cloudflare, for example with Cloudflare Authenticated Origin Pulls (mTLS) using a zone-specific client certificate. Requests on paths that are not authenticated should not carry a usable `CF-Connecting-IP`.

Traefik's access log still records the TCP peer as `ClientHost`. To log the visitor, keep the header in the access log: `accessLog.fields.headers.names.CF-Connecting-IP: keep`.

## Development

```sh
make test              # fmt, vet, tidy, unit tests with -race and coverage
make lint              # golangci-lint
make yaegi-test        # unit tests in the Yaegi interpreter, as Traefik runs the plugin
make integration-test  # the plugin inside a real Traefik binary (TRAEFIK_VERSION, default v3.7.10)
```
