#!/usr/bin/env bash
# 在 Codespace 内起一条 Cloudflare 隧道，绕开 GitHub 端口鉴权墙。
# 输出 https://xxx.trycloudflare.com，任何客户端都能直连。
set -uo pipefail
ROOT="$HOME/wb"
LOG="$ROOT/logs"
mkdir -p "$LOG"
ARCH=$(uname -m); case "$ARCH" in x86_64) A=amd64;; aarch64|arm64) A=arm64;; esac

if [ ! -x "$ROOT/cloudflared" ]; then
  echo "==> 下载 cloudflared"
  for i in 1 2 3; do
    if curl -fsSL --retry 2 --connect-timeout 15 -m 180 \
      -o "$ROOT/cloudflared" \
      "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${A}"; then
      chmod +x "$ROOT/cloudflared" && break
    fi
    echo "   retry $i"; sleep 5
  done
fi

# 杀掉旧隧道
pkill -f "cloudflared tunnel" 2>/dev/null || true
sleep 1

CFLOG="$LOG/cloudflared.log"
: > "$CFLOG"
nohup "$ROOT/cloudflared" tunnel --no-autoupdate --protocol http2 \
  --url http://127.0.0.1:8080 --logfile "$CFLOG" --loglevel info \
  >/dev/null 2>&1 &

echo "==> 等待隧道注册"
for i in $(seq 1 40); do
  if grep -q "Registered tunnel connection" "$CFLOG" 2>/dev/null; then
    URL=$(grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' "$CFLOG" | tail -1)
    if [ -n "${URL:-}" ]; then
      echo "$URL" > "$ROOT/public_url.txt"
      break
    fi
  fi
  sleep 1
done

URL=$(cat "$ROOT/public_url.txt" 2>/dev/null || true)
KEY=$(python3 -c "import json;print(json.load(open('$ROOT/site.json'))['master_key'])" 2>/dev/null || echo "?")
GW=$(cat "$ROOT/.apikey" 2>/dev/null || echo "?")

echo
echo "──────────────────────────────────────────────"
if [ -n "${URL:-}" ]; then
  echo "  公网地址   $URL"
  echo "  面板       $URL/panel/"
  echo "  Base URL   $URL/v1"
else
  echo "  隧道未注册成功，看日志：$CFLOG"
fi
echo "  API Key    $KEY"
echo "  网关密钥   $GW"
echo "──────────────────────────────────────────────"
echo
