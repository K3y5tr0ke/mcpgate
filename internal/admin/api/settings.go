package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// allowedSettingsKeys is the exhaustive set of keys the settings API accepts.
// Unknown keys are rejected to prevent arbitrary data injection.
var allowedSettingsKeys = map[string]bool{
	"session_ttl_seconds":               true,
	"rate_limit_default_rpm":            true,
	"rate_limit_default_rpd":            true,
	"log_retention_hot_days":            true,
	"proxy_default_timeout_seconds":     true,
	"circuit_breaker_failure_threshold": true,
	"circuit_breaker_recovery_seconds":  true,
}

type settingsHandler struct {
	store  store.SettingsStore
	logger *slog.Logger
}

func (h *settingsHandler) get(w http.ResponseWriter, r *http.Request) {
	settings, err := h.store.GetAll(r.Context())
	if err != nil {
		h.logger.Error("settings: get all", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *settingsHandler) put(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	for key := range body {
		if !allowedSettingsKeys[key] {
			writeJSONError(w, http.StatusBadRequest, "unknown setting key: "+key)
			return
		}
	}

	for key, value := range body {
		if err := h.store.Set(r.Context(), key, value); err != nil {
			h.logger.Error("settings: set", "key", key, "err", err)
			writeJSONError(w, http.StatusInternalServerError, "failed to update setting: "+key)
			return
		}
	}

	settings, err := h.store.GetAll(r.Context())
	if err != nil {
		h.logger.Error("settings: get after update", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "settings updated but failed to reload")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
