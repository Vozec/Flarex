#!/usr/bin/env bash
# bench_local.sh — local SOCKS5 bench via mockworker (no real CF).
#
# Pipeline:
#   python3 -m http.server (target)  →  mockworker (fake CF Worker)  ←  flarex (SOCKS5 :1080)
#   curl --socks5-hostname localhost:1080 http://localhost:8000/
#
# Usage: ./scripts/bench_local.sh [N concurrent] [M requests]

set -euo pipefail

N_CONCURRENT="${1:-50}"
N_REQ="${2:-500}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/bin/flarex"
MOCK="$ROOT/bin/mockworker"
SECRET="benchsecret"
CONFIG="/tmp/cft-bench.yaml"
DB="/tmp/cft-bench.db"

cleanup() {
  pkill -P $$ 2>/dev/null || true
  rm -f "$DB"
}
trap cleanup EXIT

echo "[*] Build..."
make -C "$ROOT" build >/dev/null

rm -f "$DB"
cat > "$CONFIG" <<EOF
log:
  level: warn
listen:
  socks5: "tcp://127.0.0.1:11080"
worker:
  name_prefix: "mock-"
  count: 0
filter:
  allow_ports: [80, 443, 8000, 8080, 8443]
pool:
  strategy: round_robin
  max_retries: 2
  backoff_ms: 20
admin:
  addr: "127.0.0.1:19090"
state:
  path: "$DB"
security:
  hmac_secret: "$SECRET"
EOF

echo "[*] Start target (python http.server :8001)..."
python3 -m http.server 8001 --bind 127.0.0.1 >/tmp/bench-target.log 2>&1 &
sleep 0.5

echo "[*] Start mockworker :8787..."
MOCK_HMAC_SECRET="$SECRET" "$MOCK" --addr 127.0.0.1:8787 >/tmp/bench-mock.log 2>&1 &
sleep 0.3

echo "[*] Seed state..."
"$BIN" -c "$CONFIG" seed --name mock --url http://127.0.0.1:8787 >/dev/null

echo "[*] Start flarex SOCKS5 :11080..."
"$BIN" -c "$CONFIG" serve >/tmp/bench-proxy.log 2>&1 &
sleep 0.5

echo "[*] Warmup..."
curl -sf --socks5-hostname 127.0.0.1:11080 http://127.0.0.1:8001/ >/dev/null || { echo "warmup FAIL"; cat /tmp/bench-proxy.log; exit 1; }
echo "[+] Warmup OK"

echo "[*] Bench: $N_REQ requests, $N_CONCURRENT concurrent..."
START=$(date +%s.%N)
seq 1 "$N_REQ" | xargs -n1 -P "$N_CONCURRENT" -I{} \
  curl -s -o /dev/null -w "%{http_code}\n" \
    --socks5-hostname 127.0.0.1:11080 http://127.0.0.1:8001/ \
  | sort | uniq -c
END=$(date +%s.%N)
ELAPSED=$(awk "BEGIN{print $END-$START}")
RPS=$(awk "BEGIN{print $N_REQ/$ELAPSED}")

echo
echo "=================================="
echo "  N_REQ       : $N_REQ"
echo "  CONCURRENT  : $N_CONCURRENT"
echo "  ELAPSED     : ${ELAPSED}s"
echo "  RPS         : $RPS"
echo "=================================="
echo
echo "=== Admin /status (worker summary) ==="
curl -s http://127.0.0.1:19090/status | head -c 500
echo
echo
echo "=== Metrics (extraits) ==="
curl -s http://127.0.0.1:19090/metrics | grep -E "^cft_(connections|dial|request)" | head -20
