package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"hookrelay/internal/store"
)

func (s *Server) diagnoseDelivery(w http.ResponseWriter, r *http.Request) {
	id, ok := uuidParam(w, r, "id", "delivery")
	if !ok {
		return
	}
	g, created, err := s.Store.EnqueueDiagnosis(r.Context(), tenantID(r), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "delivery not found")
	case errors.Is(err, store.ErrNothingToDiagnose):
		writeError(w, http.StatusConflict, "no_attempts", "This delivery hasn't been attempted yet, so there's nothing to diagnose.")
	case err != nil:
		s.internalError(w, r, err)
	case created:
		writeJSON(w, http.StatusAccepted, g)
	default:
		writeJSON(w, http.StatusOK, g) // already queued or running
	}
}

func (s *Server) getDiagnosis(w http.ResponseWriter, r *http.Request) {
	id, ok := uuidParam(w, r, "id", "delivery")
	if !ok {
		return
	}
	g, err := s.Store.LatestDiagnosis(r.Context(), tenantID(r), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no diagnosis for this delivery yet")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

// rateLimit limits each tenant to `limit` requests per `window` for one route.
// If Redis is down it fails open: losing rate limiting for a few minutes is
// better than rejecting every request because a helper service is unhealthy.
func (s *Server) rateLimit(name string, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.Limiter == nil || limit <= 0 {
				next.ServeHTTP(w, r)
				return
			}
			allowed, retry, err := s.Limiter.Allow(r.Context(), name+":"+tenantID(r), limit, window)
			if err != nil {
				s.Log.Warn("rate limiter unavailable, allowing request", "err", err)
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				secs := int(math.Ceil(retry.Seconds()))
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				writeError(w, http.StatusTooManyRequests, "rate_limited",
					fmt.Sprintf("Too many requests. Try again in %d seconds.", secs))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
