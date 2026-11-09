package handler

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jsojo/goshrt/internal/metrics"
)

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(wrapped.statusCode)
		path := r.URL.Path

		metrics.HTTPRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(r.Method, path).Observe(duration)
	})
}

// requestLogger emits one structured log line per request.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(metricsMiddleware)
	r.Use(requestLogger)

	r.Get("/metrics", promhttp.Handler().ServeHTTP)
	r.Get("/health", h.HealthCheck)
	r.Get("/healthz", h.Liveness)
	r.Get("/readyz", h.Readiness)

	r.Post("/api/url", h.CreateURL)
	r.Get("/api/urls", h.ListURLs)
	r.Get("/api/url/{shortCode}/stats", h.GetStats)
	r.Delete("/api/url/{shortCode}", h.DeleteURL)

	r.Get("/{shortCode}", h.RedirectURL)

	return r
}
