// Package metrics defines the Prometheus metrics for the API and worker.
// Names follow Prometheus conventions: a hookrelay_ prefix, _total for
// counters, _seconds for durations, and only low-cardinality labels.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// ---------- API ----------
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hookrelay_http_requests_total", Help: "API requests by method, route pattern and status code.",
	}, []string{"method", "route", "status"})
	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "hookrelay_http_request_duration_seconds", Help: "API request latency by route pattern.",
		Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
	}, []string{"method", "route"})
	EventsPublished = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hookrelay_events_published_total", Help: "New events accepted (idempotent replays excluded).",
	})
	DeliveriesCreated = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hookrelay_deliveries_created_total", Help: "Deliveries created by fanning events out to endpoints.",
	})

	// ---------- Worker ----------
	DeliveryAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hookrelay_delivery_attempts_total", Help: "Delivery attempts by outcome: succeeded, retry, dead, lease_lost.",
	}, []string{"outcome"})
	DeliveryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "hookrelay_delivery_attempt_duration_seconds", Help: "Time spent on one HTTP delivery attempt.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 15},
	}, []string{"outcome"})
	DeliveryLag = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "hookrelay_delivery_lag_seconds", Help: "From event accepted to delivered, for first-attempt successes (queueing plus sending).",
		Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2, 5, 10, 30, 60},
	})
	InFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "hookrelay_worker_in_flight", Help: "Deliveries this worker is sending right now.",
	})
	Queue = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hookrelay_queue_deliveries", Help: "Deliveries by queue state: due, scheduled, in_flight, dead. Every worker reports the same totals, so use max() across workers.",
	}, []string{"state"})

	// ---------- AI diagnosis ----------
	Diagnoses = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hookrelay_diagnoses_total", Help: "Diagnoses by source: model, cache, rules_check (model answer failed the plausibility check), rules_fallback (model unavailable or invalid), rules (no model configured).",
	}, []string{"source"})
	DiagnosisDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "hookrelay_diagnosis_model_seconds", Help: "Time spent waiting for the model per diagnosis.",
		Buckets: []float64{1, 2, 5, 10, 20, 30, 60, 120, 180},
	})
	DiagnosisTokens = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hookrelay_diagnosis_tokens_total", Help: "Model tokens used by diagnoses, by kind: prompt or completion.",
	}, []string{"kind"})
)

func Handler() http.Handler { return promhttp.Handler() }

// Middleware records each request under its chi route pattern, such as
// /v1/events/{id}. Using the raw path would create a new time series for
// every ID, which is the classic way to overload Prometheus.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		HTTPRequests.WithLabelValues(r.Method, route, strconv.Itoa(sw.status)).Inc()
		HTTPDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}
