package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"hookrelay/internal/keys"
	"hookrelay/internal/store"
)

type ctxKey int

const tenantIDKey ctxKey = 0

func tenantID(r *http.Request) string {
	id, _ := r.Context().Value(tenantIDKey).(string)
	return id
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}

// requireAPIKey hashes the presented key and looks the hash up. The raw key is
// never stored, so a database leak doesn't leak working keys.
func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		id, err := s.Store.TenantIDForKey(r.Context(), keys.HashAPIKey(tok))
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid API key")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tenantIDKey, id)))
	})
}

// requireAdmin uses a constant-time comparison so response timing doesn't
// reveal how many leading characters of a guessed token were correct.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := bearerToken(r)
		if !ok || subtle.ConstantTimeCompare([]byte(tok), []byte(s.AdminToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "admin token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
