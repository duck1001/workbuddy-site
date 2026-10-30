package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type modelCacheT struct {
	mu     sync.Mutex
	at     time.Time
	body   []byte
	status int
}

func newModelCache() *modelCacheT { return &modelCacheT{} }

func extractKey(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
			return strings.TrimSpace(h[7:])
		}
	}
	if h := r.Header.Get("x-api-key"); h != "" {
		return strings.TrimSpace(h)
	}
	return ""
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	plain := extractKey(r)
	k, u := st.LookupKey(plain)
	if k == nil {
		writeAPIError(w, 401, "invalid_api_key", "无效的 API Key，请在控制台创建")
		return
	}
	if ok, reason := st.QuotaCheck(k, u); !ok {
		writeAPIError(w, 429, "quota_exceeded", reason)
		return
	}

	start := time.Now()
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	target := s.upstreamBase + r.URL.RequestURI()
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		writeAPIError(w, 400, "bad_request", err.Error())
		return
	}
	for name, vals := range r.Header {
		if strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "X-Api-Key") ||
			strings.EqualFold(name, "Host") || strings.EqualFold(name, "Connection") {
			continue
		}
		for _, v := range vals {
			req.Header.Add(name, v)
		}
	}
	req.Header.Set("Authorization", "Bearer "+upstreamKey)
	req.Header.Set("X-Forwarded-For", clientIP(r))

	resp, err := s.client.Do(req)
	if err != nil {
		writeAPIError(w, 502, "upstream_error", "上游不可达: "+err.Error())
		return
	}
	defer resp.Body.Close()

	for _, h := range []string{"Content-Type", "Cache-Control", "X-Request-Id"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("X-Site-Key", k.Prefix)
	w.WriteHeader(resp.StatusCode)

	scan := &usageScan{limit: 512 << 10}
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 16<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			_, _ = scan.Write(buf[:n])
			if _, werr := w.Write(buf[:n]); werr != nil {
				break
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}

	tokens := scan.Tokens()
	model := scan.Model()
	visible := strings.Contains(r.URL.Path, "completions") || strings.Contains(r.URL.Path, "messages") || strings.Contains(r.URL.Path, "responses")
	st.RecordUsage(u, k, visible, Usage{
		Time: time.Now(), UserID: u.ID, KeyID: k.ID, Model: model,
		Tokens: tokens, Status: resp.StatusCode,
		Latency: time.Since(start).Milliseconds(), IP: clientIP(r),
		Stream: strings.Contains(resp.Header.Get("Content-Type"), "event-stream"),
	})
	log.Printf("%s %s %s -> %d tok=%d %dms", k.Prefix, r.Method, r.URL.Path, resp.StatusCode, tokens, time.Since(start).Milliseconds())
}

type usageScan struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (u *usageScan) Write(p []byte) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.buf.Write(p)
	if u.buf.Len() > u.limit {
		b := u.buf.Bytes()
		keep := make([]byte, u.limit/2)
		copy(keep, b[u.buf.Len()-len(keep):])
		u.buf.Reset()
		u.buf.Write(keep)
	}
	return len(p), nil
}

type usageBlock struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

func (u *usageScan) Tokens() int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	b := u.buf.Bytes()
	var best int64
	for i := 0; i+8 < len(b); i++ {
		if string(b[i:i+8]) != `"usage":` {
			continue
		}
		j := i + 8
		for j < len(b) && (b[j] == ' ' || b[j] == '\t') {
			j++
		}
		if j >= len(b) || b[j] != '{' {
			continue
		}
		var ub usageBlock
		if err := json.NewDecoder(bytes.NewReader(b[j:])).Decode(&ub); err == nil {
			v := ub.TotalTokens
			if v == 0 {
				v = ub.PromptTokens + ub.CompletionTokens
			}
			if v > best {
				best = v
			}
		}
	}
	return best
}

func (u *usageScan) Model() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	b := u.buf.Bytes()
	idx := bytes.Index(b, []byte(`"model":"`))
	if idx < 0 {
		return ""
	}
	rest := b[idx+9:]
	if end := bytes.IndexByte(rest, '"'); end > 0 && end < 64 {
		return string(rest[:end])
	}
	return ""
}

func writeAPIError(w http.ResponseWriter, code int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": msg, "type": typ, "code": typ},
	})
}

var _ = io.Discard
