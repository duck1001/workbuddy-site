package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Listen          string `json:"listen"`
	Upstream        string `json:"upstream"`
	UpstreamKeyFile string `json:"upstream_key_file"`
	DataFile        string `json:"data_file"`
	SiteName        string `json:"site_name"`
	InviteCode      string `json:"invite_code"`
	AdminUser       string `json:"admin_user"`
	AdminPass       string `json:"admin_pass"`
	PublicBase      string `json:"public_base"`
	CookieSecret    string `json:"cookie_secret"`
	SignupBonus     int64  `json:"signup_bonus_tokens"`
	DefaultDayReq   int64  `json:"default_daily_requests"`
	Mode            string `json:"mode"`
	MasterKey       string `json:"master_key"`
}

var (
	cfg         Config
	st          *Store
	upstreamKey string
	cookieKey   []byte
	ownerID     string
)

func loadConfig(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("读取配置失败 %s: %v", path, err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		log.Fatalf("解析配置失败: %v", err)
	}
	changed := false
	if cfg.Mode == "" {
		cfg.Mode = "single"
		changed = true
	}
	if cfg.SiteName == "" {
		cfg.SiteName = "WorkBuddy API"
		changed = true
	}
	if cfg.SignupBonus == 0 {
		cfg.SignupBonus = 200000
		changed = true
	}
	if cfg.CookieSecret == "" {
		cfg.CookieSecret = randHex(32)
		changed = true
	}
	if changed {
		saveConfig(path)
	}
}

func saveConfig(path string) {
	if b, err := json.MarshalIndent(cfg, "", "  "); err == nil {
		_ = os.WriteFile(path, b, 0o600)
	}
}

func readUpstreamKey(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("读取上游 key 失败 %s: %v", path, err)
	}
	return strings.TrimSpace(string(b))
}

func main() {
	var cfgPath, addr string
	flag.StringVar(&cfgPath, "config", "config.json", "配置文件")
	flag.StringVar(&addr, "listen", "", "覆盖监听地址")
	flag.Parse()

	loadConfig(cfgPath)
	if addr != "" {
		cfg.Listen = addr
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:9001"
	}
	upstreamKey = readUpstreamKey(cfg.UpstreamKeyFile)
	cookieKey = []byte(cfg.CookieSecret)
	st = LoadStore(cfg.DataFile)

	if cfg.Mode == "single" {
		owner := st.FindUserByName("owner")
		if owner == nil {
			salt := newSalt()
			owner, _ = st.CreateUser("owner", hashPassword(randHex(16), salt), salt, true)
		}
		st.SetQuota(owner.ID, 0)
		ownerID = owner.ID
		if cfg.MasterKey == "" {
			cfg.MasterKey = "sk-wb-" + randHex(24)
			saveConfig(cfgPath)
		}
		if k := st.EnsureKey(owner.ID, cfg.MasterKey, "master"); k != nil {
			st.SetKeyDayLimit(k.ID, 0)
		}
	}
	if cfg.Mode != "single" && cfg.AdminUser != "" && st.FindUserByName(cfg.AdminUser) == nil {
		salt := newSalt()
		if u, err := st.CreateUser(cfg.AdminUser, hashPassword(cfg.AdminPass, salt), salt, true); err == nil {
			st.SetQuota(u.ID, 0)
			log.Printf("已创建管理员账号 %s", cfg.AdminUser)
		}
	}

	srv := &Server{
		upstreamBase: strings.TrimRight(cfg.Upstream, "/"),
		client: &http.Client{Transport: &http.Transport{
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     90 * time.Second,
		}},
		modelsCache: newModelCache(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleLanding)
	mux.HandleFunc("/login", srv.handleLogin)
	mux.HandleFunc("/register", srv.handleRegister)
	mux.HandleFunc("/logout", srv.handleLogout)
	mux.HandleFunc("/dashboard", srv.handleDashboard)
	mux.HandleFunc("/docs", srv.handleDocs)
	mux.HandleFunc("/admin", srv.handleAdmin)
	mux.HandleFunc("/api/keys", srv.handleCreateKey)
	mux.HandleFunc("/api/keys/revoke", srv.handleRevokeKey)
	mux.HandleFunc("/api/me", srv.handleMe)
	mux.HandleFunc("/style.css", srv.serveCSS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"service":"wbsite","ok":true}`))
	})
	mux.HandleFunc("/v1/", srv.handleProxy)

	go func() {
		for range time.Tick(30 * time.Second) {
			st.Flush()
		}
	}()

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("%s 已启动: http://%s", cfg.SiteName, cfg.Listen)
	log.Fatal(server.ListenAndServe())
}

func atoiSafe(s string, def int64) int64 {
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v
	}
	return def
}
