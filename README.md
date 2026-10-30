# workbuddy-site

给 workbuddy2api 套的自托管门户：单 key、额度计量、用量明细、模型目录、OpenAI 兼容入口。

纯 Go 标准库，无第三方依赖，`go:embed` 打包页面，单二进制约 8 MB。

## 在 Codespaces 里跑（推荐）

1. 仓库页 → **Code** → **Codespaces** → **Create codespace on main**
2. 等 `postCreateCommand` 跑完（约 1–2 分钟，终端会打印门户地址和 key）
3. 底部「端口」面板，把 **8080** 的可见性改成 **Public**
4. 打开 `https://<codespace>-8080.app.github.dev`
5. 面板 `/panel/` → 输入网关 key → 「添加账号」扫码

之后每次重开 Codespaces，`postStartCommand` 会自动拉起三个服务。

**额度**：免费 120 core-hours/月，2 核 ≈ 60 小时；闲置 30 分钟自动停。

## 本地跑

```sh
go build -o wbsite .
./wbsite -config config.json
```

最小配置：

```json
{
  "listen": "127.0.0.1:9001",
  "upstream": "http://127.0.0.1:7863",
  "upstream_key_file": "/path/to/.apikey",
  "data_file": "./data.json",
  "site_name": "WorkBuddy API",
  "mode": "single",
  "master_key": "sk-wb-...",
  "cookie_secret": "...",
  "default_daily_requests": 0
}
```

`mode: "single"` 无登录，打开即控制台；`"multi"` 走注册登录 + 管理员后台。

## 结构

```
main.go      配置、路由、启动
store.go     用户 / Key / 用量（JSON 原子落盘，30s 刷盘）
auth.go      PBKDF2 口令、HMAC 会话、CSRF、登录限速
proxy.go     /v1/* 转发、SSE 直通、usage 提取
pages.go     页面渲染、模型目录缓存（5 分钟）
web/         首页 / 登录 / 注册 / 控制台 / 文档 / 管理
.devcontainer  Codespaces 自动部署
```
