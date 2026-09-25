package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"hookrelay/internal/store"
)

const pageSize = 50

var validStatuses = map[string]bool{"pending": true, "in_flight": true, "succeeded": true, "dead": true}

func page[T any](items []T, next *store.Cursor) map[string]any {
	out := map[string]any{"data": items}
	if next != nil {
		out["next_cursor"] = next.Encode()
	}
	return out
}

// uuidParam reads a {name} path parameter and 404s if it isn't a UUID.
func uuidParam(w http.ResponseWriter, r *http.Request, name, what string) (string, bool) {
	id := chi.URLParam(r, name)
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", what+" not found")
		return "", false
	}
	return id, true
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	cur, err := store.DecodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
		return
	}
	items, next, err := s.Store.ListEvents(r.Context(), tenantID(r), pageSize, cur)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page(items, next))
}

func (s *Server) listDeliveries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.DeliveryFilter{Limit: pageSize}

	if st := q.Get("status"); st != "" {
		if !validStatuses[st] {
			writeError(w, http.StatusUnprocessableEntity, "invalid_status", "status must be pending, in_flight, succeeded or dead")
			return
		}
		f.Status = &st
	}
	if ev := q.Get("event_id"); ev != "" {
		if _, err := uuid.Parse(ev); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_event_id", "event_id must be a UUID")
			return
		}
		f.EventID = &ev
	}
	cur, err := store.DecodeCursor(q.Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
		return
	}
	f.After = cur

	items, next, err := s.Store.ListDeliveries(r.Context(), tenantID(r), f)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page(items, next))
}

func (s *Server) getDelivery(w http.ResponseWriter, r *http.Request) {
	id, ok := uuidParam(w, r, "id", "delivery")
	if !ok {
		return
	}
	d, err := s.Store.GetDelivery(r.Context(), tenantID(r), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "delivery not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) replayDelivery(w http.ResponseWriter, r *http.Request) {
	id, ok := uuidParam(w, r, "id", "delivery")
	if !ok {
		return
	}
	err := s.Store.ReplayDelivery(r.Context(), tenantID(r), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "delivery not found")
	case errors.Is(err, store.ErrEndpointDisabled):
		writeError(w, http.StatusConflict, "endpoint_disabled", "This endpoint is disabled. Re-enable it on the Endpoints page, then replay.")
	case errors.Is(err, store.ErrNotReplayable):
		writeError(w, http.StatusConflict, "not_failed", "Only failed deliveries can be replayed.")
	case err != nil:
		s.internalError(w, r, err)
	default:
		// 202 Accepted: queued, not yet delivered.
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
	}
}

func (s *Server) enableEndpoint(w http.ResponseWriter, r *http.Request) {
	id, ok := uuidParam(w, r, "id", "endpoint")
	if !ok {
		return
	}
	ep, err := s.Store.EnableEndpoint(r.Context(), tenantID(r), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ep)
}

func (s *Server) replayFailedForEndpoint(w http.ResponseWriter, r *http.Request) {
	id, ok := uuidParam(w, r, "id", "endpoint")
	if !ok {
		return
	}
	n, err := s.Store.ReplayFailedForEndpoint(r.Context(), tenantID(r), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found")
	case errors.Is(err, store.ErrEndpointDisabled):
		writeError(w, http.StatusConflict, "endpoint_disabled", "Re-enable this endpoint before replaying its failed deliveries.")
	case err != nil:
		s.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusAccepted, map[string]int64{"replayed": n})
	}
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.Stats(r.Context(), tenantID(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
