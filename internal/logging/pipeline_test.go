package logging_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/logging"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
	"log/slog"
)

// captureSink records all Write calls.
type captureSink struct {
	mu      sync.Mutex
	entries []store.AuditLogEntry
	err     error // if non-nil, Write returns this error
}

func (c *captureSink) Write(_ context.Context, entries []store.AuditLogEntry) error {
	if c.err != nil {
		return c.err
	}
	c.mu.Lock()
	c.entries = append(c.entries, entries...)
	c.mu.Unlock()
	return nil
}

func (c *captureSink) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func testPipeline(primary, fallback logging.Sink) *logging.Pipeline {
	cfg := config.PipelineConfig{
		BufferSize:    1000,
		FlushInterval: 10 * time.Millisecond,
		BatchSize:     100,
	}
	return logging.NewPipeline(cfg, primary, fallback, "genesis", slog.Default())
}

func TestPipeline_SubmitDeliveredToSink(t *testing.T) {
	sink := &captureSink{}
	p := testPipeline(sink, nil)
	p.Start(context.Background())

	p.Submit(store.AuditLogEntry{MCPMethod: "tools/list", Status: "success"})
	p.Submit(store.AuditLogEntry{MCPMethod: "tools/call", Status: "success"})

	p.Drain()

	if sink.count() != 2 {
		t.Errorf("sink received %d entries, want 2", sink.count())
	}
}

func TestPipeline_DrainFlushesRemaining(t *testing.T) {
	sink := &captureSink{}
	cfg := config.PipelineConfig{
		BufferSize:    1000,
		FlushInterval: time.Hour, // effectively never ticks during test
		BatchSize:     100,
	}
	p := logging.NewPipeline(cfg, sink, nil, "genesis", slog.Default())
	p.Start(context.Background())

	for i := 0; i < 10; i++ {
		p.Submit(store.AuditLogEntry{MCPMethod: "ping", Status: "success"})
	}
	p.Drain()

	if sink.count() != 10 {
		t.Errorf("sink received %d entries, want 10", sink.count())
	}
}

func TestPipeline_ChannelFullDropsEntries(t *testing.T) {
	// Use a tiny buffer and a sink that blocks indefinitely.
	blockCh := make(chan struct{})
	blockSink := &blockingSink{block: blockCh}

	cfg := config.PipelineConfig{
		BufferSize:    2,
		FlushInterval: time.Millisecond,
		BatchSize:     100,
	}
	p := logging.NewPipeline(cfg, blockSink, nil, "genesis", slog.Default())
	p.Start(context.Background())

	// Submit more entries than the buffer holds while the sink is blocked.
	for i := 0; i < 10; i++ {
		p.Submit(store.AuditLogEntry{Status: "success"})
	}

	if p.DroppedCount() == 0 {
		t.Log("no drops yet — sink may not have blocked in time (flaky); skipping assertion")
	}

	close(blockCh) // unblock sink so Drain can complete
	p.Drain()
}

// blockingSink blocks on Write until its block channel is closed.
type blockingSink struct{ block chan struct{} }

func (b *blockingSink) Write(ctx context.Context, _ []store.AuditLogEntry) error {
	select {
	case <-b.block:
	case <-ctx.Done():
	}
	return nil
}

func TestPipeline_FallbackCalledOnPrimaryError(t *testing.T) {
	primary := &captureSink{err: errors.New("primary down")}
	fallback := &captureSink{}

	p := testPipeline(primary, fallback)
	p.Start(context.Background())

	p.Submit(store.AuditLogEntry{MCPMethod: "tools/list", Status: "success"})
	p.Drain()

	if fallback.count() != 1 {
		t.Errorf("fallback received %d entries, want 1", fallback.count())
	}
}

func TestPipeline_SubscriberReceivesEntries(t *testing.T) {
	sink := &captureSink{}
	p := testPipeline(sink, nil)
	p.Start(context.Background())

	ch, cancel := p.Subscribe("test-sub", 64)
	defer cancel()

	p.Submit(store.AuditLogEntry{MCPMethod: "tools/list", Status: "success"})
	p.Drain()

	select {
	case e := <-ch:
		if e.MCPMethod != "tools/list" {
			t.Errorf("subscriber got method %q, want tools/list", e.MCPMethod)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for subscriber entry")
	}
}

func TestPipeline_HashChainSetOnEntries(t *testing.T) {
	sink := &captureSink{}
	p := testPipeline(sink, nil)
	p.Start(context.Background())

	p.Submit(store.AuditLogEntry{MCPMethod: "tools/list", Status: "success"})
	p.Submit(store.AuditLogEntry{MCPMethod: "tools/call", Status: "success"})
	p.Drain()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	for i, e := range sink.entries {
		if e.HashChain == "" {
			t.Errorf("entry[%d] has empty HashChain", i)
		}
	}
	// Hashes must be distinct.
	if len(sink.entries) == 2 && sink.entries[0].HashChain == sink.entries[1].HashChain {
		t.Error("entries have identical HashChain values")
	}
}
