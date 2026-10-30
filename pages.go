package main

import (
	"embed"
	"encoding/json"
	"html/template"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

//go:embed web/*.html web/style.css
var webFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"fmtTime": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.Local().Format("01-02 15:04")
	},
	"sub": func(a, b int64) int64 {
		if a < b {
			return 0
		}
		return a - b
	},
	"pct": func(a, b int64) int64 {
		if b <= 0 {
			return 0
		}
		p := a * 100 / b
		if p > 100 {
			p = 100
		}
		return p
	},
	"comma": func(n int64) string {
		s := ""
		neg := n < 0
		if neg {
			n = -n
		}
		for n > 0 {
			s = string(rune('0'+n%10)) + s
			n /= 10
			if n > 0 && len(s)%4 == 3 {
				s = "," + s
			}
		}
		if s == "" {
			s = "0"
		}
		if neg {
			s = "-" + s
		}
		return s
	},
}).ParseFS(webFS, "web/*.html"))

type Server struct {
	upstreamBase string
	client       *http.Client
	modelsCache  *modelCacheT
}

type pageData struct {
	Site      string
	User      *User
	CSRF      string
	Err       string
	NewKey    string
	Keys      []*Key
	Usage     []Usage
	Models    []string
	Users     []*User
	StatsU    int64
	StatsK    int64
	StatsReq  int64
	StatsTok  int64
	IsAdmin   bool
	Page      string
	Upstream  string
	InviteOn  bool
	Mode      string
	MasterKey string
	GatewayKey string
}

func (s *Server) base(r *http.Request) pageData {
	scheme := "http"
	if isSecure(r) {
		scheme = "https"
	}
	d := pageData{
		Site: cfg.SiteName, Page: "home", InviteOn: cfg.InviteCode != "",
		Upstream: scheme + "://" + r.Host, Mode: cfg.Mode, MasterKey: cfg.MasterKey, GatewayKey: upstreamKey,
	}
	if u := sessionUser(r); u != nil {
		d.User = u
		d.CSRF = csrfFor(u.ID)
		d.IsAdmin = u.IsAdmin
	}
	return d
}

func (s *Server) render(w http.ResponseWriter, name string, d pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, d); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	if cfg.Mode == "single" && r.URL.Path == "/" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	d := s.base(r)
	d.Page = "home"
	d.Models = s.modelIDs()
	su, sk, sr, stt := st.Stats()
	d.StatsU, d.StatsK, d.StatsReq, d.StatsTok = su, sk, sr, stt
	s.render(w, "landing.html", d)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if cfg.Mode == "single" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet {
		d := s.base(r)
		d.Page = "login"
		if d.User != nil {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		s.render(w, "login.html", d)
		return
	}
	_ = r.ParseForm()
	if !loginAllowed(clientIP(r)) {
		d := s.base(r)
		d.Err = "尝试次数过多，请 5 分钟后再试"
		s.render(w, "login.html", d)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	u := st.FindUserByName(name)
	if u == nil || !checkPassword(r.FormValue("pass"), u.PassSalt, u.PassHash) {
		d := s.base(r)
		d.Err = "账号或密码不正确"
		d.Page = "login"
		s.render(w, "login.html", d)
		return
	}
	if u.Disabled {
		d := s.base(r)
		d.Err = "账号已停用"
		s.render(w, "login.html", d)
		return
	}
	setSession(w, u.ID, isSecure(r))
	st.TouchUser(u.ID)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if cfg.Mode == "single" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet {
		if cfg.InviteCode == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		d := s.base(r)
		d.Page = "login"
		s.render(w, "register.html", d)
		return
	}
	_ = r.ParseForm()
	if cfg.InviteCode != "" && strings.TrimSpace(r.FormValue("invite")) != cfg.InviteCode {
		d := s.base(r)
		d.Err = "邀请码不正确"
		s.render(w, "register.html", d)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	pass := r.FormValue("pass")
	if len(name) < 3 || len(name) > 24 {
		d := s.base(r)
		d.Err = "用户名需 3-24 个字符"
		s.render(w, "register.html", d)
		return
	}
	if len(pass) < 8 {
		d := s.base(r)
		d.Err = "密码至少 8 位"
		s.render(w, "register.html", d)
		return
	}
	salt := newSalt()
	u, err := st.CreateUser(name, hashPassword(pass, salt), salt, false)
	if err != nil {
		d := s.base(r)
		d.Err = "用户名已被占用"
		s.render(w, "register.html", d)
		return
	}
	setSession(w, u.ID, isSecure(r))
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	d := s.base(r)
	if d.User == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	d.Page = "dash"
	d.Keys = st.KeysOf(d.User.ID)
	d.Usage = st.UsageOf(d.User.ID, 40)
	d.Models = s.modelIDs()
	d.NewKey = r.URL.Query().Get("newkey")
	s.render(w, "dashboard.html", d)
}

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	d := s.base(r)
	d.Page = "docs"
	d.Models = s.modelIDs()
	s.render(w, "docs.html", d)
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	d := s.base(r)
	if d.User == nil || !d.IsAdmin {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		if r.FormValue("csrf") == csrfFor(d.User.ID) {
			id := r.FormValue("uid")
			switch r.FormValue("action") {
			case "disable":
				st.SetUserFlags(id, true, 0)
			case "enable":
				st.SetUserFlags(id, false, 0)
			case "quota":
				st.SetUserFlags(id, false, atoiSafe(r.FormValue("limit"), 0))
			}
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	d.Users = st.AllUsers()
	d.StatsU, d.StatsK, d.StatsReq, d.StatsTok = st.Stats()
	s.render(w, "admin.html", d)
}

func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r)
	if u == nil {
		writeAPIError(w, 401, "unauthorized", "请先登录")
		return
	}
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	if r.FormValue("csrf") != csrfFor(u.ID) {
		writeAPIError(w, 403, "csrf", "CSRF 校验失败")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "default"
	}
	plain, _ := st.CreateKey(u.ID, name)
	http.Redirect(w, r, "/dashboard?newkey="+plain, http.StatusSeeOther)
}

func (s *Server) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	if r.FormValue("csrf") == csrfFor(u.ID) {
		st.RevokeKey(u.ID, r.FormValue("id"), u.IsAdmin)
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r)
	if u == nil {
		writeAPIError(w, 401, "unauthorized", "not logged in")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": u.ID, "name": u.Name, "limit": u.TokLimit, "used": u.TokUsed, "req": u.ReqTotal,
	})
}

func (s *Server) serveCSS(w http.ResponseWriter, r *http.Request) {
	b, err := webFS.ReadFile("web/style.css")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(b)
}

func (s *Server) modelIDs() []string {
	s.modelsCache.mu.Lock()
	defer s.modelsCache.mu.Unlock()
	if time.Since(s.modelsCache.at) < 5*time.Minute && len(s.modelsCache.body) > 0 {
		return parseModelIDs(s.modelsCache.body)
	}
	req, _ := http.NewRequest("GET", s.upstreamBase+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+upstreamKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return parseModelIDs(s.modelsCache.body)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == 200 {
		s.modelsCache.body = body
		s.modelsCache.at = time.Now()
	}
	return parseModelIDs(body)
}

func parseModelIDs(body []byte) []string {
	var v struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	out := []string{}
	if json.Unmarshal(body, &v) != nil {
		return out
	}
	for _, m := range v.Data {
		out = append(out, m.ID)
	}
	sort.Strings(out)
	return out
}
