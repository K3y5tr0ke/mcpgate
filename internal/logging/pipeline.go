package logging

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// Sink writes a batch of entries to a durable backend.
// Implemented by sinks.PostgresSink and sinks.WALSink.
type Sink interface {
	Write(ctx context.Context, entries []store.AuditLogEntry) error
}

// subscriber holds a per-connection fan-out channel for the WS log stream.
type subscriber struct {
	ch     chan store.AuditLogEntry
	cancel func()
}

// Pipeline is the async audit log ingestion pipeline.
//
// Submit() is safe to call from any goroutine (handler path).
// Start() and Drain() are called from the server lifecycle goroutine.
type Pipeline struct {
	entries     chan store.AuditLogEntry
	cfg         config.PipelineConfig
	primary     Sink
	fallback    Sink // nil if WALPath == ""
	chain       *HashChain
	logger      *slog.Logger
	dropped     atomic.Int64
	subMu       sync.RWMutex
	subscribers map[string]*subscriber
	done        chan struct{}
	drain       chan struct{}
}

// NewPipeline creates a Pipeline. initHash is the hash chain tip from the last
// persisted row, or the configured seed if the table was empty.
func NewPipeline(
	cfg config.PipelineConfig,
	primary Sink,
	fallback Sink,
	initHash string,
	logger *slog.Logger,
) *Pipeline {
	return &Pipeline{
		entries:     make(chan store.AuditLogEntry, cfg.BufferSize),
		cfg:         cfg,
		primary:     primary,
		fallback:    fallback,
		chain:       NewHashChain(initHash),
		logger:      logger,
		subscribers: make(map[string]*subscriber),
		done:        make(chan struct{}),
		drain:       make(chan struct{}),
	}
}

// Submit enqueues entry for async write. Non-blocking: if the channel is full the
// entry is dropped and the dropped counter is incremented.
func (p *Pipeline) Submit(entry store.AuditLogEntry) {
	select {
	case p.entries <- entry:
	default:
		p.dropped.Add(1)
		p.logger.Warn("audit pipeline full — entry dropped",
			"dropped_total", p.dropped.Load())
	}
}

// DroppedCount returns the total number of entries dropped due to backpressure.
func (p *Pipeline) DroppedCount() int64 { return p.dropped.Load() }

// Subscribe registers a channel that receives copies of written entries.
// The returned cancel func deregisters the subscriber and closes the channel.
func (p *Pipeline) Subscribe(id string, bufSize int) (<-chan store.AuditLogEntry, func()) {
	ch := make(chan store.AuditLogEntry, bufSize)
	var once sync.Once
	cancelFn := func() {
		once.Do(func() {
			p.subMu.Lock()
			delete(p.subscribers, id)
			p.subMu.Unlock()
			close(ch)
		})
	}
	sub := &subscriber{ch: ch, cancel: cancelFn}
	p.subMu.Lock()
	p.subscribers[id] = sub
	p.subMu.Unlock()
	return ch, cancelFn
}

// Start launches the writer goroutine. ctx is forwarded to sink Write calls.
func (p *Pipeline) Start(ctx context.Context) {
	go p.writer(ctx)
}

// Drain signals the writer to flush all pending entries and blocks until done.
// Call after http.Shutdown() so all in-flight handlers have called Submit first.
func (p *Pipeline) Drain() {
	close(p.done)
	<-p.drain
}

func (p *Pipeline) writer(ctx context.Context) {
	defer close(p.drain)

	ticker := time.NewTicker(p.cfg.FlushInterval)
	defer ticker.Stop()

	batch := make([]store.AuditLogEntry, 0, p.cfg.BatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}

		// Normalize fields that change across a Postgres round-trip so the hash
		// computed here matches what VerifyHashChain recomputes from stored rows.
		for i := range batch {
			batch[i].Timestamp = batch[i].Timestamp.UTC().Truncate(time.Microsecond)
			if batch[i].Metadata == nil {
				batch[i].Metadata = map[string]any{}
			}
		}

		// Compute hash chain sequentially — single goroutine guarantees correctness.
		for i := range batch {
			if err := p.chain.Next(&batch[i]); err != nil {
				p.logger.Error("hashchain compute failed", "err", err)
			}
		}

		if err := p.primary.Write(ctx, batch); err != nil {
			p.logger.Error("audit log primary write failed",
				"err", err, "count", len(batch))
			if p.fallback != nil {
				if wErr := p.fallback.Write(ctx, batch); wErr != nil {
					p.logger.Error("audit log WAL fallback failed",
						"err", wErr, "count", len(batch))
					p.dropped.Add(int64(len(batch)))
				}
			} else {
				p.dropped.Add(int64(len(batch)))
			}
		} else {
			p.fanOut(batch)
		}

		batch = batch[:0]
	}

	for {
		select {
		case entry := <-p.entries:
			batch = append(batch, entry)
			if len(batch) >= p.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-p.done:
			// Drain remaining entries in the channel before exiting.
			for {
				select {
				case entry := <-p.entries:
					batch = append(batch, entry)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (p *Pipeline) fanOut(batch []store.AuditLogEntry) {
	p.subMu.RLock()
	defer p.subMu.RUnlock()
	for _, sub := range p.subscribers {
		for _, e := range batch {
			select {
			case sub.ch <- e:
			default: // subscriber slow — drop silently
			}
		}
	}
}
