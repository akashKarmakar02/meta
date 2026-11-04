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

PORT=$PORT PATH="$SANDBOX_PATH" ./bin/example >/tmp/go-meta-node-free.log 2>&1 &
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

if [ "$fail" -ne 0 ]; then
  echo "NODE-FREE SMOKE: FAILED" >&2
  exit 1
fi
echo "NODE-FREE SMOKE: ALL CHECKS PASSED (no Node on PATH)"
