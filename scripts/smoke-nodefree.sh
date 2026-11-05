#!/usr/bin/env bash
# P2-F03 node-free smoke: exercises a production binary with Node/npm
# unreachable on PATH. Usage: smoke-nodefree.sh [--untagged] [binary]
# Default: tagged binary (bin/example-native), expects real image bytes.
# --untagged: default binary (bin/example), expects truthful 501s.
set -u
UNTAGGED=0
if [ "${1:-}" = --untagged ]; then UNTAGGED=1; shift; fi
BIN="${1:-bin/example-native}"
PORT="${SMOKE_PORT:-3199}"
BASE="http://127.0.0.1:$PORT"
export PORT

# Node-free runtime environment. This host ships Node as a system package
# on /usr/bin, so stripping PATH directories is not sufficient. Instead the
# smoke runs with a minimal PATH containing only the utilities the check
# itself needs — simulating a machine without Node installed.
SMOKE_BIN="$(mktemp -d)"
for tool in curl seq sleep rm; do ln -s "$(command -v "$tool")" "$SMOKE_BIN/$tool" 2>/dev/null; done
export PATH="$SMOKE_BIN"
if command -v node >/dev/null 2>&1; then
  echo "FAIL: node still reachable on PATH"; exit 1
fi

"$BIN" >/tmp/smoke-server.log 2>&1 < /dev/null &
SRV=$!
trap 'kill $SRV 2>/dev/null; rm -rf "$SMOKE_BIN"' EXIT
up=1
for _ in $(seq 1 50); do
  curl -s -o /dev/null "$BASE/healthz" && up=0 && break
  sleep 0.2
done
[ "$up" = 0 ] || { echo "FAIL: server did not come up"; exit 1; }

pass=0; fail=0
probe() { # method path want_status [want_ct] [extra header]
  local method="$1" path="$2" want="$3" wantct="${4:-}" hdr="${5:-}"
  local args=(-s -o /tmp/smoke.body -w '%{http_code} %{content_type}')
  if [ "$method" = HEAD ]; then args+=(-I); else args+=(-X "$method"); fi
  [ -n "$hdr" ] && args+=(-H "$hdr")
  local got
  got=$(curl "${args[@]}" "$BASE$path")
  local status=${got%% *}; local ct=${got#* }
  if [ "$status" != "$want" ]; then
    echo "FAIL $method $path: status $status want $want"; fail=$((fail+1)); return
  fi
  if [ -n "$wantct" ] && [[ "$ct" != "$wantct"* ]]; then
    echo "FAIL $method $path: ct '$ct' want prefix '$wantct'"; fail=$((fail+1)); return
  fi
  echo "ok $method $path -> $status ${ct:+($ct)}"
  pass=$((pass+1))
}

# Baseline framework behavior.
probe GET /healthz 200
probe GET / 200 text/html
probe GET /about 200 text/html

# Image optimization core (F03). Tagged binary: real bytes. Untagged
# binary (pass --untagged): endpoint truthfully reports 501.
if [ "$UNTAGGED" = 1 ]; then
  probe GET "/_meta/image?src=%2Fimages%2Fhero.jpg&w=800&f=webp" 501
  probe GET "/_meta/image?src=%2Fimages%2Fhero.jpg&w=800&f=auto" 501
  probe GET "/_meta/image?src=%2Fimages%2Fmissing.jpg&w=100&f=webp" 501
  probe GET "/_meta/image?src=%2Fetc%2Fpasswd&w=1&f=webp" 400
  exit $([ "$fail" = 0 ] && echo 0 || echo 1)
fi
probe GET "/_meta/image?src=%2Fimages%2Fhero.jpg&w=800&f=webp" 200 image/webp
probe GET "/_meta/image?src=%2Fimages%2Fhero.jpg&w=800&f=avif" 200 image/avif
probe GET "/_meta/image?src=%2Fimages%2Fhero.jpg&w=800&f=auto" 200 image/avif "Accept: image/avif,image/webp"
probe GET "/_meta/image?src=%2Fimages%2Fhero.jpg&w=800&f=auto" 200 image/webp "Accept: image/webp"
probe GET "/_meta/image?src=%2Fimages%2Fhero.jpg&w=800&f=jpeg" 200 image/jpeg
probe GET "/_meta/image?src=%2Fimages%2Fhero.jpg&w=800&f=png" 200 image/png
probe HEAD "/_meta/image?src=%2Fimages%2Fhero.jpg&w=800&f=webp" 200 image/webp
probe GET "/_meta/image?src=%2Fimages%2Fmissing.jpg&w=100&f=webp" 404
probe GET "/_meta/image?src=%2Fetc%2Fpasswd&w=1&f=webp" 400

echo "pass=$pass fail=$fail"
[ "$fail" = 0 ]
