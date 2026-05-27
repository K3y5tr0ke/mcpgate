package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/K3y5tr0ke/mcpgate/examples/mcp-servers/common"
)

func main() {
	serverName := os.Getenv("MCP_SERVER_NAME")
	if serverName == "" {
		serverName = "sse"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := common.NewHandler(serverName)

	mux := http.NewServeMux()

	// POST /message — inline SSE response.
	// The mcpgate SSE transport's Send() POSTs here and reads the first
	// "data: ..." line from the response body via readFirstSSEData().
	mux.HandleFunc("POST /message", func(w http.ResponseWriter, r *http.Request) {
		var req common.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = fmt.Fprintf(w, "data: %s\n\n", common.ParseErrorResponse())
			return
		}
		logger.Info("request", "method", req.Method, "notification", req.IsNotification())

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")

		resp := h.Handle(&req)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", resp)
	})

	// GET /sse — persistent event stream.
	// The mcpgate SSE transport's Stream() GETs this endpoint and holds the
	// connection open for server-push notifications. We send an initial
	// "connected" event then hold until the client disconnects.
	mux.HandleFunc("GET /sse", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		logger.Info("sse stream opened", "remote", r.RemoteAddr)
		_, _ = fmt.Fprintf(w, "event: connected\ndata: {}\n\n")
		flusher.Flush()

		<-r.Context().Done()
		logger.Info("sse stream closed", "remote", r.RemoteAddr)
	})

	logger.Info("starting", "server", serverName, "port", port, "transport", "sse")
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // disabled: SSE GET /sse streams indefinitely
	}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
