package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
	"github.com/K3y5tr0ke/mcpgate/internal/proxy/transport"
)

const serverCacheTTL = 30 * time.Second

// Handler is the core MCP proxy. It resolves an upstream server by name,
// creates or reuses a Transport, then forwards JSON-RPC messages verbatim.
type Handler struct {
	servers  store.ServerStore
	proxyCfg config.ProxyConfig
	logger   *slog.Logger

	// transport cache
	transports sync.Map // key: server name → transport.Transport

	// server config cache (avoids a DB hit on every request)
	cacheMu  sync.RWMutex
	cache    map[string]*store.MCPServer
	cacheExp map[string]time.Time
}

// NewHandler creates a proxy Handler.
func NewHandler(cfg config.ProxyConfig, servers store.ServerStore, logger *slog.Logger) *Handler {
	return &Handler{
		servers:  servers,
		proxyCfg: cfg,
		logger:   logger,
		cache:    make(map[string]*store.MCPServer),
		cacheExp: make(map[string]time.Time),
	}
}

// HandleStreamableHTTP is the chi handler for POST /mcp/{serverName}/message.
func (h *Handler) HandleStreamableHTTP(w http.ResponseWriter, r *http.Request) {
	serverName := chi.URLParam(r, "serverName")

	body, err := h.readBody(r)
	if err != nil {
		h.writeError(w, nil, CodeInvalidRequest, "cannot read request body")
		return
	}

	msg, err := Parse(body)
	if err != nil {
		h.writeError(w, nil, CodeParseError, err.Error())
		return
	}

	tr, err := h.transportFor(r.Context(), serverName)
	if err != nil {
		h.logger.Error("resolve transport", "server", serverName, "err", err)
		h.writeError(w, msg.ID, CodeInternalError, fmt.Sprintf("server %q not available", serverName))
		return
	}

	resp, err := tr.Send(r.Context(), body)
	if err != nil {
		h.logger.Error("upstream error", "server", serverName, "method", msg.Method, "err", err)
		h.writeError(w, msg.ID, CodeInternalError, "upstream error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp) //nolint:errcheck
}

// HandleSSEStream is the chi handler for GET /mcp/{serverName}/sse.
func (h *Handler) HandleSSEStream(w http.ResponseWriter, r *http.Request) {
	serverName := chi.URLParam(r, "serverName")

	_, err := h.transportFor(r.Context(), serverName)
	if err != nil {
		http.Error(w, fmt.Sprintf("server %q not available", serverName), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	messages := make(chan []byte, 32)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		for {
			select {
			case msg := <-messages:
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			case <-ctx.Done():
				return
			}
		}
	}()

	<-ctx.Done()
}

// HandleSSEMessage is the chi handler for POST /mcp/{serverName}/sse.
func (h *Handler) HandleSSEMessage(w http.ResponseWriter, r *http.Request) {
	serverName := chi.URLParam(r, "serverName")

	body, err := h.readBody(r)
	if err != nil {
		h.writeError(w, nil, CodeInvalidRequest, "cannot read request body")
		return
	}

	msg, err := Parse(body)
	if err != nil {
		h.writeError(w, nil, CodeParseError, err.Error())
		return
	}

	tr, err := h.transportFor(r.Context(), serverName)
	if err != nil {
		h.writeError(w, msg.ID, CodeInternalError, fmt.Sprintf("server %q not available", serverName))
		return
	}

	resp, err := tr.Send(r.Context(), body)
	if err != nil {
		h.writeError(w, msg.ID, CodeInternalError, "upstream error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp) //nolint:errcheck
}

// transportFor returns a cached Transport for serverName, creating one if needed.
func (h *Handler) transportFor(ctx context.Context, serverName string) (transport.Transport, error) {
	if v, ok := h.transports.Load(serverName); ok {
		return v.(transport.Transport), nil
	}

	srv, err := h.serverConfig(ctx, serverName)
	if err != nil {
		return nil, fmt.Errorf("server config: %w", err)
	}
	if !srv.IsEnabled {
		return nil, fmt.Errorf("server %q is disabled", serverName)
	}

	tr, err := h.buildTransport(srv)
	if err != nil {
		return nil, fmt.Errorf("build transport: %w", err)
	}

	// Store with compare-and-swap to avoid a race on first creation.
	actual, loaded := h.transports.LoadOrStore(serverName, tr)
	if loaded {
		tr.Close() //nolint:errcheck
		return actual.(transport.Transport), nil
	}
	return tr, nil
}

// buildTransport creates the appropriate transport.Transport for srv.
func (h *Handler) buildTransport(srv *store.MCPServer) (transport.Transport, error) {
	pool := transport.NewPool(transport.PoolConfig{
		MaxConns:        h.proxyCfg.ConnectionPool.MaxPerServer,
		IdleConnTimeout: h.proxyCfg.ConnectionPool.IdleTimeout,
		RequestTimeout:  h.proxyCfg.DefaultTimeout,
	})

	switch srv.Transport {
	case "streamable_http":
		endpoint, _ := srv.Config["endpoint"].(string)
		if endpoint == "" {
			return nil, fmt.Errorf("streamable_http transport requires config.endpoint")
		}
		return transport.NewStreamableHTTP(endpoint, pool), nil

	case "sse":
		sseEP, _ := srv.Config["sse_endpoint"].(string)
		msgEP, _ := srv.Config["message_endpoint"].(string)
		if sseEP == "" || msgEP == "" {
			return nil, fmt.Errorf("sse transport requires config.sse_endpoint and config.message_endpoint")
		}
		return transport.NewSSE(sseEP, msgEP, pool), nil

	case "stdio":
		cmd, _ := srv.Config["command"].(string)
		if cmd == "" {
			return nil, fmt.Errorf("stdio transport requires config.command")
		}
		var args []string
		if raw, ok := srv.Config["args"].([]any); ok {
			for _, a := range raw {
				if s, ok := a.(string); ok {
					args = append(args, s)
				}
			}
		}
		return transport.NewStdio(cmd, args), nil

	default:
		return nil, fmt.Errorf("unknown transport type %q", srv.Transport)
	}
}

// serverConfig returns the MCPServer config, using a short-lived cache.
func (h *Handler) serverConfig(ctx context.Context, name string) (*store.MCPServer, error) {
	h.cacheMu.RLock()
	srv, ok := h.cache[name]
	exp := h.cacheExp[name]
	h.cacheMu.RUnlock()

	if ok && time.Now().Before(exp) {
		return srv, nil
	}

	srv, err := h.servers.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}

	h.cacheMu.Lock()
	h.cache[name] = srv
	h.cacheExp[name] = time.Now().Add(serverCacheTTL)
	h.cacheMu.Unlock()

	return srv, nil
}

func (h *Handler) readBody(r *http.Request) ([]byte, error) {
	limit := h.proxyCfg.MaxRequestSize
	if limit <= 0 {
		limit = 4 << 20 // 4 MiB default
	}
	return io.ReadAll(io.LimitReader(r.Body, limit+1))
}

func (h *Handler) writeError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // JSON-RPC errors still use 200
	w.Write(ErrorResponse(id, code, message)) //nolint:errcheck
}
