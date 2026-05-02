#!/bin/bash
#
# start-gorse.sh — 一键启动 Gorse 所有服务
# 用法: ./start-gorse.sh [--config <path>]
#
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
CONFIG_FILE="${1:-$PROJECT_DIR/config/config-cadoo.toml}"
LOG_DIR="$PROJECT_DIR/logs"
PID_DIR="$PROJECT_DIR/run"

mkdir -p "$LOG_DIR" "$PID_DIR"

log() { echo "[$(date '+%H:%M:%S')] $*"; }

is_running() { kill -0 "$1" 2>/dev/null; }

# 从 TOML 配置文件中提取值（支持顶层键和 [section] 内键）
toml_get() {
    local key="$1" file="$2"
    if [[ "$key" == *"."* ]]; then
        # Table key: [section.sub] 内查找字段
        local section="${key%.*}" field="${key##*.}"
        awk -v sec="$section" -v fld="$field" '
BEGIN { in_section=0 }
/^\[/ {
    in_section = 0
    if (index($0, "[" sec "]") == 1) in_section = 1
    next
}
in_section && index($0, fld " =") == 1 {
    sub("^[^=]*= *", ""); gsub(/^"/, ""); gsub(/"$/, ""); print; exit
}
' "$file"
    else
        # Top-level key
        awk -F ' = ' -v k="$key" '$1 == k { gsub(/^"|"$/, "", $2); print $2 }' "$file"
    fi
}

log "读取配置文件: $CONFIG_FILE"

# 获取数据库配置
DATA_STORE=$(toml_get "data_store" "$CONFIG_FILE")
DATA_STORE_URI="$DATA_STORE"
log "Data Store: $DATA_STORE_URI"

# 获取 Redis 配置
# 优先从 recommend.supply_demand 获取（config-cadoo.toml 风格）
REDIS_FROM_SUPPLY=$(toml_get "recommend.supply_demand.redis_addr" "$CONFIG_FILE")
REDIS_PASSWORD_SUPPLY=$(toml_get "recommend.supply_demand.redis_password" "$CONFIG_FILE")

if [[ -n "$REDIS_FROM_SUPPLY" ]]; then
    REDIS_HOST="${REDIS_FROM_SUPPLY%%:*}"
    REDIS_PORT="${REDIS_FROM_SUPPLY##*:}"
    REDIS_PASSWORD="$REDIS_PASSWORD_SUPPLY"
    log "Redis (from supply_demand): $REDIS_HOST:$REDIS_PORT"
elif [[ "$DATA_STORE" =~ redis://.* ]]; then
    REDIS_HOST=$(echo "$DATA_STORE" | sed -n 's|.*@\([^:/]*\).*|\1|p')
    REDIS_PORT=$(echo "$DATA_STORE" | sed -n 's|.*:\([0-9]*\)/.*|\1|p')
    REDIS_PASSWORD=$(echo "$DATA_STORE" | sed -n 's|redis://\([^:@]*\):\([^@]*\)@.*|\2|p')
    log "Redis (from data_store): $REDIS_HOST:$REDIS_PORT"
else
    REDIS_HOST="localhost"
    REDIS_PORT="6379"
    REDIS_PASSWORD=""
    log "Redis: 使用默认值 localhost:6379"
fi

# ============================================================
# 检查已有进程
# ============================================================
check_port() {
    lsof -ti :"$1" 2>/dev/null | head -1
}

EXISTING_PIDS=""
for port in 8088 8091 8092 8093 8086; do
    pid=$(check_port $port)
    if [[ -n "$pid" ]]; then
        EXISTING_PIDS="$EXISTING_PIDS $port(PID $pid)"
    fi
done

if [[ -n "$EXISTING_PIDS" ]]; then
    log "端口已被占用:$EXISTING_PIDS"
    log "请先运行: ./stop-gorse.sh"
    exit 1
fi

# ============================================================
# 编译二进制
# ============================================================
compile() {
    local name="$1" path="$2" dest="$3"
    if [[ ! -f "$dest" ]] || [[ "$path/main.go" -nt "$dest" ]]; then
        log "编译 $name..."
        (cd "$PROJECT_DIR" && go build -o "$dest" "./$path")
    else
        log "使用已有二进制: $dest"
    fi
}

GORSE_BIN="$PROJECT_DIR/bin/gorse-in-one"
AB_BIN="$PROJECT_DIR/bin/ab-experiment"
RISK_BIN="$PROJECT_DIR/bin/risk-health"
COLD_BIN="$PROJECT_DIR/bin/cold-start-sidecar"

compile "gorse-in-one" "cmd/gorse-in-one" "$GORSE_BIN"
compile "ab-experiment" "cmd/ab-experiment" "$AB_BIN"
compile "risk-health" "cmd/risk-health" "$RISK_BIN"
compile "cold-start-sidecar" "cmd/cold-start-sidecar" "$COLD_BIN"

# ============================================================
# 启动 ab-experiment (8092)
# ============================================================
log "启动 ab-experiment (8092)..."
export REDIS_ADDR="$REDIS_HOST:$REDIS_PORT"
export REDIS_PASSWORD="$REDIS_PASSWORD"
export HTTP_PORT=":8092"
"$AB_BIN" >> "$LOG_DIR/ab-experiment.log" 2>&1 &
AB_PID=$!
echo $AB_PID > "$PID_DIR/ab-experiment.pid"
log "ab-experiment PID: $AB_PID"

# ============================================================
# 启动 risk-health (8093)
# ============================================================
log "启动 risk-health (8093)..."
export REDIS_ADDR="$REDIS_HOST:$REDIS_PORT"
export REDIS_PASSWORD="$REDIS_PASSWORD"
export POSTGRES_URI="$DATA_STORE_URI"
export HTTP_PORT=":8093"
"$RISK_BIN" >> "$LOG_DIR/risk-health.log" 2>&1 &
RISK_PID=$!
echo $RISK_PID > "$PID_DIR/risk-health.pid"
log "risk-health PID: $RISK_PID"

# ============================================================
# 启动 cold-start-sidecar (8091)
# ============================================================
log "启动 cold-start-sidecar (8091)..."
export REDIS_ADDR="$REDIS_HOST:$REDIS_PORT"
export REDIS_PASSWORD="$REDIS_PASSWORD"
export DATA_STORE_URI="$DATA_STORE_URI"
export HTTP_PORT=":8091"
"$COLD_BIN" >> "$LOG_DIR/cold-start.log" 2>&1 &
COLD_PID=$!
echo $COLD_PID > "$PID_DIR/cold-start.pid"
log "cold-start-sidecar PID: $COLD_PID"

# ============================================================
# 启动 gorse-in-one (8088)
# ============================================================
log "启动 gorse-in-one (8088)..."
"$GORSE_BIN" --config "$CONFIG_FILE" >> "$LOG_DIR/gorse-in-one.log" 2>&1 &
GORSE_PID=$!
echo $GORSE_PID > "$PID_DIR/gorse-in-one.pid"
log "gorse-in-one PID: $GORSE_PID"

# ============================================================
# 等待启动并验证
# ============================================================
log "等待服务启动 (10s)..."
sleep 10

log ""
log "=== 服务状态 ==="
check() {
    local name="$1" pid="$2"
    if [[ -n "$pid" ]] && is_running "$pid"; then
        echo "  $name: 运行中 (PID $pid)"
    else
        echo "  $name: 未运行"
    fi
}
check "gorse-in-one (8088)" "$GORSE_PID"
check "ab-experiment (8092)" "$AB_PID"
check "risk-health (8093)" "$RISK_PID"
check "cold-start (8091)" "$COLD_PID"

log ""
log "=== 健康检查 ==="
check_http() {
    local url="$1" name="$2"
    local code
    code=$(curl -s -o /dev/null -w "%{http_code}" "$url" --connect-timeout 3 --max-time 5 2>/dev/null || echo "000")
    if [[ "$code" == "200" ]]; then
        echo "  $name: OK (HTTP $code)"
    else
        echo "  $name: $code"
    fi
}

check_http "http://localhost:8088/api/status" "gorse-in-one"
check_http "http://localhost:8092/health" "ab-experiment"
check_http "http://localhost:8093/health" "risk-health"
check_http "http://localhost:8091/health" "cold-start"

log ""
log "Dashboard: http://localhost:8088"
log "日志: $LOG_DIR/*.log"
