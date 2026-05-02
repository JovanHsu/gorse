#!/bin/bash
#
# stop-gorse.sh — 一键停止 Gorse 所有服务
# 用法: ./stop-gorse.sh
#
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
PID_DIR="${PROJECT_DIR}/run"

log() { echo "[$(date '+%H:%M:%S')] $*"; }

is_running() {
    kill -0 "$1" 2>/dev/null
}

stop_pid() {
    local pid="$1" name="$2"
    if [ -n "$pid" ] && is_running "$pid"; then
        log "停止 $name (PID $pid)..."
        kill "$pid" 2>/dev/null
        for i in $(seq 1 5); do
            sleep 1
            is_running "$pid" || break
        done
        if is_running "$pid"; then
            kill -9 "$pid" 2>/dev/null
            sleep 1
        fi
        log "$name 已停止"
    else
        log "$name 未运行 (或已停止)"
    fi
}

log "停止 Gorse 所有服务..."

GORSE_PID=$(cat "$PID_DIR/gorse-in-one.pid" 2>/dev/null || true)
AB_PID=$(cat "$PID_DIR/ab-experiment.pid" 2>/dev/null || true)
RISK_PID=$(cat "$PID_DIR/risk-health.pid" 2>/dev/null || true)
COLD_PID=$(cat "$PID_DIR/cold-start.pid" 2>/dev/null || true)

# 按启动顺序的逆序停止：gorse-in-one 先停（依赖 sidecar），再停 sidecar
stop_pid "$GORSE_PID" "gorse-in-one"
stop_pid "$COLD_PID" "cold-start-sidecar"
stop_pid "$RISK_PID" "risk-health"
stop_pid "$AB_PID" "ab-experiment"

# 清理 PID 文件
rm -f "$PID_DIR/gorse-in-one.pid" \
      "$PID_DIR/ab-experiment.pid" \
      "$PID_DIR/risk-health.pid" \
      "$PID_DIR/cold-start.pid"

# 检查是否有残留进程
RESIDUAL=""
for port in 8088 8091 8092 8093; do
    pid=$(lsof -ti :$port 2>/dev/null || true)
    if [ -n "$pid" ]; then
        RESIDUAL="$RESIDUAL $port"
        log "警告: 端口 $port 仍有进程 (PID $pid) 未释放"
    fi
done

if [ -z "$RESIDUAL" ]; then
    log "所有服务已停止，端口已释放"
else
    log "手动释放残留端口: kill -9 $RESIDUAL"
fi
