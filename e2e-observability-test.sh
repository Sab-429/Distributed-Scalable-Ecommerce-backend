#!/usr/bin/env bash
# =============================================================================
# e2e-observability-test.sh — Drive the full request path and emit telemetry:
#
#   Client → API Gateway → Auth / Product / Order Service → gRPC →
#   RabbitMQ → Payment Service → Notification Worker
#
# Run after ./start.sh and the observability stack are up.
#   ./e2e-observability-test.sh
# =============================================================================
set -euo pipefail

BASE="http://localhost:8000"
STAMP=$(date +%s)
EMAIL="oteluser-${STAMP}@example.com"
PASS='Password123!'

bold() { printf '\n\033[1m%s\033[0m\n' "$*"; }
step() { printf '\033[0;36m→\033[0m %s\n' "$*"; }
ok()   { printf '\033[0;32m✓\033[0m %s\n' "$*"; }

json() { python3 -c "import sys,json;d=json.load(sys.stdin);print(d$1)"; }

bold "=== E2E observability test ($(date)) ==="

step "1/9 GET /health (gateway)"
curl -s -m 10 "$BASE/health" | head -c 300; echo
ok "health"

step "2/9 Register user $EMAIL"
REG=$(curl -s -m 15 -X POST "$BASE/api/v1/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"OTel Tester\",\"email\":\"$EMAIL\",\"password\":\"$PASS\"}")
echo "$REG" | head -c 300; echo

step "3/9 Login"
LOGIN=$(curl -s -m 15 -X POST "$BASE/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASS\"}")
echo "$LOGIN" | head -c 300; echo
TOKEN=$(echo "$LOGIN" | json "['access_token']")
[ -n "$TOKEN" ] || { echo "FATAL: no access token"; exit 1; }
ok "token acquired"

step "4/9 GET /me (auth via gateway)"
curl -s -m 10 "$BASE/api/v1/auth/me" -H "Authorization: Bearer $TOKEN" | head -c 300; echo

step "5/9 Promote $EMAIL to admin (Postgres) and re-login"
PGPASSWORD='Sabya@#429' psql -h localhost -U postgres -d auth_db \
  -c "UPDATE users SET role='admin' WHERE email='${EMAIL}';" 2>&1 | tail -2
LOGIN2=$(curl -s -m 15 -X POST "$BASE/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASS\"}")
ADMIN_TOKEN=$(echo "$LOGIN2" | json "['access_token']")
[ -n "$ADMIN_TOKEN" ] || { echo "FATAL: no admin token"; exit 1; }
ok "admin token acquired"

step "6/9 Create product (admin)"
PROD=$(curl -s -m 15 -X POST "$BASE/api/v1/products" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"OTel Widget ${STAMP}\",\"description\":\"created by e2e observability test\",\"price\":42.50,\"stock\":100,\"category\":\"electronics\"}")
echo "$PROD" | head -c 300; echo
PID=$(echo "$PROD" | json "['id']" 2>/dev/null || echo "")
ok "product id=${PID:-?}"

step "7/9 List products x3 (gateway cache hit/miss + Redis)"
for i in 1 2 3; do
  curl -s -m 10 -o /dev/null -w "  attempt $i → HTTP %{http_code}, X-Cache: %{header_json}\n" \
    "$BASE/api/v1/products" -H "Authorization: Bearer $TOKEN" 2>/dev/null \
    | sed 's/{"[^"]*":"[^"]*",*}/[hdr]/g'
done

step "8/9 Create order (gRPC to product-service → outbox → RabbitMQ)"
ORDER=$(curl -s -m 20 -X POST "$BASE/api/v1/orders" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"items\":[{\"product_id\":${PID:-1},\"quantity\":2}]}")
echo "$ORDER" | head -c 400; echo
OID=$(echo "$ORDER" | json "['id']" 2>/dev/null || echo "")
ok "order id=${OID:-?}"

step "9/9 Pay (payment-service consumes order.created)"
sleep 3
if [ -n "${OID:-}" ]; then
  PAYMENTS=$(curl -s -m 15 "$BASE/api/v1/payments" -H "Authorization: Bearer $TOKEN")
  echo "$PAYMENTS" | head -c 400; echo
  PAY_ID=$(echo "$PAYMENTS" | python3 -c "import sys,json;d=json.load(sys.stdin);items=d if isinstance(d,list) else d.get('payments',d.get('items',[]));print(items[0]['id'] if items else '')" 2>/dev/null || echo "")
  if [ -n "$PAY_ID" ]; then
    curl -s -m 15 -X POST "$BASE/api/v1/payments/${PAY_ID}/pay" \
      -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
      -d '{}' | head -c 400; echo
    ok "payment submitted"
  else
    warn="no payment found yet (relay may still be running)"
    echo "  note: $warn"
  fi
fi

bold "=== Traffic generation complete — wait ~15s for metric/log batches ==="
echo "Jaeger:     http://localhost:16686"
echo "Prometheus: http://localhost:9090"
echo "Grafana:    http://localhost:3000  (admin/admin)"
echo "Loki:       http://localhost:3100"
