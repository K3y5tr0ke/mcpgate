package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/K3y5tr0ke/mcpgate/internal/logging"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

type logHandler struct {
	store    store.AuditLogStore
	pipeline *logging.Pipeline
	logger   *slog.Logger
}

var wsUpgrader = websocket.Upgrader{
	// CheckOrigin always returns true — the admin API already requires the admin
	// role, so a separate CORS check here adds no security.
	CheckOrigin: func(*http.Request) bool { return true },
}

// list handles GET /api/v1/admin/logs
func (h *logHandler) list(w http.ResponseWriter, r *http.Request) {
	filter, page, ok := parseLogParams(w, r)
	if !ok {
		return
	}

	entries, total, err := h.store.Query(r.Context(), filter, page)
	if err != nil {
		h.logger.Error("audit log query failed", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "query failed")
		return
	}

	pageSize := page.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	pages := int64(0)
	if pageSize > 0 {
		pages = (total + int64(pageSize) - 1) / int64(pageSize)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"total":   total,
		"page":    page.Page,
		"pages":   pages,
	})
}

// export handles GET /api/v1/admin/logs/export
func (h *logHandler) export(w http.ResponseWriter, r *http.Request) {
	filter, _, ok := parseLogParams(w, r)
	if !ok {
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		writeJSONError(w, http.StatusBadRequest, "format must be json or csv")
		return
	}

	ext := format
	ct := "application/json"
	if format == "csv" {
		ct = "text/csv"
	}

	ts := time.Now().UTC().Format("20060102-150405")
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition",
		`attachment; filename="mcpgate-logs-`+ts+`.`+ext+`"`)
	w.WriteHeader(http.StatusOK)

	if err := h.store.Export(r.Context(), filter, format, w); err != nil {
		h.logger.Error("audit log export failed", "err", err)
	}
}

// verify handles GET /api/v1/admin/logs/verify?from=...&to=...&genesis=...
// It re-derives the hash chain for all entries in [from, to] and reports
// whether every entry's stored hash matches the recomputed value.
func (h *logHandler) verify(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	from, err := time.Parse(time.RFC3339, q.Get("from"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid 'from' timestamp (RFC3339 required)")
		return
	}
	to, err := time.Parse(time.RFC3339, q.Get("to"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid 'to' timestamp (RFC3339 required)")
		return
	}
	if from.After(to) {
		writeJSONError(w, http.StatusBadRequest, "'from' must be before 'to'")
		return
	}
	genesis := q.Get("genesis")

	ok, err := h.store.VerifyHashChain(r.Context(), from, to, genesis)
	if err != nil {
		h.logger.Error("verify hash chain failed", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "verification failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   ok,
		"from": from,
		"to":   to,
	})
}

// stream handles WS GET /api/v1/admin/logs/stream
// Auth note: the RequireRole("admin") middleware runs before wsUpgrader.Upgrade,
// so the WebSocket connection is never established for unauthenticated callers.
// Browser clients authenticate via the mcpgate_session cookie (set by SAML ACS);
// the auth middleware reads it as a fallback to the Authorization header.
// A ?token= URL parameter is intentionally not supported — URL parameters appear
// in server access logs, which would leak the bearer token.
func (h *logHandler) stream(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Warn("ws upgrade failed", "err", err)
		return
	}
	defer conn.Close() //nolint:errcheck

	connID := uuid.New().String()
	ch, cancel := h.pipeline.Subscribe(connID, 256)
	defer cancel()

	var filter store.LogFilter

	// Read goroutine: accept filter updates from the client.
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				cancel()
				return
			}
			var f store.LogFilter
			if json.Unmarshal(msg, &f) == nil {
				filter = f
			}
		}
	}()

	// Write loop: forward matching entries to client.
	for entry := range ch {
		if !matchesLogFilter(entry, filter) {
			continue
		}
		data, _ := json.Marshal(entry)
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return
		}
	}
}

// parseLogParams extracts LogFilter and Pagination from query params.
// Writes a 400 and returns false on invalid input.
func parseLogParams(w http.ResponseWriter, r *http.Request) (store.LogFilter, store.Pagination, bool) {
	q := r.URL.Query()
	var filter store.LogFilter

	if s := q.Get("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid 'from' timestamp")
			return store.LogFilter{}, store.Pagination{}, false
		}
		filter.From = &t
	}
	if s := q.Get("to"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid 'to' timestamp")
			return store.LogFilter{}, store.Pagination{}, false
		}
		filter.To = &t
	}
	if filter.From != nil && filter.To != nil && filter.From.After(*filter.To) {
		writeJSONError(w, http.StatusBadRequest, "'from' must be before 'to'")
		return store.LogFilter{}, store.Pagination{}, false
	}
	if s := q.Get("user_id"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid user_id")
			return store.LogFilter{}, store.Pagination{}, false
		}
		filter.UserID = &id
	}
	if s := q.Get("server_id"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid server_id")
			return store.LogFilter{}, store.Pagination{}, false
		}
		filter.ServerID = &id
	}
	filter.ToolName = q.Get("tool_name")
	filter.MCPMethod = q.Get("method")
	filter.Status = q.Get("status")
	filter.Search = q.Get("search")

	page := store.Pagination{Page: 1, PageSize: 50}
	if s := q.Get("page"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			writeJSONError(w, http.StatusBadRequest, "invalid page")
			return store.LogFilter{}, store.Pagination{}, false
		}
		page.Page = n
	}
	if s := q.Get("page_size"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			writeJSONError(w, http.StatusBadRequest, "invalid page_size")
			return store.LogFilter{}, store.Pagination{}, false
		}
		if n > 200 {
			n = 200
		}
		page.PageSize = n
	}

	return filter, page, true
}

// matchesLogFilter returns true if entry satisfies the filter criteria.
// Used to filter the WS stream server-side after fan-out.
func matchesLogFilter(e store.AuditLogEntry, f store.LogFilter) bool {
	if f.From != nil && e.Timestamp.Before(*f.From) {
		return false
	}
	if f.To != nil && e.Timestamp.After(*f.To) {
		return false
	}
	if f.UserID != nil && e.UserID != *f.UserID {
		return false
	}
	if f.MCPMethod != "" && e.MCPMethod != f.MCPMethod {
		return false
	}
	if f.Status != "" && e.Status != f.Status {
		return false
	}
	return true
}
