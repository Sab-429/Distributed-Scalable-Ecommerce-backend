#!/usr/bin/env bash
# =============================================================================
# start.sh — Build and run all microservices
#
# Start order:
#   1. product-service  (HTTP :8080  |  gRPC :50051)
#   2. auth-service     (HTTP :8081)
#   3. order-service    (HTTP :8082)
#   4. payment-service  (HTTP :8083)
#   5. api-gateway      (HTTP :8000)
#
# Each service runs in the background.  PIDs are tracked in /tmp/services.pids
# so the companion stop.sh can shut them all down cleanly.
#
# Usage:
#   ./start.sh          # start everything
#   ./start.sh --logs   # start everything and tail all logs
# =============================================================================

set -euo pipefail

# ── Colour helpers ────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { echo -e "${CYAN}[INFO]${RESET}  $*"; }
success() { echo -e "${GREEN}[OK]${RESET}    $*"; }
warn()    { echo -e "${YELLOW}[WARN]${RESET}  $*"; }
error()   { echo -e "${RED}[ERROR]${RESET} $*" >&2; }

# ── Windows Go PATH shim (WSL) ────────────────────────────────────────────────
# When running from WSL, the Windows Go installation is not in the WSL PATH.
# Detect common install locations and add the first one found.
for _go_win_path in \
    "/mnt/c/Program Files/Go/bin" \
    "/mnt/c/Go/bin" \
    "/mnt/c/Users/$USER/AppData/Local/Programs/Go/bin"
do
    if [[ -x "$_go_win_path/go.exe" ]]; then
        export PATH="$_go_win_path:$PATH"
        break
    fi
done
unset _go_win_path

# Wrapper so that `go` always resolves correctly whether the binary is
# called `go` (native Linux install) or `go.exe` (Windows install via WSL).
go() {
    if command -v go.exe &>/dev/null; then
        go.exe "$@"
    else
        command go "$@"
    fi
}
export -f go

# ── Config ────────────────────────────────────────────────────────────────────
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_DIR="$ROOT_DIR/logs"
PID_FILE="/tmp/services.pids"

SHOW_LOGS=false
[[ "${1:-}" == "--logs" ]] && SHOW_LOGS=true

mkdir -p "$LOG_DIR"
> "$PID_FILE"   # truncate / create

# ── Strip Windows CRLF line endings from all .env files ──────────────────────
# Files edited on Windows have \r\n endings which cause `$'\r': command not found`
# errors in bash when the .env is sourced or loaded by godotenv.
find "$ROOT_DIR" -maxdepth 2 -name ".env" | while read -r envfile; do
    sed -i 's/\r//' "$envfile"
done

# ── Prerequisite checks ───────────────────────────────────────────────────────
check_prereqs() {
    info "Checking prerequisites..."

    if ! command -v go &>/dev/null && ! command -v go.exe &>/dev/null; then
        error "Go is not installed or not in PATH. Aborting."
        exit 1
    fi
    success "Go $(go version | awk '{print $3}')"

    # Redis
    if command -v redis-cli &>/dev/null && redis-cli ping &>/dev/null; then
        success "Redis is running"
    else
        warn "Redis does not appear to be running on localhost:6379."
        warn "product-service requires Redis. Start it with: redis-server"
        read -r -p "  Continue anyway? [y/N] " ans
        [[ "${ans,,}" == "y" ]] || exit 1
    fi

    # RabbitMQ
    if (echo > /dev/tcp/localhost/5672) 2>/dev/null; then
        success "RabbitMQ is running"
    else
        warn "RabbitMQ does not appear to be running on localhost:5672."
        warn "order-service, payment-service and notification-worker require it."
        read -r -p "  Continue anyway? [y/N] " ans
        [[ "${ans,,}" == "y" ]] || exit 1
    fi
}

# ── Build a service ───────────────────────────────────────────────────────────
# Usage: build_service <display-name> <service-dir>
build_service() {
    local name="$1"
    local dir="$2"
    info "Building ${BOLD}${name}${RESET}..."
    # Force Linux binary — go.exe (Windows toolchain) defaults to GOOS=windows.
    # -o server places the binary in the service root next to .env so
    # godotenv.Load(filepath.Dir(exe)+"/.env") resolves correctly at runtime.
    if ! (cd "$dir" && go build -o server ./cmd/server/...); then
        error "Build failed for $name. Aborting."
        exit 1
    fi
    success "$name built"
}

