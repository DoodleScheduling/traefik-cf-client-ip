# Traefik Cloudflare client IP

[![release](https://img.shields.io/github/release/DoodleScheduling/traefik-cf-client-ip/all.svg)](https://github.com/DoodleScheduling/traefik-cf-client-ip/releases)
[![report](https://goreportcard.com/badge/github.com/DoodleScheduling/traefik-cf-client-ip)](https://goreportcard.com/report/github.com/DoodleScheduling/traefik-cf-client-ip)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/DoodleScheduling/traefik-cf-client-ip/badge)](https://api.securityscorecards.dev/projects/github.com/DoodleScheduling/traefik-cf-client-ip)
[![Coverage Status](https://coveralls.io/repos/github/DoodleScheduling/traefik-cf-client-ip/badge.svg?branch=master)](https://coveralls.io/github/DoodleScheduling/traefik-cf-client-ip?branch=master)
[![license](https://img.shields.io/github/license/DoodleScheduling/traefik-cf-client-ip.svg)](https://github.com/DoodleScheduling/traefik-cf-client-ip/blob/master/LICENSE)

A Traefik middleware plugin that sets `X-Forwarded-For` and `X-Real-Ip` to the visitor IP from Cloudflare's `CF-Connecting-IP` header.

## Why

When Traefik runs behind Cloudflare and one or more load balancers or proxies, the address it sees for a connection is often not the visitor's. It can be a Cloudflare edge address, a load balancer address or an internal address. Traefik then writes that address into `X-Forwarded-For` and `X-Real-Ip`, and applications that rely on these headers (logging, rate limiting, auditing, login protection) see the wrong client.

Cloudflare always sends the visitor's IP in `CF-Connecting-IP`. This middleware copies it into `X-Forwarded-For` and `X-Real-Ip`, so applications get the visitor IP without any change on their side.

## Behaviour

| `CF-Connecting-IP` | Result |
|---|---|
| Exactly one valid IPv4 or IPv6 address | `X-Forwarded-For` and `X-Real-Ip` are replaced with the normalised IP; previous values are removed |
| Missing, empty, repeated, a list, with a port, a CIDR, or not an IP | The request continues unchanged; nothing is rejected and no empty values are written |

Traefik's proxy appends the address of its own peer to `X-Forwarded-For` afterwards, so the application receives:

```text
X-Forwarded-For: 198.51.100.7, 10.0.0.10
X-Real-Ip: 198.51.100.7
```

Only these two request headers are changed. The middleware has no options.

## Usage

Enable the plugin:

```yaml
# static configuration
experimental:
  plugins:
    cfClientIp:
      moduleName: github.com/DoodleScheduling/traefik-cf-client-ip
      version: v0.0.1
```

Define the middleware:

```yaml
# dynamic configuration (file provider)
http:
  middlewares:
    cf-client-ip:
      plugin:
        cfClientIp: {}
```

Or, with the Kubernetes CRD provider:

```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: cf-client-ip
spec:
  plugin:
    cfClientIp: {}
```

Apply it to the routers that receive Cloudflare traffic, or to the whole entrypoint. Put it first, so later middlewares already see the visitor IP:

```yaml
# static configuration
entryPoints:
  websecure:
    address: ":443"
    http:
      middlewares:
        - cf-client-ip@file
```

## Security

The middleware trusts `CF-Connecting-IP` as it arrives and does not check where the request came from. Anything that can reach the entrypoint without going through Cloudflare can choose the IP your applications see.

Only use it where connections are known to come from Cloudflare, for example:

- restrict the entrypoint to Cloudflare's IP ranges, or
- require Cloudflare's [Authenticated Origin Pulls](https://developers.cloudflare.com/ssl/origin-configuration/authenticated-origin-pull/) client certificate (mTLS), preferably with a certificate specific to your zone.

Traefik's access log keeps recording the connection's peer as `ClientHost`. To log the visitor, keep the header in the access log: `accessLog.fields.headers.names.CF-Connecting-IP: keep`.

## Development

```sh
make test              # fmt, vet, tidy, unit tests with -race and coverage
make lint              # golangci-lint
make yaegi-test        # unit tests in the Yaegi interpreter, as Traefik runs the plugin
make integration-test  # the plugin inside a real Traefik binary (TRAEFIK_VERSION, default v3.7.10)
```
