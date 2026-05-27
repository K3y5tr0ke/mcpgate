package local

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	jwtpkg "github.com/K3y5tr0ke/mcpgate/internal/auth/jwt"
	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// Handlers holds all local-login HTTP handler dependencies.
type Handlers struct {
	userStore    store.UserStore
	sessionStore store.SessionStore
	sessionCache store.SessionCache
	roleStore    store.RoleStore
	jwtManager   *jwtpkg.Manager
	redis        *redis.Client
	authCfg      config.AuthConfig
	logger       *slog.Logger
}

// NewHandlers wires all dependencies for the local auth handler set.
func NewHandlers(
	userStore store.UserStore,
	sessionStore store.SessionStore,
	sessionCache store.SessionCache,
	roleStore store.RoleStore,
	jwtManager *jwtpkg.Manager,
	redisClient *redis.Client,
	authCfg config.AuthConfig,
	logger *slog.Logger,
) *Handlers {
	return &Handlers{
		userStore:    userStore,
		sessionStore: sessionStore,
		sessionCache: sessionCache,
		roleStore:    roleStore,
		jwtManager:   jwtManager,
		redis:        redisClient,
		authCfg:      authCfg,
		logger:       logger,
	}
}

// Login handles POST /auth/local/login.
// Body: {"email": "...", "password": "..."}
// On success: sets mcpgate_session cookie and returns {"token": "<jwt>"}.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	clientIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		clientIP = host
	}

	if h.bruteForceCheck(r.Context(), clientIP) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" || body.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "email and password required")
		return
	}

	// Generic message for all auth failures — prevents user enumeration.
	const errMsg = "invalid credentials"

	user, err := h.userStore.GetByEmail(r.Context(), body.Email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusUnauthorized, errMsg)
			return
		}
		h.logger.Error("local login: get user", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Only local accounts (idp_name == "local") can use this endpoint.
	if user.IDPName != "local" || user.PasswordHash == nil {
		writeJSONError(w, http.StatusUnauthorized, errMsg)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(body.Password)); err != nil {
		writeJSONError(w, http.StatusUnauthorized, errMsg)
		return
	}

	if user.Status != "active" {
		writeJSONError(w, http.StatusForbidden, "account suspended")
		return
	}

	roles, err := h.roleStore.GetUserRoles(r.Context(), user.ID)
	if err != nil {
		h.logger.Error("local login: get roles", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	roleNames := make([]string, len(roles))
	for i, ro := range roles {
		roleNames[i] = ro.Name
	}

	h.bruteForceReset(r.Context(), clientIP)

	sessionID := uuid.New()
	ttl := h.authCfg.SessionTTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	session := &store.Session{
		ID:        sessionID,
		UserID:    user.ID,
		ClientIP:  clientIP,
		UserAgent: r.UserAgent(),
		ExpiresAt: time.Now().Add(ttl),
		CreatedAt: time.Now(),
	}
	if err := h.sessionStore.Create(r.Context(), session); err != nil {
		h.logger.Error("local login: create session", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	sessionData := store.SessionData{
		UserID:    user.ID,
		Email:     user.Email,
		Roles:     roleNames,
		SessionID: sessionID,
	}
	if err := h.sessionCache.Set(r.Context(), sessionID, sessionData, ttl); err != nil {
		h.logger.Warn("local login: cache session", "err", err)
	}

	tokenStr, err := h.jwtManager.Issue(user.ID, user.Email, roleNames, sessionID)
	if err != nil {
		h.logger.Error("local login: issue JWT", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "mcpgate_session",
		Value:    tokenStr,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	})

	writeJSON(w, http.StatusOK, map[string]string{"token": tokenStr})
}

// bruteForceCheck returns true if the IP has exceeded the attempt limit.
func (h *Handlers) bruteForceCheck(ctx context.Context, ip string) bool {
	if !h.authCfg.BruteForce.Enabled {
		return false
	}
	key := "brute:local_login:" + ip
	n, err := h.redis.Incr(ctx, key).Result()
	if err != nil {
		return false
	}
	if n == 1 {
		_ = h.redis.Expire(ctx, key, h.authCfg.BruteForce.Window).Err()
	}
	if n > int64(h.authCfg.BruteForce.MaxAttempts) {
		_ = h.redis.Expire(ctx, key, h.authCfg.BruteForce.BlockFor).Err()
		return true
	}
	return false
}

func (h *Handlers) bruteForceReset(ctx context.Context, ip string) {
	_ = h.redis.Del(ctx, "brute:local_login:"+ip).Err()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
