#!/usr/bin/env bash
# Codespaces 每次启动：拉起三个服务（幂等）
set -uo pipefail
ROOT="$HOME/wb"
LOG="$ROOT/logs"
mkdir -p "$LOG"

running() { pgrep -f "$1" >/dev/null 2>&1; }

if ! running "wb2api -config"; then
  nohup "$ROOT/wb2api" -config "$ROOT/gateway.json" >> "$LOG/gateway.log" 2>&1 &
  echo "gateway started"
fi
if ! running "wbsite -config"; then
  nohup "$ROOT/wbsite" -config "$ROOT/site.json" >> "$LOG/site.log" 2>&1 &
  echo "site started"
fi
if ! running "caddy run"; then
  nohup "$ROOT/caddy" run --config "$ROOT/Caddyfile" --adapter caddyfile >> "$LOG/caddy.log" 2>&1 &
  echo "caddy started"
fi
sleep 2
bash "$(dirname "$0")/info.sh"
