package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

type serverHandler struct {
	servers store.ServerStore
	logger  *slog.Logger
}

// createServerRequest is the JSON body for POST /servers.
type createServerRequest struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Transport   string         `json:"transport"`
	Config      map[string]any `json:"config"`
	HealthCheck map[string]any `json:"health_check"`
	IsEnabled   bool           `json:"is_enabled"`
}

// updateServerRequest is the JSON body for PUT /servers/{id}.
type updateServerRequest struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Transport   string         `json:"transport"`
	Config      map[string]any `json:"config"`
	HealthCheck map[string]any `json:"health_check"`
	IsEnabled   bool           `json:"is_enabled"`
}

func (h *serverHandler) list(w http.ResponseWriter, r *http.Request) {
	servers, err := h.servers.List(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "list servers failed")
		h.logger.Error("admin: list servers", "err", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": servers, "total": len(servers)})
}

func (h *serverHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Transport == "" {
		writeJSONError(w, http.StatusBadRequest, "name and transport are required")
		return
	}

	srv := &store.MCPServer{
		Name:        req.Name,
		Description: req.Description,
		Transport:   req.Transport,
		Config:      req.Config,
		HealthCheck: req.HealthCheck,
		IsEnabled:   req.IsEnabled,
	}
	if err := h.servers.Create(r.Context(), srv); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeJSONError(w, http.StatusConflict, "server name already exists")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "create server failed")
		h.logger.Error("admin: create server", "err", err)
		return
	}
	writeJSON(w, http.StatusCreated, srv)
}

func (h *serverHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	srv, err := h.servers.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "server not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "get server failed")
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

func (h *serverHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid server id")
		return
	}

	var req updateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	srv := &store.MCPServer{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Transport:   req.Transport,
		Config:      req.Config,
		HealthCheck: req.HealthCheck,
		IsEnabled:   req.IsEnabled,
	}
	if err := h.servers.Update(r.Context(), srv); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "server not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "update server failed")
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

func (h *serverHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	if err := h.servers.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "server not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "delete server failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
