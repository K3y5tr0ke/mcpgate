package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	jwkspkg "github.com/K3y5tr0ke/mcpgate/internal/auth/jwks"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

type jwksHandler struct {
	store     store.JWKSConfigStore
	validator *jwkspkg.Validator
	logger    *slog.Logger
}

func (h *jwksHandler) list(w http.ResponseWriter, r *http.Request) {
	cfgs, err := h.store.List(r.Context())
	if err != nil {
		h.logger.Error("jwks: list", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if cfgs == nil {
		cfgs = []store.JWKSConfig{}
	}
	writeJSON(w, http.StatusOK, cfgs)
}

func (h *jwksHandler) create(w http.ResponseWriter, r *http.Request) {
	var cfg store.JWKSConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if cfg.Name == "" || cfg.Issuer == "" || cfg.JWKSURL == "" {
		writeJSONError(w, http.StatusBadRequest, "name, issuer and jwks_url are required")
		return
	}
	if cfg.ID == uuid.Nil {
		cfg.ID = uuid.New()
	}

	if err := h.store.Create(r.Context(), &cfg); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeJSONError(w, http.StatusConflict, "JWKS config with this name or issuer already exists")
			return
		}
		h.logger.Error("jwks: create", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, cfg)
}

func (h *jwksHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid id")
		return
	}
	cfg, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		h.logger.Error("jwks: get", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (h *jwksHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var cfg store.JWKSConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cfg.ID = id

	if err := h.store.Update(r.Context(), &cfg); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		if errors.Is(err, store.ErrAlreadyExists) {
			writeJSONError(w, http.StatusConflict, "JWKS config with this name or issuer already exists")
			return
		}
		h.logger.Error("jwks: update", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (h *jwksHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		h.logger.Error("jwks: delete", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rotate forces a re-fetch of an issuer's JWKS by evicting the in-memory client
// cache. It first verifies the remote JWKS URL is reachable and returns a
// non-empty key set (502 otherwise), then invalidates the cached client so the
// next token validation lazily re-fetches the fresh keys. The fetched keys are
// not stored here — invalidation alone is the contract.
func (h *jwksHandler) rotate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid id")
		return
	}

	cfg, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		h.logger.Error("jwks: rotate get", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	keyCount, err := fetchJWKSKeyCount(r.Context(), cfg.JWKSURL)
	if err != nil {
		h.logger.Warn("jwks: rotate fetch failed", "issuer", cfg.Issuer, "err", err)
		writeJSONError(w, http.StatusBadGateway, "could not fetch remote JWKS: "+err.Error())
		return
	}

	if h.validator != nil {
		h.validator.Invalidate(cfg.Issuer)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":     cfg.Issuer,
		"key_count":  keyCount,
		"rotated_at": time.Now().UTC(),
	})
}

// jwksRotateClient bounds the rotate endpoint's outbound fetch and refuses to
// follow redirects (a JWKS URL must not 3xx-pivot to an internal address — SSRF).
var jwksRotateClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// fetchJWKSKeyCount GETs a JWKS URL and returns the number of keys it advertises.
// Returns an error if the endpoint is unreachable, returns a non-200, is not valid
// JSON, or contains an empty "keys" array.
func fetchJWKSKeyCount(ctx context.Context, jwksURL string) (int, error) {
	// jwksURL is an admin-configured value from jwks_configs — the same URL the
	// JWKS validator fetches on every M2M token validation (auth/jwks/client.go).
	// Rotation introduces no new fetch surface, so the gosec SSRF taint warning
	// is a known false positive here.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, http.NoBody) //nolint:gosec // admin-configured JWKS URL, same as validator fetch
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	resp, err := jwksRotateClient.Do(req) //nolint:gosec // admin-configured JWKS URL, same as validator fetch
	if err != nil {
		return 0, fmt.Errorf("unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}

	var doc struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return 0, fmt.Errorf("invalid JSON: %w", err)
	}
	if len(doc.Keys) == 0 {
		return 0, errors.New("response contained no keys")
	}
	return len(doc.Keys), nil
}
