package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-gost/llm-gw/internal/config"
)

const (
	sessionCookie = "llm_gw_session"
	csrfCookie    = "llm_gw_csrf"
	csrfHeader    = "X-CSRF-Token"
)

func newToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// hashToken keeps raw session tokens out of the database.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	if s.passwordHash == "" || !config.VerifyPassword(s.passwordHash, in.Password) {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	token := newToken(32)
	csrf := newToken(32)

	ttl := s.ttl
	if in.Remember {
		ttl = s.rememberTTL
	}
	expires := time.Now().Add(ttl)
	// A non-remembered session is a browser-session cookie (MaxAge 0); a
	// remembered one survives both browser restarts and llm-gw restarts.
	maxAge := 0
	if in.Remember {
		maxAge = int(ttl.Seconds())
	}

	s.mu.Lock()
	s.sessions[hashToken(token)] = expires
	s.mu.Unlock()

	if in.Remember && s.stats != nil {
		if err := s.stats.PutSession(context.Background(), hashToken(token), expires); err != nil {
			s.log.Warnf("persist session: %v", err)
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: maxAge,
	})
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookie, Value: csrf, Path: "/",
		SameSite: http.SameSiteLaxMode, MaxAge: maxAge,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.mu.Lock()
		delete(s.sessions, hashToken(c.Value))
		s.mu.Unlock()
		if s.stats != nil {
			if err := s.stats.DeleteSession(context.Background(), hashToken(c.Value)); err != nil {
				s.log.Warnf("delete session: %v", err)
			}
		}
	}
	for _, name := range []string{sessionCookie, csrfCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || !s.validSession(c.Value) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			csrf, err := r.Cookie(csrfCookie)
			got := r.Header.Get(csrfHeader)
			if err != nil || csrf.Value == "" || subtle.ConstantTimeCompare([]byte(got), []byte(csrf.Value)) != 1 {
				writeError(w, http.StatusForbidden, "csrf token mismatch")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) validSession(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := hashToken(token)
	exp, ok := s.sessions[key]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.sessions, key)
		return false
	}
	return true
}
