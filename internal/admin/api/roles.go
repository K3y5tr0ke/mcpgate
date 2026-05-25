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

type roleHandler struct {
	store  store.RoleStore
	logger *slog.Logger
}

func (h *roleHandler) list(w http.ResponseWriter, r *http.Request) {
	roles, err := h.store.List(r.Context())
	if err != nil {
		h.logger.Error("roles: list", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to list roles")
		return
	}
	if roles == nil {
		roles = []store.Role{}
	}
	writeJSON(w, http.StatusOK, roles)
}

func (h *roleHandler) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required")
		return
	}
	role := &store.Role{Name: body.Name, Description: body.Description}
	if err := h.store.Create(r.Context(), role); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeJSONError(w, http.StatusConflict, "role with this name already exists")
			return
		}
		h.logger.Error("roles: create", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to create role")
		return
	}
	writeJSON(w, http.StatusCreated, role)
}

func (h *roleHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid role ID")
		return
	}
	role, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "role not found")
			return
		}
		h.logger.Error("roles: get", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to get role")
		return
	}
	writeJSON(w, http.StatusOK, role)
}

func (h *roleHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid role ID")
		return
	}
	existing, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "role not found")
			return
		}
		h.logger.Error("roles: update get", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to get role")
		return
	}

	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// System role names are immutable; only description may be changed.
	if existing.IsSystem {
		existing.Description = body.Description
	} else {
		if body.Name != "" {
			existing.Name = body.Name
		}
		existing.Description = body.Description
	}

	if err := h.store.Update(r.Context(), existing); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeJSONError(w, http.StatusConflict, "role with this name already exists")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "role not found")
			return
		}
		h.logger.Error("roles: update", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to update role")
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

func (h *roleHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid role ID")
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "role not found")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeJSONError(w, http.StatusConflict, "system roles cannot be deleted")
			return
		}
		h.logger.Error("roles: delete", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to delete role")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *roleHandler) assign(w http.ResponseWriter, r *http.Request) {
	roleID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid role ID")
		return
	}
	var body struct {
		UserID     string `json:"user_id"`
		AssignedBy string `json:"assigned_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	userID, err := uuid.Parse(body.UserID)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user_id")
		return
	}
	// assigned_by is optional; default to nil UUID (system assignment).
	assignedBy := uuid.Nil
	if body.AssignedBy != "" {
		if assignedBy, err = uuid.Parse(body.AssignedBy); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid assigned_by")
			return
		}
	}
	if err := h.store.AssignToUser(r.Context(), userID, roleID, assignedBy); err != nil {
		h.logger.Error("roles: assign", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to assign role")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *roleHandler) revoke(w http.ResponseWriter, r *http.Request) {
	roleID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid role ID")
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	if err := h.store.RevokeFromUser(r.Context(), userID, roleID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "assignment not found")
			return
		}
		h.logger.Error("roles: revoke", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to revoke role")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
