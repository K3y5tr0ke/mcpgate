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
		serverName = "streamable-http"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := common.NewHandler(serverName)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, r *http.Request) {
		var req common.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(common.ParseErrorResponse())
			return
		}
		logger.Info("request", "method", req.Method, "notification", req.IsNotification())

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(h.Handle(&req))
	})

	logger.Info("starting", "server", serverName, "port", port, "transport", "streamable_http")
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
