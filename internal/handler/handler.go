package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jsojo/goshrt/internal/service"
	"github.com/jsojo/goshrt/internal/store"
)

type Handler struct {
	svc *service.Service
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

type createURLRequest struct {
	URL         string `json:"url"`
	CustomAlias string `json:"custom_alias"`
	TTLSeconds  *int   `json:"ttl_seconds"`
}

type createURLResponse struct {
	ShortCode   string `json:"short_code"`
	OriginalURL string `json:"original_url"`
	CreatedAt   string `json:"created_at"`
}

type listURLsResponse struct {
	URLs  []*store.URL `json:"urls"`
	Page  int          `json:"page"`
	Limit int          `json:"limit"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func isDuplicate(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate")
}

func (h *Handler) CreateURL(w http.ResponseWriter, r *http.Request) {
	var req createURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	var ttl time.Duration
	if req.TTLSeconds != nil && *req.TTLSeconds > 0 {
		ttl = time.Duration(*req.TTLSeconds) * time.Second
	}

	url, err := h.svc.CreateURL(r.Context(), req.URL, req.CustomAlias, ttl)
	if err != nil {
		if isDuplicate(err) {
			writeError(w, http.StatusConflict, "alias already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, createURLResponse{
		ShortCode:   url.ShortCode,
		OriginalURL: url.OriginalURL,
		CreatedAt:   url.CreatedAt.Format(time.RFC3339),
	})
}

func (h *Handler) RedirectURL(w http.ResponseWriter, r *http.Request) {
	shortCode := chi.URLParam(r, "shortCode")

	url, err := h.svc.GetURL(r.Context(), shortCode)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if url == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	if url.ExpiresAt != nil && time.Now().After(*url.ExpiresAt) {
		writeError(w, http.StatusGone, "url has expired")
		return
	}

	http.Redirect(w, r, url.OriginalURL, http.StatusMovedPermanently)
	go h.svc.IncrementClick(context.Background(), shortCode)
}

func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	shortCode := chi.URLParam(r, "shortCode")

	url, err := h.svc.GetStats(r.Context(), shortCode)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if url == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	writeJSON(w, http.StatusOK, url)
}

func (h *Handler) ListURLs(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	urls, err := h.svc.ListURLs(r.Context(), page, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if urls == nil {
		urls = []*store.URL{}
	}

	writeJSON(w, http.StatusOK, listURLsResponse{
		URLs:  urls,
		Page:  page,
		Limit: limit,
	})
}

func (h *Handler) DeleteURL(w http.ResponseWriter, r *http.Request) {
	shortCode := chi.URLParam(r, "shortCode")

	existing, err := h.svc.GetURL(r.Context(), shortCode)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	if err := h.svc.DeleteURL(r.Context(), shortCode); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Liveness reports that the process is running. It must never depend on
// downstream services, otherwise a database outage would restart the app.
func (h *Handler) Liveness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

// Readiness reports whether the instance can serve traffic: Postgres and Redis
// must both answer within the probe timeout.
func (h *Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := h.svc.Ready(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not ready",
			"error":  err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
