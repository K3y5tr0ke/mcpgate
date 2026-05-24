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

type idpHandler struct {
	idps   store.SAMLIDPConfigStore
	logger *slog.Logger
}

func (h *idpHandler) list(w http.ResponseWriter, r *http.Request) {
	cfgs, err := h.idps.List(r.Context())
	if err != nil {
		h.logger.Error("idp: list", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, cfgs)
}

func (h *idpHandler) create(w http.ResponseWriter, r *http.Request) {
	var cfg store.SAMLIDPConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if cfg.Name == "" || cfg.EntityID == "" || cfg.SSOURL == "" || cfg.Certificate == "" {
		writeJSONError(w, http.StatusBadRequest, "name, entity_id, sso_url and certificate are required")
		return
	}
	if cfg.ID == uuid.Nil {
		cfg.ID = uuid.New()
	}
	if cfg.AttributeMap == nil {
		cfg.AttributeMap = map[string]string{}
	}
	if cfg.GroupRoleMap == nil {
		cfg.GroupRoleMap = map[string]string{}
	}

	if err := h.idps.Create(r.Context(), &cfg); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeJSONError(w, http.StatusConflict, "IDP with this name already exists")
			return
		}
		h.logger.Error("idp: create", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, cfg)
}

func (h *idpHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid id")
		return
	}
	cfg, err := h.idps.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		h.logger.Error("idp: get", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (h *idpHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var cfg store.SAMLIDPConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cfg.ID = id

	if err := h.idps.Update(r.Context(), &cfg); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		h.logger.Error("idp: update", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (h *idpHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.idps.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		h.logger.Error("idp: delete", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
