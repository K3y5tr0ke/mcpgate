package transport

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// PoolMetrics tracks live connection pool statistics.
type PoolMetrics struct {
	Active  atomic.Int64
	Waiting atomic.Int64
	Total   atomic.Int64
}

// PoolConfig controls pool behaviour.
type PoolConfig struct {
	MaxConns        int
	RequestTimeout  time.Duration
	IdleConnTimeout time.Duration
}

// Pool is a bounded HTTP client with semaphore-based concurrency control.
type Pool struct {
	client  *http.Client
	sem     chan struct{}
	Metrics PoolMetrics
}

// NewPool creates a Pool. cfg.MaxConns defaults to 50 if zero.
func NewPool(cfg PoolConfig) *Pool {
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 50
	}
	if cfg.IdleConnTimeout <= 0 {
		cfg.IdleConnTimeout = 90 * time.Second
	}

	tr := &http.Transport{
		MaxIdleConnsPerHost: cfg.MaxConns,
		IdleConnTimeout:     cfg.IdleConnTimeout,
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   cfg.RequestTimeout,
	}

	return &Pool{
		client: client,
		sem:    make(chan struct{}, cfg.MaxConns),
	}
}

// Do executes req within the pool's concurrency limit, blocking until a slot
// is available or ctx is cancelled.
func (p *Pool) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	p.Metrics.Waiting.Add(1)
	select {
	case p.sem <- struct{}{}:
		p.Metrics.Waiting.Add(-1)
	case <-ctx.Done():
		p.Metrics.Waiting.Add(-1)
		return nil, ctx.Err()
	}

	p.Metrics.Active.Add(1)
	p.Metrics.Total.Add(1)
	defer func() {
		<-p.sem
		p.Metrics.Active.Add(-1)
	}()

	return p.client.Do(req.WithContext(ctx))
}
