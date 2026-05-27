package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/K3y5tr0ke/mcpgate/internal/auth/rbac"
)

type policyHandler struct {
	enforcer *rbac.Enforcer
	logger   *slog.Logger
}

// list returns all 'p' policy rules as [{role, server, action}].
func (h *policyHandler) list(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.enforcer.ListPolicies())
}

// add inserts a new policy rule and reloads the enforcer.
// Body: {"role":"developer","server":"my-server","action":"call"}
func (h *policyHandler) add(w http.ResponseWriter, r *http.Request) {
	var body rbac.PolicyRule
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Subject == "" || body.Resource == "" || body.Action == "" {
		writeJSONError(w, http.StatusBadRequest, "subject, resource, and action are required")
		return
	}
	if err := h.enforcer.AddPolicy(r.Context(), body.Subject, body.Resource, body.Action); err != nil {
		h.logger.Error("policies: add", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to add policy")
		return
	}
	writeJSON(w, http.StatusCreated, body)
}

// remove deletes a policy rule and reloads the enforcer.
// Body: {"role":"developer","server":"my-server","action":"call"}
func (h *policyHandler) remove(w http.ResponseWriter, r *http.Request) {
	var body rbac.PolicyRule
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Subject == "" || body.Resource == "" || body.Action == "" {
		writeJSONError(w, http.StatusBadRequest, "subject, resource, and action are required")
		return
	}
	if err := h.enforcer.RemovePolicy(r.Context(), body.Subject, body.Resource, body.Action); err != nil {
		h.logger.Error("policies: remove", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to remove policy")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
