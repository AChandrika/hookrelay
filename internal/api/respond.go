package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

const maxBodyBytes = 256 << 10 // 256 KB

// decodeJSON caps the body size and rejects unknown fields, so typos like
// "event_typ" fail loudly instead of being silently ignored.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("body must contain a single JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}

// internalError logs the real error but never sends it to the client, since
// database errors can reveal table names and query details.
func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("internal error",
		"err", err,
		"method", r.Method,
		"path", r.URL.Path,
		"request_id", middleware.GetReqID(r.Context()),
	)
	writeError(w, http.StatusInternalServerError, "internal", "internal server error")
}
