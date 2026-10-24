package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"separ/internal/env"
)

const cookieName = "separ_session"

type sessions struct {
	mu       sync.Mutex
	tokens   map[string]time.Time
	failures map[string][]time.Time // per client IP
}

func newSessions() *sessions {
	return &sessions{tokens: map[string]time.Time{}, failures: map[string][]time.Time{}}
}

func (s *sessions) create() string {
	b := make([]byte, 24)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.tokens[tok] = time.Now().Add(14 * 24 * time.Hour)
	s.mu.Unlock()
	return tok
}

func (s *sessions) valid(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.tokens[tok]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.tokens, tok)
		return false
	}
	return true
}

func (s *sessions) revoke(tok string) {
	s.mu.Lock()
	delete(s.tokens, tok)
	s.mu.Unlock()
}

// throttled limits password guessing: 5 failures per 5 minutes per IP.
func (s *sessions) throttled(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := s.failures[ip][:0]
	for _, t := range s.failures[ip] {
		if time.Since(t) < 5*time.Minute {
			keep = append(keep, t)
		}
	}
	s.failures[ip] = keep
	return len(keep) >= 5
}

func (s *sessions) fail(ip string) {
	s.mu.Lock()
	s.failures[ip] = append(s.failures[ip], time.Now())
	s.mu.Unlock()
}

// checkPassword verifies the root password through rpcd, the same
// credentials LuCI and SSH use.
func checkPassword(pw string) bool {
	if env.Mock {
		return pw == "admin"
	}
	args, _ := json.Marshal(map[string]any{"username": "root", "password": pw, "timeout": 60})
	out, err := env.Ubus("session", "login", string(args))
	if err != nil {
		return false
	}
	var r struct {
		Session string `json:"ubus_rpc_session"`
	}
	if json.Unmarshal([]byte(out), &r) != nil || r.Session == "" {
		return false
	}
	env.Ubus("session", "destroy", `{"ubus_rpc_session":"`+r.Session+`"}`)
	return true
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	ip := clientIP(r)
	if s.sess.throttled(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, wait a few minutes")
		return
	}
	if !checkPassword(in.Password) {
		s.sess.fail(ip)
		time.Sleep(700 * time.Millisecond)
		writeErr(w, http.StatusUnauthorized, "wrong password")
		return
	}
	tok := s.sess.create()
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: 14 * 24 * 3600,
	})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.sess.revoke(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, map[string]bool{"ok": true})
}

// requireAuth guards the API; mutating calls must also be JSON, which a
// cross-site form can't send without a CORS preflight.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || !s.sess.valid(c.Value) {
			writeErr(w, http.StatusUnauthorized, "login required")
			return
		}
		// Uploads send raw bytes; a custom header can't be added cross-site
		// without a CORS preflight, so it is as safe as the JSON rule.
		upload := r.Header.Get("X-Separ-Upload") == "1"
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !upload {
			if ct := r.Header.Get("Content-Type"); len(ct) < 16 || ct[:16] != "application/json" {
				writeErr(w, http.StatusUnsupportedMediaType, "json required")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
