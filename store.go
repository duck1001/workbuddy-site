package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func hashToken(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

type User struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	PassHash string    `json:"pass_hash"`
	PassSalt string    `json:"pass_salt"`
	Created  time.Time `json:"created"`
	Disabled bool      `json:"disabled"`
	IsAdmin  bool      `json:"is_admin"`
	TokLimit int64     `json:"tok_limit"`
	TokUsed  int64     `json:"tok_used"`
	ReqTotal int64     `json:"req_total"`
	LastSeen time.Time `json:"last_seen"`
}

type Key struct {
	ID       string    `json:"id"`
	UserID   string    `json:"user_id"`
	Name     string    `json:"name"`
	Hash     string    `json:"hash"`
	Prefix   string    `json:"prefix"`
	Created  time.Time `json:"created"`
	Revoked  bool      `json:"revoked"`
	LastUsed time.Time `json:"last_used"`
	ReqTotal int64     `json:"req_total"`
	TokTotal int64     `json:"tok_total"`
	DayKey   string    `json:"day_key"`
	DayReq   int64     `json:"day_req"`
	DayLimit int64     `json:"day_limit"`
}

type Usage struct {
	Time    time.Time `json:"t"`
	UserID  string    `json:"u"`
	KeyID   string    `json:"k"`
	Model   string    `json:"m"`
	Tokens  int64     `json:"tok"`
	Status  int       `json:"s"`
	Latency int64     `json:"ms"`
	IP      string    `json:"ip"`
	Stream  bool      `json:"st"`
}

type Store struct {
	mu    sync.Mutex
	path  string
	dirty bool
	Users map[string]*User `json:"users"`
	Keys  map[string]*Key  `json:"keys"`
	Usage []Usage          `json:"usage"`
}

func LoadStore(path string) *Store {
	s := &Store{path: path, Users: map[string]*User{}, Keys: map[string]*Key{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, s)
		if s.Users == nil {
			s.Users = map[string]*User{}
		}
		if s.Keys == nil {
			s.Keys = map[string]*Key{}
		}
	}
	return s
}

func (s *Store) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLocked()
}

func (s *Store) flushLocked() {
	if !s.dirty {
		return
	}
	if len(s.Usage) > 3000 {
		s.Usage = s.Usage[len(s.Usage)-3000:]
	}
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err == nil {
		_ = os.Rename(tmp, s.path)
		s.dirty = false
	}
}

func (s *Store) FindUserByName(name string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := strings.ToLower(strings.TrimSpace(name))
	for _, u := range s.Users {
		if strings.ToLower(u.Name) == n {
			return u
		}
	}
	return nil
}

func (s *Store) GetUser(id string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Users[id]
}

type storeErr string

func (e storeErr) Error() string { return string(e) }

const ErrDuplicate = storeErr("user exists")

func (s *Store) CreateUser(name, hash, salt string, admin bool) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.Users {
		if strings.EqualFold(u.Name, name) {
			return nil, ErrDuplicate
		}
	}
	u := &User{
		ID: "u_" + randHex(8), Name: name, PassHash: hash, PassSalt: salt,
		Created: time.Now(), IsAdmin: admin, TokLimit: cfg.SignupBonus,
	}
	s.Users[u.ID] = u
	s.dirty = true
	s.flushLocked()
	return u, nil
}

func (s *Store) TouchUser(id string) {
	s.mu.Lock()
	if u := s.Users[id]; u != nil {
		u.LastSeen = time.Now()
		s.dirty = true
	}
	s.mu.Unlock()
}

