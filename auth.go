package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const pbkdfIter = 120000

func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hLen := prf.Size()
	blocks := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, blocks*hLen)
	for i := 1; i <= blocks; i++ {
		prf.Reset()
		prf.Write(salt)
		prf.Write([]byte{byte(i >> 24), byte(i >> 16), byte(i >> 8), byte(i)})
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for j := 2; j <= iter; j++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for k := range t {
				t[k] ^= u[k]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

func newSalt() string { return randHex(16) }

func hashPassword(pass, salt string) string {
	raw, _ := hex.DecodeString(salt)
	return hex.EncodeToString(pbkdf2SHA256([]byte(pass), raw, pbkdfIter, 32))
}

func checkPassword(pass, salt, want string) bool {
	return subtle.ConstantTimeCompare([]byte(hashPassword(pass, salt)), []byte(want)) == 1
}

func signCookie(uid string, exp int64) string {
	mac := hmac.New(sha256.New, cookieKey)
	fmt.Fprintf(mac, "%s|%d", uid, exp)
	raw := fmt.Sprintf("%s|%d|%s", uid, exp, hex.EncodeToString(mac.Sum(nil)))
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func parseCookie(v string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return "", false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	mac := hmac.New(sha256.New, cookieKey)
	fmt.Fprintf(mac, "%s|%d", parts[0], exp)
	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(parts[2])) {
		return "", false
	}
	return parts[0], true
}

func csrfFor(uid string) string {
	mac := hmac.New(sha256.New, cookieKey)
	mac.Write([]byte("csrf|" + uid))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

func sessionUser(r *http.Request) *User {
	if cfg.Mode == "single" {
		if u := st.GetUser(ownerID); u != nil {
			return u
		}
		return nil
	}
	c, err := r.Cookie("sid")
	if err != nil {
		return nil
	}
	uid, ok := parseCookie(c.Value)
	if !ok {
		return nil
	}
	u := st.GetUser(uid)
	if u == nil || u.Disabled {
		return nil
	}
	return u
}

func setSession(w http.ResponseWriter, uid string, secure bool) {
	exp := time.Now().Add(30 * 24 * time.Hour).Unix()
	http.SetCookie(w, &http.Cookie{
		Name: "sid", Value: signCookie(uid, exp), Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600,
	})
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: "", Path: "/", MaxAge: -1})
}

type bucket struct {
	n    int
	next time.Time
}

var loginLimiter = struct {
	sync.Mutex
	m map[string]*bucket
}{m: map[string]*bucket{}}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("CF-Connecting-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return h
}

func loginAllowed(ip string) bool {
	loginLimiter.Lock()
	defer loginLimiter.Unlock()
	b := loginLimiter.m[ip]
	now := time.Now()
	if b == nil || now.After(b.next) {
		loginLimiter.m[ip] = &bucket{n: 1, next: now.Add(5 * time.Minute)}
		return true
	}
	if b.n >= 5 {
		return false
	}
	b.n++
	return true
}

func isSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