# ── Start a service ───────────────────────────────────────────────────────────
# Usage: start_service <display-name> <service-dir> <binary-name>
start_service() {
    local name="$1"
    local dir="$2"
    local bin="$3"
    local log_file="$LOG_DIR/${bin}.log"
    local binary="$dir/$bin"

    # build produced the binary inside the service dir when targeting ./cmd/server/...
    # go build ./cmd/server outputs to the module root; adjust if needed
    if [[ ! -f "$binary" ]]; then
        # fallback: binary may be named after the directory
        binary="$(find "$dir" -maxdepth 1 -type f -perm /111 | head -1)"
    fi

    if [[ -z "$binary" || ! -f "$binary" ]]; then
        error "Could not find binary for $name in $dir"
        exit 1
    fi

    info "Starting ${BOLD}${name}${RESET} → log: logs/${bin}.log"
    "$binary" >> "$log_file" 2>&1 &
    local pid=$!
    echo "$pid $name" >> "$PID_FILE"
    success "$name started (PID $pid)"
}

# ── Wait for an HTTP port to accept connections ───────────────────────────────
wait_for_port() {
    local name="$1"
    local port="$2"
    local retries=20
    local i=0
    while ! (echo > /dev/tcp/localhost/"$port") 2>/dev/null; do
        sleep 0.5
        (( i++ ))
        if (( i >= retries )); then
            warn "$name did not become ready on port $port within ${retries}s"
            return 1
        fi
    done
    success "$name is ready on port $port"
}

# =============================================================================
# Main
# =============================================================================
echo ""
echo -e "${BOLD}=== Microservices Startup ===${RESET}"
echo ""

check_prereqs

# ── Build all services ────────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}--- Building services ---${RESET}"

# go.exe (Windows toolchain) ignores GOOS set in the Linux shell environment.
# Use `go env -w` to write it into the Go environment file, which go.exe reads.
info "Setting GOOS=linux for cross-compilation..."
go env -w GOOS=linux GOARCH=amd64

build_service "product-service" "$ROOT_DIR/product-service"
build_service "auth-service"    "$ROOT_DIR/Auth-service"
build_service "order-service"   "$ROOT_DIR/order-service"
build_service "payment-service" "$ROOT_DIR/payment-service"
build_service "api-gateway"     "$ROOT_DIR/api-gateway"
build_service "notification-worker" "$ROOT_DIR/notification-worker"

# Reset to Windows so go.exe works normally outside this script.
go env -w GOOS=windows GOARCH=amd64
info "GOOS reset to windows"

# ── Start services in dependency order ───────────────────────────────────────
echo ""
echo -e "${BOLD}--- Starting services ---${RESET}"

# 1. product-service  (gRPC :50051 consumed by order-service)
start_service "product-service" "$ROOT_DIR/product-service" "server"
wait_for_port "product-service" 8080

# 2. auth-service
start_service "auth-service"    "$ROOT_DIR/Auth-service"    "server"
wait_for_port "auth-service"    8081

# 3. order-service  (needs product-service gRPC)
start_service "order-service"   "$ROOT_DIR/order-service"   "server"
wait_for_port "order-service"   8082

# 4. payment-service  (consumes order.created from RabbitMQ, publishes
#    payment.* back; order-service consumes those in turn)
start_service "payment-service" "$ROOT_DIR/payment-service" "server"
wait_for_port "payment-service" 8083

# 5. api-gateway  (needs all four upstream services)
start_service "api-gateway"     "$ROOT_DIR/api-gateway"     "server"
wait_for_port "api-gateway"     8000

# 6. notification-worker  (consumes order events from RabbitMQ)
start_service "notification-worker" "$ROOT_DIR/notification-worker" "server"

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}=== All services running ===${RESET}"
echo ""
echo -e "  ${GREEN}product-service${RESET}   HTTP  http://localhost:8080"
echo -e "  ${GREEN}product-service${RESET}   gRPC  localhost:50051"
echo -e "  ${GREEN}auth-service${RESET}      HTTP  http://localhost:8081"
echo -e "  ${GREEN}order-service${RESET}     HTTP  http://localhost:8082"
echo -e "  ${GREEN}payment-service${RESET}   HTTP  http://localhost:8083"
echo -e "  ${GREEN}api-gateway${RESET}       HTTP  http://localhost:8000"
echo ""
echo -e "  Logs  →  ${LOG_DIR}/"
echo -e "  PIDs  →  ${PID_FILE}"
echo ""
echo -e "  To stop all services:  ${BOLD}./stop.sh${RESET}"
echo ""

# ── Optionally tail all logs ──────────────────────────────────────────────────
if $SHOW_LOGS; then
    echo -e "${CYAN}Tailing logs (Ctrl-C to detach — services keep running)...${RESET}"
    echo ""
    tail -f "$LOG_DIR"/*.log
fi
