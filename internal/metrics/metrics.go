package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "goshrt_http_requests_total",
			Help: "Total HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "goshrt_http_request_duration_seconds",
			Help:    "HTTP request latency",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	URLsCreatedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "goshrt_urls_created_total",
			Help: "Total URLs shortened",
		},
	)

	URLsDeletedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "goshrt_urls_deleted_total",
			Help: "Total URLs deleted",
		},
	)

	RedirectsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "goshrt_redirects_total",
			Help: "Total redirects by source",
		},
		[]string{"source"},
	)

	CacheOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "goshrt_cache_operations_total",
			Help: "Total cache operations by type",
		},
		[]string{"operation"},
	)

	DBErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "goshrt_db_errors_total",
			Help: "Total database errors by operation",
		},
		[]string{"operation"},
	)

	ActiveURLs = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "goshrt_active_urls",
			Help: "Current number of active shortened URLs",
		},
	)

	ClickSyncBatchSize = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "goshrt_click_sync_batch_size",
			Help: "Number of click deltas in last sync batch",
		},
	)

	BuildInfo = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "goshrt_build_info",
			Help: "Build information of the running goshrt binary",
		},
		[]string{"version", "commit", "go_version"},
	)
)