func (s *Store) KeysOf(userID string) []*Key {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Key, 0, 4)
	for _, k := range s.Keys {
		if k.UserID == userID {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

func (s *Store) CreateKey(userID, name string) (string, *Key) {
	plain := "sk-wb-" + randHex(24)
	k := &Key{
		ID: "k_" + randHex(6), UserID: userID, Name: name,
		Hash: hashToken(plain), Prefix: plain[:14], Created: time.Now(),
		DayLimit: cfg.DefaultDayReq,
	}
	s.mu.Lock()
	s.Keys[k.ID] = k
	s.dirty = true
	s.flushLocked()
	s.mu.Unlock()
	return plain, k
}

func (s *Store) EnsureKey(userID, plain, name string) *Key {
	h := hashToken(plain)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.Keys {
		if k.Hash == h {
			return k
		}
	}
	k := &Key{
		ID: "k_" + randHex(6), UserID: userID, Name: name,
		Hash: h, Prefix: plain[:14], Created: time.Now(),
	}
	s.Keys[k.ID] = k
	s.dirty = true
	s.flushLocked()
	return k
}

func (s *Store) SetKeyDayLimit(keyID string, limit int64) {
	s.mu.Lock()
	if k := s.Keys[keyID]; k != nil {
		k.DayLimit = limit
		s.dirty = true
		s.flushLocked()
	}
	s.mu.Unlock()
}

func (s *Store) RevokeKey(userID, keyID string, admin bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.Keys[keyID]
	if k == nil || (!admin && k.UserID != userID) {
		return false
	}
	k.Revoked = true
	s.dirty = true
	s.flushLocked()
	return true
}

func (s *Store) LookupKey(plain string) (*Key, *User) {
	h := hashToken(plain)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.Keys {
		if k.Hash == h {
			return k, s.Users[k.UserID]
		}
	}
	return nil, nil
}

func (s *Store) QuotaCheck(k *Key, u *User) (bool, string) {
	today := time.Now().Format("2006-01-02")
	s.mu.Lock()
	defer s.mu.Unlock()
	if u == nil || u.Disabled {
		return false, "账号已停用"
	}
	if k.Revoked {
		return false, "Key 已吊销"
	}
	if u.TokLimit > 0 && u.TokUsed >= u.TokLimit {
		return false, "额度已用完"
	}
	if k.DayKey != today {
		k.DayKey, k.DayReq = today, 0
		s.dirty = true
	}
	if k.DayLimit > 0 && k.DayReq >= k.DayLimit {
		return false, "今日请求数已达上限"
	}
	return true, ""
}

func (s *Store) RecordUsage(u *User, k *Key, visible bool, rec Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	u.ReqTotal++
	u.LastSeen = now
	k.ReqTotal++
	k.LastUsed = now
	today := now.Format("2006-01-02")
	if k.DayKey != today {
		k.DayKey, k.DayReq = today, 0
	}
	k.DayReq++
	if rec.Tokens > 0 {
		u.TokUsed += rec.Tokens
		k.TokTotal += rec.Tokens
	}
	if visible {
		s.Usage = append(s.Usage, rec)
	}
	s.dirty = true
}

func (s *Store) UsageOf(userID string, limit int) []Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Usage, 0, limit)
	for i := len(s.Usage) - 1; i >= 0 && len(out) < limit; i-- {
		if s.Usage[i].UserID == userID {
			out = append(out, s.Usage[i])
		}
	}
	return out
}

func (s *Store) AllUsers() []*User {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*User, 0, len(s.Users))
	for _, u := range s.Users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

func (s *Store) SetUserFlags(id string, disabled bool, limit int64) {
	s.mu.Lock()
	if u := s.Users[id]; u != nil {
		u.Disabled = disabled
		if limit > 0 {
			u.TokLimit = limit
		}
		s.dirty = true
		s.flushLocked()
	}
	s.mu.Unlock()
}

func (s *Store) SetQuota(id string, limit int64) {
	s.mu.Lock()
	if u := s.Users[id]; u != nil {
		u.TokLimit = limit
		s.dirty = true
		s.flushLocked()
	}
	s.mu.Unlock()
}

func (s *Store) Stats() (users, keys, reqs, tokens int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.Users {
		users++
		reqs += u.ReqTotal
		tokens += u.TokUsed
	}
	for _, k := range s.Keys {
		if !k.Revoked {
			keys++
		}
	}
	return
}
