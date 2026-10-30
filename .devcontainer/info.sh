#!/usr/bin/env bash
# 打印访问信息
set -uo pipefail
ROOT="$HOME/wb"

DOMAIN="${CODESPACE_NAME:-unknown}-8080.${GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN:-app.github.dev}"
URL="https://${DOMAIN}"

KEY=$(python3 -c "import json;print(json.load(open('$ROOT/site.json'))['master_key'])" 2>/dev/null || echo "?")
GW=$(cat "$ROOT/.apikey" 2>/dev/null || echo "?")

cat <<EOF

──────────────────────────────────────────────
  门户       $URL
  面板       $URL/panel/          （网关运维面，需上面的网关 key）
  Base URL   $URL/v1
  API Key    $KEY
  网关 key   $GW
──────────────────────────────────────────────
如果 $URL 打不开：底部「端口」面板里把 8080 的可见性改成 Public。
添加账号：打开 $URL/panel/ → 输入网关 key → 「添加账号」扫码。
──────────────────────────────────────────────

EOF
