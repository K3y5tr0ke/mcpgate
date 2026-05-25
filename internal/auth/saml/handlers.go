package saml

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	cssaml "github.com/crewjam/saml"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	jwtpkg "github.com/K3y5tr0ke/mcpgate/internal/auth/jwt"
	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// Handlers holds all SAML HTTP handler dependencies.
type Handlers struct {
	sp           *SP
	userStore    store.UserStore
	sessionStore store.SessionStore
	sessionCache store.SessionCache
	roleStore    store.RoleStore
	idpStore     store.SAMLIDPConfigStore
	jwtManager   *jwtpkg.Manager
	redisClient  *redis.Client
	guard        *BruteForceGuard
	cfg          config.SAMLConfig
	authCfg      config.AuthConfig
	logger       *slog.Logger
}

// NewHandlers wires all dependencies for the SAML handler set.
func NewHandlers(
	sp *SP,
	userStore store.UserStore,
	sessionStore store.SessionStore,
	sessionCache store.SessionCache,
	roleStore store.RoleStore,
	idpStore store.SAMLIDPConfigStore,
	jwtManager *jwtpkg.Manager,
	redisClient *redis.Client,
	cfg config.SAMLConfig,
	authCfg config.AuthConfig,
	logger *slog.Logger,
) *Handlers {
	return &Handlers{
		sp:           sp,
		userStore:    userStore,
		sessionStore: sessionStore,
		sessionCache: sessionCache,
		roleStore:    roleStore,
		idpStore:     idpStore,
		jwtManager:   jwtManager,
		redisClient:  redisClient,
		guard:        NewBruteForceGuard(redisClient, authCfg.BruteForce),
		cfg:          cfg,
		authCfg:      authCfg,
		logger:       logger,
	}
}

