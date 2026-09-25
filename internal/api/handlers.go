package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"hookrelay/internal/keys"
	"hookrelay/internal/store"
)

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) createTenant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_name", "name is required (max 100 characters)")
		return
	}

	full, prefix, hash, err := keys.NewAPIKey()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	t, err := s.Store.CreateTenantWithKey(r.Context(), req.Name, prefix, hash)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"tenant":  t,
		"api_key": full,
		"note":    "Save this key now. It is stored hashed and cannot be shown again.",
	})
}

func (s *Server) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL        string   `json:"url"`
		EventTypes []string `json:"event_types"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_url", "url must be an absolute http or https URL")
		return
	}
	// TODO(hardening): block private and loopback IPs here to prevent SSRF
	// (someone registering http://169.254.169.254/ to probe your cloud metadata),
	// with an allowlist flag so the local mock receiver still works in dev.

	if len(req.EventTypes) > 50 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_event_types", "at most 50 event types")
		return
	}
	for _, et := range req.EventTypes {
		if strings.TrimSpace(et) == "" || len(et) > 100 {
			writeError(w, http.StatusUnprocessableEntity, "invalid_event_types", "event types must be 1-100 characters")
			return
		}
	}

	secret, err := keys.NewSigningSecret()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ep, err := s.Store.CreateEndpoint(r.Context(), tenantID(r), u.String(), secret, req.EventTypes)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, ep)
}

func (s *Server) listEndpoints(w http.ResponseWriter, r *http.Request) {
	eps, err := s.Store.ListEndpoints(r.Context(), tenantID(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": eps})
}

func (s *Server) publishEvent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EventType string          `json:"event_type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	req.EventType = strings.TrimSpace(req.EventType)
	if req.EventType == "" || len(req.EventType) > 100 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_event_type", "event_type is required (max 100 characters)")
		return
	}
	if p := bytes.TrimSpace(req.Payload); len(p) == 0 || p[0] != '{' {
		writeError(w, http.StatusUnprocessableEntity, "invalid_payload", "payload must be a JSON object")
		return
	}

	var idemKey *string
	if k := strings.TrimSpace(r.Header.Get("Idempotency-Key")); k != "" {
		if len(k) > 255 {
			writeError(w, http.StatusUnprocessableEntity, "invalid_idempotency_key", "Idempotency-Key max 255 characters")
			return
		}
		idemKey = &k
	}

	res, err := s.Store.PublishEvent(r.Context(), tenantID(r), req.EventType, req.Payload, idemKey)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK // same key again: return the original, create nothing
	}
	writeJSON(w, status, res)
}

func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "event not found")
		return
	}
	ev, deliveries, err := s.Store.GetEvent(r.Context(), tenantID(r), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "event not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": ev, "deliveries": deliveries})
}
