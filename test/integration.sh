#!/usr/bin/env bash
# Runs the plugin inside a real Traefik (as a local plugin, interpreted by Yaegi like in
# production) in front of a backend that echoes the client IP headers it receives.
#   TRAEFIK_VERSION=v3.7.10 ./test/integration.sh
set -euo pipefail

TRAEFIK_VERSION=${TRAEFIK_VERSION:-v3.7.10}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null || true; rm -rf "$WORK"' EXIT

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m); [[ $arch == x86_64 ]] && arch=amd64; [[ $arch == aarch64 ]] && arch=arm64
tarball=traefik_${TRAEFIK_VERSION}_${os}_${arch}.tar.gz
base=https://github.com/traefik/traefik/releases/download/${TRAEFIK_VERSION}
curl -fsSL -o "$WORK/$tarball" "$base/$tarball"
curl -fsSL -o "$WORK/checksums.txt" "$base/traefik_${TRAEFIK_VERSION}_checksums.txt"
(cd "$WORK" && grep " ${tarball}\$" checksums.txt | shasum -a 256 -c - >/dev/null)
tar -xzf "$WORK/$tarball" -C "$WORK" traefik

plugin=$WORK/plugins-local/src/github.com/DoodleScheduling/traefik-cf-client-ip
mkdir -p "$plugin"
cp "$ROOT/go.mod" "$ROOT/main.go" "$ROOT/.traefik.yml" "$plugin/"
cp "$ROOT/test/traefik.yml" "$ROOT/test/dynamic.yml" "$WORK/"

python3 "$ROOT/test/echo.py" 18090 &
(cd "$WORK" && ./traefik --configFile=traefik.yml >traefik.log 2>&1) &
for _ in $(seq 1 60); do curl -fs -o /dev/null http://127.0.0.1:18093/ && break; sleep 0.5; done

failed=0
check() {
  local name=$1 want=$2 got
  shift 2
  got=$(curl -fs "$@")
  if [[ $got == "$want" ]]; then
    echo "ok   $name"
  else
    echo "FAIL $name"; echo "     want $want"; echo "     got  $got"; failed=1
  fi
}

check "without plugin, Traefik writes its peer" \
  '{"Cf-Connecting-Ip": ["198.51.100.7"], "X-Forwarded-For": ["127.0.0.1"], "X-Real-Ip": ["127.0.0.1"]}' \
  http://127.0.0.1:18093/ -H 'CF-Connecting-IP: 198.51.100.7'
check "visitor first, Traefik appends its peer" \
  '{"Cf-Connecting-Ip": ["198.51.100.7"], "X-Forwarded-For": ["198.51.100.7, 127.0.0.1"], "X-Real-Ip": ["198.51.100.7"]}' \
  http://127.0.0.1:18091/ -H 'CF-Connecting-IP: 198.51.100.7'
check "client-chosen X-Forwarded-For is replaced" \
  '{"Cf-Connecting-Ip": ["198.51.100.7"], "X-Forwarded-For": ["198.51.100.7, 127.0.0.1"], "X-Real-Ip": ["198.51.100.7"]}' \
  http://127.0.0.1:18091/ -H 'CF-Connecting-IP: 198.51.100.7' -H 'X-Forwarded-For: 203.0.113.77'
check "IPv6 and lowercase header name" \
  '{"Cf-Connecting-Ip": ["2001:db8::1"], "X-Forwarded-For": ["2001:db8::1, 127.0.0.1"], "X-Real-Ip": ["2001:db8::1"]}' \
  http://127.0.0.1:18091/ -H 'cf-connecting-ip: 2001:db8::1'
check "no header: unchanged" \
  '{"Cf-Connecting-Ip": null, "X-Forwarded-For": ["127.0.0.1"], "X-Real-Ip": ["127.0.0.1"]}' \
  http://127.0.0.1:18091/
check "invalid header: unchanged" \
  '{"Cf-Connecting-Ip": ["nope"], "X-Forwarded-For": ["127.0.0.1"], "X-Real-Ip": ["127.0.0.1"]}' \
  http://127.0.0.1:18091/ -H 'CF-Connecting-IP: nope'
check "repeated header: unchanged" \
  '{"Cf-Connecting-Ip": ["198.51.100.7", "203.0.113.1"], "X-Forwarded-For": ["127.0.0.1"], "X-Real-Ip": ["127.0.0.1"]}' \
  http://127.0.0.1:18091/ -H 'CF-Connecting-IP: 198.51.100.7' -H 'CF-Connecting-IP: 203.0.113.1'
check "untrusted peer: header removed" \
  '{"Cf-Connecting-Ip": null, "X-Forwarded-For": ["127.0.0.1"], "X-Real-Ip": ["127.0.0.1"]}' \
  http://127.0.0.1:18092/ -H 'CF-Connecting-IP: 198.51.100.7'

if grep -q -i -E 'panic|level=error|ERR' "$WORK/traefik.log"; then
  echo "FAIL Traefik log has errors:"; grep -i -E 'panic|level=error|ERR' "$WORK/traefik.log"; failed=1
fi
exit $failed
