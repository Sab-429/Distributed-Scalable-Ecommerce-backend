#!/usr/bin/env bash
# =============================================================================
# stop.sh — Gracefully stop all microservices started by start.sh
# =============================================================================

set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'
BOLD='\033[1m'; RESET='\033[0m'

info()    { echo -e "${CYAN}[INFO]${RESET}  $*"; }
success() { echo -e "${GREEN}[OK]${RESET}    $*"; }
error()   { echo -e "${RED}[ERROR]${RESET} $*" >&2; }

PID_FILE="/tmp/services.pids"

if [[ ! -f "$PID_FILE" ]]; then
    error "No PID file found at $PID_FILE — nothing to stop."
    exit 1
fi

echo ""
echo -e "${BOLD}=== Stopping microservices ===${RESET}"
echo ""

while read -r pid name; do
    if kill -0 "$pid" 2>/dev/null; then
        info "Stopping ${BOLD}${name}${RESET} (PID $pid)..."
        kill -SIGTERM "$pid"
        # Wait up to 5 s for graceful shutdown
        for _ in {1..10}; do
            kill -0 "$pid" 2>/dev/null || break
            sleep 0.5
        done
        if kill -0 "$pid" 2>/dev/null; then
            kill -SIGKILL "$pid" 2>/dev/null || true
            error "$name did not stop gracefully; force-killed."
        else
            success "$name stopped"
        fi
    else
        info "$name (PID $pid) is not running — skipping"
    fi
done < "$PID_FILE"

rm -f "$PID_FILE"

echo ""
success "All services stopped."
echo ""
