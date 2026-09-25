// Package api is the HTTP layer: routing, auth, validation, and JSON responses.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"hookrelay/internal/store"
)

type Server struct {
	Store      *store.Store
	AdminToken string
	Log        *slog.Logger
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(10 * time.Second))

	r.Get("/healthz", s.healthz)

	// Admin-only: bootstrap a tenant and get its first API key.
	r.With(s.requireAdmin).Post("/v1/tenants", s.createTenant)

	// Tenant API: everything below needs "Authorization: Bearer hr_...".
	r.Group(func(r chi.Router) {
		r.Use(s.requireAPIKey)

		r.Post("/v1/endpoints", s.createEndpoint)
		r.Get("/v1/endpoints", s.listEndpoints)
		r.Post("/v1/endpoints/{id}/enable", s.enableEndpoint)
		r.Post("/v1/endpoints/{id}/replay-failed", s.replayFailedForEndpoint)

		r.Post("/v1/events", s.publishEvent)
		r.Get("/v1/events", s.listEvents)
		r.Get("/v1/events/{id}", s.getEvent)

		r.Get("/v1/deliveries", s.listDeliveries)
		r.Get("/v1/deliveries/{id}", s.getDelivery)
		r.Post("/v1/deliveries/{id}/replay", s.replayDelivery)

		r.Get("/v1/stats", s.stats)
	})
	return r
}
