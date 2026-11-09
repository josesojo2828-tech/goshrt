package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jsojo/goshrt/internal/metrics"
	"github.com/jsojo/goshrt/internal/store"
)

type Store interface {
	Create(ctx context.Context, url *store.URL) error
	GetByShortCode(ctx context.Context, shortCode string) (*store.URL, error)
	List(ctx context.Context, offset, limit int) ([]*store.URL, error)
	UpdateClicks(ctx context.Context, shortCode string, clicks int64) error
	SoftDelete(ctx context.Context, shortCode string) error
	GetAllActive(ctx context.Context) ([]*store.URL, error)
	Ping(ctx context.Context) error
}

type Cache interface {
	Get(ctx context.Context, shortCode string) (*store.URL, error)
	Set(ctx context.Context, url *store.URL) error
	Delete(ctx context.Context, shortCode string) error
	IncrementClicks(ctx context.Context, shortCode string) (int64, error)
	Ping(ctx context.Context) error
}

type clickDelta struct {
	value int64
}

type Service struct {
	store  Store
	cache  Cache
	deltas sync.Map
}

func New(store Store, cache Cache) *Service {
	return &Service{
		store: store,
		cache: cache,
	}
}

// Ready reports whether every downstream dependency is reachable.
// It is used by the readiness probe to keep traffic away from an unhealthy instance.
func (s *Service) Ready(ctx context.Context) error {
	if err := s.store.Ping(ctx); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	if err := s.cache.Ping(ctx); err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	return nil
}

func (s *Service) CreateURL(ctx context.Context, originalURL string, customAlias string, ttl time.Duration) (*store.URL, error) {
	shortCode := customAlias
	if shortCode == "" {
		var err error
		shortCode, err = store.GenerateShortCode(6)
		if err != nil {
			return nil, fmt.Errorf("generating short code: %w", err)
		}
	}

	now := time.Now().UTC()
	url := &store.URL{
		ShortCode:   shortCode,
		OriginalURL: originalURL,
		CreatedAt:   now,
		UpdatedAt:   now,
		IsActive:    true,
	}

	if ttl > 0 {
		expires := now.Add(ttl)
		url.ExpiresAt = &expires
	}

	const maxRetries = 3
	for i := 0; i < maxRetries; i++ {
		if i > 0 && customAlias == "" {
			code, err := store.GenerateShortCode(6)
			if err != nil {
				return nil, fmt.Errorf("generating short code on retry: %w", err)
			}
			url.ShortCode = code
		}

		err := s.store.Create(ctx, url)
		if err != nil {
			if isUniqueViolation(err) && i < maxRetries-1 {
				continue
			}
			metrics.DBErrorsTotal.WithLabelValues("create").Inc()
			return nil, fmt.Errorf("creating url in store: %w", err)
		}
		break
	}

	if err := s.cache.Set(ctx, url); err != nil {
		return nil, fmt.Errorf("caching url: %w", err)
	}

	metrics.ActiveURLs.Inc()
	metrics.URLsCreatedTotal.Inc()
	return url, nil
}

func (s *Service) GetURL(ctx context.Context, shortCode string) (*store.URL, error) {
	url, err := s.cache.Get(ctx, shortCode)
	if err != nil {
		return nil, fmt.Errorf("cache get: %w", err)
	}
	if url != nil {
		metrics.RedirectsTotal.WithLabelValues("cache_hit").Inc()
		metrics.CacheOperationsTotal.WithLabelValues("hit").Inc()
		return url, nil
	}

	metrics.CacheOperationsTotal.WithLabelValues("miss").Inc()
	metrics.RedirectsTotal.WithLabelValues("cache_miss").Inc()

	url, err = s.store.GetByShortCode(ctx, shortCode)
	if err != nil {
		metrics.DBErrorsTotal.WithLabelValues("get").Inc()
		return nil, fmt.Errorf("store get: %w", err)
	}
	if url == nil {
		return nil, nil
	}

	if err := s.cache.Set(ctx, url); err != nil {
		return nil, fmt.Errorf("populating cache: %w", err)
	}
	metrics.CacheOperationsTotal.WithLabelValues("set").Inc()

	return url, nil
}

func (s *Service) GetStats(ctx context.Context, shortCode string) (*store.URL, error) {
	url, err := s.store.GetByShortCode(ctx, shortCode)
	if err != nil {
		metrics.DBErrorsTotal.WithLabelValues("get").Inc()
		return nil, fmt.Errorf("store get stats: %w", err)
	}
	return url, nil
}

func (s *Service) ListURLs(ctx context.Context, page, limit int) ([]*store.URL, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	urls, err := s.store.List(ctx, offset, limit)
	if err != nil {
		metrics.DBErrorsTotal.WithLabelValues("list").Inc()
		return nil, fmt.Errorf("store list: %w", err)
	}
	return urls, nil
}

func (s *Service) DeleteURL(ctx context.Context, shortCode string) error {
	if err := s.store.SoftDelete(ctx, shortCode); err != nil {
		metrics.DBErrorsTotal.WithLabelValues("delete").Inc()
		return fmt.Errorf("store soft delete: %w", err)
	}
	if err := s.cache.Delete(ctx, shortCode); err != nil {
		return fmt.Errorf("cache delete: %w", err)
	}
	metrics.ActiveURLs.Dec()
	metrics.URLsDeletedTotal.Inc()
	return nil
}

func (s *Service) SeedCache(ctx context.Context) (int, error) {
	urls, err := s.store.GetAllActive(ctx)
	if err != nil {
		return 0, fmt.Errorf("getting active urls: %w", err)
	}
	for _, u := range urls {
		if err := s.cache.Set(ctx, u); err != nil {
			return 0, fmt.Errorf("seeding url %s: %w", u.ShortCode, err)
		}
	}
	metrics.ActiveURLs.Set(float64(len(urls)))
	return len(urls), nil
}

func (s *Service) IncrementClick(ctx context.Context, shortCode string) (int64, error) {
	newClicks, err := s.cache.IncrementClicks(ctx, shortCode)
	if err != nil {
		return 0, err
	}

	val, _ := s.deltas.LoadOrStore(shortCode, new(clickDelta))
	atomic.AddInt64(&val.(*clickDelta).value, 1)

	return newClicks, nil
}

func (s *Service) SyncClicks(ctx context.Context) error {
	var lastErr error
	var batchSize int
	s.deltas.Range(func(key, value any) bool {
		shortCode := key.(string)
		delta := atomic.SwapInt64(&value.(*clickDelta).value, 0)

		if delta > 0 {
			if err := s.store.UpdateClicks(ctx, shortCode, delta); err != nil {
				atomic.AddInt64(&value.(*clickDelta).value, delta)
				lastErr = fmt.Errorf("updating clicks for %s: %w", shortCode, err)
				return false
			}
			batchSize++
		}

		s.deltas.Delete(key)
		return true
	})

	metrics.ClickSyncBatchSize.Set(float64(batchSize))
	return lastErr
}

func isUniqueViolation(err error) bool {
	return err != nil && (contains(err.Error(), "unique") || contains(err.Error(), "duplicate"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
