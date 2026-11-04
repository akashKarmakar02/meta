#!/usr/bin/env bash
# Node-free production smoke test (P2-C06 §21).
#
# Runs the production binary against the prebuilt dist/ assets with an
# deliberately Node-free PATH, then probes the endpoints Phase C depends on.
# Any non-2xx/expected status fails the script. Requires: make build first.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ ! -x bin/example ]; then
  echo "FAIL: bin/example missing — run 'make build' first" >&2
  exit 1
fi
if [ ! -d dist/client ]; then
  echo "FAIL: dist/client missing — run 'make build' first" >&2
  exit 1
fi

PORT=${NODE_FREE_PORT:-3141}
BASE="http://127.0.0.1:${PORT}"

# A PATH with no Node, npm, npx, or corepack. Build it from a scratch
# directory so the test works even where /usr/bin holds node itself: the
# server binary needs only its own runtime libs (statically resolved) plus
# basic file utilities, which we provide via curated symlinks.
SANDBOX_PATH="$(mktemp -d)"
for tool in sh bash env cat grep sed awk sleep seq kill curl mktemp; do
  for dir in /usr/bin /bin; do
    if [ -x "$dir/$tool" ]; then
      ln -sf "$dir/$tool" "$SANDBOX_PATH/$tool"
      break
    fi
  done
done
export PATH="$SANDBOX_PATH:$PATH" # scratch dir first; nothing else resolvable
if command -v node >/dev/null 2>&1; then
  if command -v node >/dev/null 2>&1 && ! [ "$(command -v node)" = "" ]; then
    node_path="$(command -v node || true)"
    # Verify node is NOT reachable under the sandbox-only lookup.
    if PATH="$SANDBOX_PATH" command -v node >/dev/null 2>&1; then
      echo "FAIL: node still reachable under sandbox PATH" >&2
      rm -rf "$SANDBOX_PATH"
      exit 1
    fi
  fi
fi
cleanup() {
  if [ -n "${SERVER_PID:-}" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "${SANDBOX_PATH:-}"
}
trap cleanup EXIT

cleanup() {
  if [ -n "${SERVER_PID:-}" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
}

# D06: the action endpoint trusts an explicit origin only. The smoke
# server serves on 127.0.0.1, so configure it as the trusted origin the
# way a production deployment would.
PORT=$PORT GO_META_TRUSTED_ORIGIN="$BASE" PATH="$SANDBOX_PATH" \
  ./bin/example >/tmp/go-meta-node-free.log 2>&1 &
SERVER_PID=$!
# Note: curl runs with the normal PATH — it tests the server over HTTP;
# only the SERVER must be Node-free.

# Bounded readiness poll (no fixed sleeps).
for _ in $(seq 1 100); do
  if curl -fsS -o /dev/null "$BASE/health" 2>/dev/null; then
    break
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "FAIL: server exited during startup:" >&2
    cat /tmp/go-meta-node-free.log >&2
    exit 1
  fi
  sleep 0.1
done

fail=0
check() { # label expected_status path [curl args...]
  local label="$1" want="$2" path="$3"
  shift 3
  local got
  got=$(curl -s -o /dev/null -w '%{http_code}' "$@" "$BASE$path")
  if [ "$got" != "$want" ]; then
    echo "FAIL: $label: GET $path -> $got (want $want)" >&2
    fail=1
  else
    echo "ok:   $label: GET $path -> $got"
  fi
}

# Health endpoint.
check health 200 /healthz

# Static SSR loader route: posts/[postId] renders typed loader data.
code=$(curl -s "$BASE/posts/7" | grep -c "Example Post" || true)
if [ "$code" -lt 1 ]; then
  echo "FAIL: static SSR loader data missing on /posts/7" >&2
  fail=1
else
  echo "ok:   SSR loader data rendered on /posts/7"
fi

# Dynamic (catch-all) SSR loader route.
code=$(curl -s "$BASE/docs/intro" | grep -c 'data-go-page-docs-intro' || true)
if [ "$code" -lt 1 ]; then
  echo "FAIL: dynamic SSR route /docs/intro did not render" >&2
  fail=1
else
  echo "ok:   dynamic SSR route /docs/intro renders"
fi

# CSR loader initial data: shell embeds the bootstrap JSON.
code=$(curl -s "$BASE/dashboard/team-x" | grep -c '"greeting":"Loaded by Go"' || true)
if [ "$code" -lt 1 ]; then
  echo "FAIL: CSR initial loader data missing on /dashboard/team-x" >&2
  fail=1
else
  echo "ok:   CSR initial loader data embedded on /dashboard/team-x"
fi

# CSR navigation envelope: optional data field present.
code=$(curl -s -H 'X-Go-Web-Navigation: 1' "$BASE/dashboard/team-x" | grep -c '"data":{"teamId":"team-x"' || true)
if [ "$code" -lt 1 ]; then
  echo "FAIL: navigation envelope data missing for /dashboard/team-x" >&2
  fail=1
else
  echo "ok:   navigation envelope carries loader data for /dashboard/team-x"
fi

# Error status maps through.
check loader-error-404 404 /c05/notFound

# Redirect status maps through.
got=$(curl -s -o /dev/null -w '%{http_code} %{redirect_url}' "$BASE/c05/redirectSsr")
case "$got" in
  "302 "*) echo "ok:   redirect /c05/redirectSsr -> $got" ;;
  *) echo "FAIL: redirect /c05/redirectSsr -> $got (want 302 + Location)" >&2; fail=1 ;;
