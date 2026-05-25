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

type userHandler struct {
	store  store.UserStore
	logger *slog.Logger
}

func (h *userHandler) list(w http.ResponseWriter, r *http.Request) {
	users, _, err := h.store.List(r.Context(), store.UserFilter{}, store.Pagination{PageSize: 100})
	if err != nil {
		h.logger.Error("users: list", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	if users == nil {
		users = []store.User{}
	}
	writeJSON(w, http.StatusOK, users)
}

func (h *userHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	user, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "user not found")
			return
		}
		h.logger.Error("users: get", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to get user")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (h *userHandler) patch(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Status != "active" && body.Status != "suspended" {
		writeJSONError(w, http.StatusBadRequest, `status must be "active" or "suspended"`)
		return
	}
	if err := h.store.UpdateStatus(r.Context(), id, body.Status); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "user not found")
			return
		}
		h.logger.Error("users: patch", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to update user")
		return
	}
	user, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		h.logger.Error("users: patch get", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to fetch updated user")
		return
	}
	writeJSON(w, http.StatusOK, user)
}