// Login initiates SP-initiated SAML login via HTTP-Redirect binding.
// GET /auth/saml/login
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if !h.sp.Configured() {
		http.Error(w, "SAML not configured", http.StatusServiceUnavailable)
		return
	}

	clientIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		clientIP = host
	}
	if h.guard.Check(r.Context(), "brute:saml_login:"+clientIP) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	idpCfg, err := h.firstEnabledIDP(r.Context())
	if err != nil {
		h.logger.Error("saml login: no IdP configured", "err", err)
		http.Error(w, "no IdP configured", http.StatusServiceUnavailable)
		return
	}

	sp, err := h.sp.NewServiceProvider(idpCfg)
	if err != nil {
		h.logger.Error("saml login: build SP", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	ssoURL := idpCfg.SSOURL
	authReq, err := sp.MakeAuthenticationRequest(ssoURL, cssaml.HTTPRedirectBinding, cssaml.HTTPPostBinding)
	if err != nil {
		h.logger.Error("saml login: make authn request", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Embed the AuthnRequest ID in RelayState as "<id>|<redirect_to>" so ACS can
	// validate InResponseTo without a separate cookie (avoids SameSite=None requirement).
	redirectTo := r.URL.Query().Get("redirect_to")
	if redirectTo == "" {
		redirectTo = "/"
	}
	relayState := authReq.ID + "|" + redirectTo

	redirectURL, err := authReq.Redirect(relayState, sp)
	if err != nil {
		h.logger.Error("saml login: build redirect URL", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, redirectURL.String(), http.StatusFound) //nolint:gosec // G710: IdP SSO URL from admin-configured metadata, not user input
}

// ACS handles the SAML assertion consumer service (IdP POST response).
// POST /auth/saml/acs
func (h *Handlers) ACS(w http.ResponseWriter, r *http.Request) {
	if !h.sp.Configured() {
		http.Error(w, "SAML not configured", http.StatusServiceUnavailable)
		return
	}

	clientIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		clientIP = host
	}
	if h.guard.Check(r.Context(), "brute:saml_acs:"+clientIP) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	idpCfg, err := h.firstEnabledIDP(r.Context())
	if err != nil {
		h.logger.Error("saml acs: no IdP configured", "err", err)
		http.Error(w, "no IdP configured", http.StatusServiceUnavailable)
		return
	}

	sp, err := h.sp.NewServiceProvider(idpCfg)
	if err != nil {
		h.logger.Error("saml acs: build SP", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// ParseForm must be called before ParseResponse — crewjam/saml reads req.PostForm directly.
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Parse RelayState "<requestID>|<redirectTo>" to recover the AuthnRequest ID for
	// InResponseTo validation. Falls back to IdP-initiated flow if format not present.
	var possibleRequestIDs []string
	redirectTo := "/"
	if rs := r.FormValue("RelayState"); rs != "" {
		if idx := strings.Index(rs, "|"); idx >= 0 {
			possibleRequestIDs = []string{rs[:idx]}
			if dest := rs[idx+1:]; strings.HasPrefix(dest, "/") {
				redirectTo = dest
			}
		}
	}

	assertion, err := sp.ParseResponse(r, possibleRequestIDs)
	if err != nil {
		var invalidErr *cssaml.InvalidResponseError
		if errors.As(err, &invalidErr) && invalidErr.PrivateErr != nil {
			h.logger.Warn("saml acs: invalid assertion", "reason", invalidErr.PrivateErr)
		} else {
			h.logger.Warn("saml acs: invalid assertion", "err", err)
		}
		http.Error(w, "invalid SAML response", http.StatusBadRequest)
		return
	}

	// Replay detection.
	if err := h.checkReplay(r.Context(), assertion); err != nil {
		h.logger.Warn("saml acs: replay detected", "assertion_id", assertion.ID)
		http.Error(w, "assertion already consumed", http.StatusBadRequest)
		return
	}

	// Extract attributes.
	attrs := Extract(assertion, idpCfg.AttributeMap)
	if attrs.Email == "" {
		http.Error(w, "no email in assertion", http.StatusBadRequest)
		return
	}

	nameID := ""
	if assertion.Subject != nil && assertion.Subject.NameID != nil {
		nameID = assertion.Subject.NameID.Value
	}

	// JIT provision.
	user, err := h.jitProvision(r.Context(), idpCfg.Name, nameID, attrs)
	if err != nil {
		h.logger.Error("saml acs: JIT provision", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if user.Status != "active" {
		http.Error(w, "account suspended", http.StatusForbidden)
		return
	}

	// Fetch roles.
	roles, err := h.roleStore.GetUserRoles(r.Context(), user.ID)
	if err != nil {
		h.logger.Error("saml acs: get user roles", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	roleNames := make([]string, len(roles))
	for i, ro := range roles {
		roleNames[i] = ro.Name
	}

	// Reset brute-force counters on successful authentication.
	h.guard.Reset(r.Context(), "brute:saml_login:"+clientIP)
	h.guard.Reset(r.Context(), "brute:saml_acs:"+clientIP)

	// sessionID is always newly generated here — no pre-auth session exists to fix.
	// Create durable session.
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
		h.logger.Error("saml acs: create session", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Cache session in Redis.
	sessionData := store.SessionData{
		UserID:    user.ID,
		Email:     user.Email,
		Roles:     roleNames,
		SessionID: sessionID,
	}
	if err := h.sessionCache.Set(r.Context(), sessionID, sessionData, ttl); err != nil {
		h.logger.Error("saml acs: cache session", "err", err)
	}

	// Issue JWT.
	tokenStr, err := h.jwtManager.Issue(user.ID, user.Email, roleNames, sessionID)
	if err != nil {
		h.logger.Error("saml acs: issue JWT", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
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

	http.Redirect(w, r, redirectTo, http.StatusFound) //nolint:gosec // G710: redirectTo validated as relative path above
}

// Metadata serves the SP metadata XML.
// GET /auth/saml/metadata
func (h *Handlers) Metadata(w http.ResponseWriter, r *http.Request) {
	if !h.sp.Configured() {
		http.Error(w, "SAML not configured", http.StatusServiceUnavailable)
		return
	}

	meta, err := h.sp.SPMetadata()
	if err != nil {
		h.logger.Error("saml metadata", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	out, err := xml.MarshalIndent(meta, "", "  ")
	if err != nil {
		h.logger.Error("saml metadata: marshal", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/samlmetadata+xml")
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(out)
}

// Logout invalidates the current session and clears the session cookie.
// Accepts the token from the mcpgate_session cookie (browser) or an
// Authorization: Bearer header (API / programmatic clients).
// GET /auth/saml/logout
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	tokenStr := ""
	if cookie, err := r.Cookie("mcpgate_session"); err == nil {
		tokenStr = cookie.Value
	} else if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		tokenStr = strings.TrimPrefix(auth, "Bearer ")
	}

	if tokenStr != "" {
		// Parse JWT ignoring expiry — we want to clean up even expired tokens.
		if claims, err := h.jwtManager.Validate(tokenStr); err == nil {
			if sessionID, parseErr := uuid.Parse(claims.SessionID); parseErr == nil {
				if delErr := h.sessionCache.Delete(r.Context(), sessionID); delErr != nil {
					h.logger.Warn("logout: delete session cache", "err", delErr)
				}
				if delErr := h.sessionStore.Delete(r.Context(), sessionID); delErr != nil {
					h.logger.Warn("logout: delete session store", "err", delErr)
				}
			}
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "mcpgate_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/", http.StatusFound)
}

// --- helpers ---

func (h *Handlers) firstEnabledIDP(ctx context.Context) (*store.SAMLIDPConfig, error) {
	idps, err := h.idpStore.ListEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if len(idps) == 0 {
		return nil, store.ErrNotFound
	}
	return &idps[0], nil
}

// checkReplay records the assertion ID in Redis with NX (set if not exists).
// Returns an error if the assertion was already seen.
func (h *Handlers) checkReplay(ctx context.Context, assertion *cssaml.Assertion) error {
	if assertion.ID == "" {
		return nil
	}

	ttl := time.Minute * 5 // default safety window
	if assertion.Conditions != nil && !assertion.Conditions.NotOnOrAfter.IsZero() {
		remaining := time.Until(assertion.Conditions.NotOnOrAfter) + 5*time.Minute
		if remaining > ttl {
			ttl = remaining
		}
	}

	key := "saml:replay:" + assertion.ID
	set, err := h.redisClient.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		// On Redis failure, log and allow (availability over strict replay prevention).
		h.logger.Error("saml replay check: redis error", "err", err)
		return nil
	}
	if !set {
		return fmt.Errorf("assertion %q already consumed", assertion.ID)
	}
	return nil
}

// jitProvision looks up or creates a user based on the SAML assertion.
func (h *Handlers) jitProvision(ctx context.Context, idpName, nameID string, attrs MappedAttributes) (*store.User, error) {
	// Try by IdP subject (nameID).
	if nameID != "" {
		user, err := h.userStore.GetByIDPSubject(ctx, idpName, nameID)
		if err == nil {
			user.DisplayName = attrs.DisplayName
			user.IDPName = idpName
			user.IDPSubject = nameID
			if updateErr := h.userStore.Update(ctx, user); updateErr != nil {
				h.logger.Warn("jit: update user", "user_id", user.ID, "err", updateErr)
			}
			return user, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}

	// Try by email.
	user, err := h.userStore.GetByEmail(ctx, attrs.Email)
	if err == nil {
		user.DisplayName = attrs.DisplayName
		user.IDPSubject = nameID
		user.IDPName = idpName
		if updateErr := h.userStore.Update(ctx, user); updateErr != nil {
			h.logger.Warn("jit: update user email match", "user_id", user.ID, "err", updateErr)
		}
		return user, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	// Create new user.
	newUser := &store.User{
		ID:          uuid.New(),
		Email:       attrs.Email,
		DisplayName: attrs.DisplayName,
		IDPSubject:  nameID,
		IDPName:     idpName,
		Status:      "active",
		Metadata:    map[string]any{},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := h.userStore.Create(ctx, newUser); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return newUser, nil
}