esac

# Asset serving.
asset=$(curl -s "$BASE/" | grep -o 'src="[^"]*\.js"' | head -1 | sed 's/src="//; s/"//')
if [ -z "$asset" ]; then
  echo "FAIL: no client asset referenced on /" >&2
  fail=1
else
  check "client asset $asset" 200 "$asset"
fi

# D06: the full action system works with no Node on PATH. Obtain a CSRF
# cookie + token from the form page, then exercise all three transports.
csrf_cookie=$(curl -s -c - "$BASE/guestbook" | grep _meta_csrf | awk '{print $NF}')
if [ -z "$csrf_cookie" ]; then
  echo "FAIL: guestbook page issued no CSRF cookie" >&2
  fail=1
else
  # JSON action: success envelope (Origin required).
  got=$(curl -s -o /tmp/d06-json-body -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' -H "Origin: $BASE" -H "Host: localhost:$PORT" \
    --data '{"title":"node free"}' "$BASE/_meta/actions/demo.createPost")
  if [ "$got" != "200" ]; then
    echo "FAIL: JSON action success -> $got (want 200)" >&2
    fail=1
  else
    echo "ok:   JSON action success -> 200"
  fi
  # JSON action: structured validation 422 (an empty guestbook name fails
  # the typed validator with a structured response).
  got=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' -H "Origin: $BASE" \
    --data '{"name":" ","message":"x"}' "$BASE/_meta/actions/guestbook.submit")
  if [ "$got" != "422" ]; then
    echo "FAIL: JSON action validation -> $got (want 422)" >&2
    fail=1
  else
    echo "ok:   JSON action validation -> 422"
  fi
  # Native form POST: 303 PRG on valid CSRF double-submit.
  got=$(curl -s -o /dev/null -w '%{http_code} %{redirect_url}' -X POST \
    -H 'Content-Type: application/x-www-form-urlencoded' -H "Origin: $BASE" \
    -H "Cookie: _meta_csrf=$csrf_cookie" \
    --data-urlencode "_csrf=$csrf_cookie" --data-urlencode "name=Node" --data-urlencode "message=free of node" \
    "$BASE/_meta/actions/guestbook.submit")
  case "$got" in
    "303 "*) echo "ok:   native form POST -> $got" ;;
    *) echo "FAIL: native form POST -> $got (want 303 + Location)" >&2; fail=1 ;;
  esac
  # Enhanced form POST: JSON 200 with the marker header.
  got=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/x-www-form-urlencoded' -H "Origin: $BASE" \
    -H 'X-Go-Meta-Action: 1' -H "Cookie: _meta_csrf=$csrf_cookie" \
    --data-urlencode "_csrf=$csrf_cookie" --data-urlencode "name=Enhanced" --data-urlencode "message=node free" \
    "$BASE/_meta/actions/guestbook.submit")
  if [ "$got" != "200" ]; then
    echo "FAIL: enhanced form POST -> $got (want 200)" >&2
    fail=1
  else
    echo "ok:   enhanced form POST -> 200"
  fi
  # Trusted-origin rejection: cross-origin Origin fails closed.
  got=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' -H 'Origin: https://evil.example' \
    --data '{"title":"x"}' "$BASE/_meta/actions/demo.createPost")
  if [ "$got" != "403" ]; then
    echo "FAIL: cross-origin action -> $got (want 403)" >&2
    fail=1
  else
    echo "ok:   cross-origin action rejected -> 403"
  fi
  # Invalid CSRF rejection: tampered token fails closed.
  got=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/x-www-form-urlencoded' -H "Origin: $BASE" \
    -H 'X-Go-Meta-Action: 1' -H "Cookie: _meta_csrf=tampered" \
    --data-urlencode "_csrf=tampered" --data-urlencode "name=X" --data-urlencode "message=Y" \
    "$BASE/_meta/actions/guestbook.submit")
  if [ "$got" != "403" ]; then
    echo "FAIL: tampered CSRF -> $got (want 403)" >&2
    fail=1
  else
    echo "ok:   tampered CSRF rejected -> 403"
  fi
fi

if [ "$fail" -ne 0 ]; then
  echo "NODE-FREE SMOKE: FAILED" >&2
  exit 1
fi
echo "NODE-FREE SMOKE: ALL CHECKS PASSED (no Node on PATH)"
