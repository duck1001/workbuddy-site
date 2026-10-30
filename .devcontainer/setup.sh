#!/usr/bin/env bash
# Codespaces 首次创建：拉上游网关 + Caddy，生成本地配置，编译站点
set -euo pipefail
cd "$(dirname "$0")/.."

ROOT="$HOME/wb"
mkdir -p "$ROOT/auths" "$ROOT/data" "$ROOT/logs"

ARCH=$(uname -m); case "$ARCH" in x86_64) A=amd64;; aarch64|arm64) A=arm64;; esac
echo "==> arch $A"

echo "==> wb2api"
if [ ! -x "$ROOT/wb2api" ]; then
  for i in 1 2 3; do
    if curl -fsSL --retry 2 --connect-timeout 15 -m 180 -o /tmp/wb2api.tgz \
      "https://github.com/linguo2625469/workbuddy2api-panel/releases/download/v1.13.0/wb2api-panel-v1.13.0-linux-${A}.tar.gz"; then
      tar -xzf /tmp/wb2api.tgz -C "$ROOT" wb2api && chmod +x "$ROOT/wb2api" && break
    fi
    echo "   retry $i"; sleep 5
  done
fi
[ -x "$ROOT/wb2api" ] || echo "!! wb2api 下载失败，稍后重开终端执行 bash .devcontainer/setup.sh"

echo "==> caddy"
if [ ! -x "$ROOT/caddy" ]; then
  for i in 1 2 3; do
    TAG=$(curl -fsSL --connect-timeout 15 -m 60 https://api.github.com/repos/caddyserver/caddy/releases/latest \
      | python3 -c "import sys,json;print(json.load(sys.stdin).get('tag_name',''))" 2>/dev/null || true)
    [ -z "$TAG" ] && TAG=v2.10.0
    if curl -fsSL --retry 2 --connect-timeout 15 -m 180 -o /tmp/caddy.tgz \
      "https://github.com/caddyserver/caddy/releases/download/${TAG}/caddy_${TAG#v}_linux_${A}.tar.gz"; then
      tar -xzf /tmp/caddy.tgz -C "$ROOT" caddy && chmod +x "$ROOT/caddy" && break
    fi
    echo "   retry $i"; sleep 5
  done
fi
[ -x "$ROOT/caddy" ] || echo "!! caddy 下载失败"

echo "==> build site"
[ -f go.mod ] && go build -trimpath -ldflags="-s -w" -o "$ROOT/wbsite" .

if [ ! -f "$ROOT/gateway.json" ]; then
  GW_KEY=$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')
  MASTER="sk-wb-$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  echo "$GW_KEY" > "$ROOT/.apikey"
  chmod 600 "$ROOT/.apikey"

  cat > "$ROOT/gateway.json" <<EOF
{
  "listen": "127.0.0.1:7863",
  "api_key": "$GW_KEY",
  "auth_dir": "$ROOT/auths",
  "state_file": "$ROOT/data/state.json",
  "logging": { "request_archive_enabled": true, "request_retention_days": 7, "request_archive_max_mb": 100, "request_client_info": true },
  "schedule": { "checkin_hours": [9,21], "growth_hours": [1], "travel_hours": [9,21], "activity_hours": [10],
    "keepalive_hours": [22], "blackcat_hours": [23], "checkin_enabled": true, "growth_enabled": true,
    "travel_enabled": true, "activity_enabled": true, "keepalive_enabled": true, "blackcat_enabled": true,
    "balance_refresh_enabled": true, "balance_refresh_minutes": 5 },
  "upstream": { "timeout_seconds": 120, "header_timeout_seconds": 120, "idle_timeout_seconds": 300, "passthrough_ip": true },
  "pool": { "max_in_flight": 3, "max_in_flight_global": 2, "breaker_threshold": 3, "breaker_cooldown": "30m",
    "degrade_threshold": 5, "degrade_cooldown": "10m", "prefer_expiring": true, "expiring_soon": "168h", "credit_floor": 100 }
}
EOF

  cat > "$ROOT/site.json" <<EOF
{
  "listen": "127.0.0.1:9001",
  "upstream": "http://127.0.0.1:7863",
  "upstream_key_file": "$ROOT/.apikey",
  "data_file": "$ROOT/data/site.json",
  "site_name": "WorkBuddy API",
  "invite_code": "",
  "admin_user": "admin",
  "admin_pass": "",
  "cookie_secret": "$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')",
  "signup_bonus_tokens": 200000,
  "default_daily_requests": 0,
  "mode": "single",
  "master_key": "$MASTER"
}
EOF

  cat > "$ROOT/Caddyfile" <<'EOF'
{
	admin off
	auto_https off
	persist_config off
	servers {
		trusted_proxies static 127.0.0.1/32
		client_ip_headers CF-Connecting-IP
	}
}

:8080 {
	header {
		-Server
	}

	handle /panel/* {
		reverse_proxy 127.0.0.1:7863 {
			flush_interval -1
		}
	}
	handle /status {
		reverse_proxy 127.0.0.1:7863
	}
	handle /gw-healthz {
		reverse_proxy 127.0.0.1:7863
	}

	handle {
		reverse_proxy 127.0.0.1:9001 {
			flush_interval -1
			header_up X-Forwarded-Proto https
			transport http {
				dial_timeout 10s
				read_timeout 1h
				write_timeout 1h
			}
		}
	}
}
EOF
  chmod 600 "$ROOT/gateway.json" "$ROOT/site.json"
fi

echo "==> done"
