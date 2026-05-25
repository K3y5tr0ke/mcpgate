//go:build integration

package e2e_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// TestE2E_SAML_JIT_ExistingUserByIDPSubject verifies that jitProvision updates
// (not re-creates) a user when the IDP subject already exists in the database.
func TestE2E_SAML_JIT_ExistingUserByIDPSubject(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	idp := ts.setupSyntheticIdP(t)

	existingID := uuid.New()
	if err := ts.userStore.Create(ctx, &store.User{
		ID:         existingID,
		Email:      "idpsubject@example.com",
		IDPSubject: "idpsubject-id",
		IDPName:    idp.cfg.Name,
		Status:     "active",
		Metadata:   map[string]any{},
	}); err != nil {
		t.Fatalf("create existing user: %v", err)
	}

	samlResp := ts.buildSAMLResponse(t, idp.idp, "idpsubject@example.com", "idpsubject-id")
	resp := ts.postACS(t, samlResp)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("ACS: want 302, got %d: %s", resp.StatusCode, body)
	}

	user, err := ts.userStore.GetByEmail(ctx, "idpsubject@example.com")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if user.ID != existingID {
		t.Errorf("JIT created new user instead of updating: got ID %s, want %s", user.ID, existingID)
	}
}

// TestE2E_SAML_JIT_ExistingUserByEmail verifies that jitProvision matches a
// user by email and populates IDPSubject when the subject was previously blank.
func TestE2E_SAML_JIT_ExistingUserByEmail(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	idp := ts.setupSyntheticIdP(t)

	existingID := uuid.New()
	if err := ts.userStore.Create(ctx, &store.User{
		ID:       existingID,
		Email:    "emailmatch@example.com",
		Status:   "active",
		Metadata: map[string]any{},
	}); err != nil {
		t.Fatalf("create existing user: %v", err)
	}

	samlResp := ts.buildSAMLResponse(t, idp.idp, "emailmatch@example.com", "new-idp-subject")
	resp := ts.postACS(t, samlResp)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("ACS: want 302, got %d: %s", resp.StatusCode, body)
	}

	user, err := ts.userStore.GetByEmail(ctx, "emailmatch@example.com")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if user.ID != existingID {
		t.Errorf("JIT created new user instead of updating: got ID %s, want %s", user.ID, existingID)
	}
	if user.IDPSubject != "new-idp-subject" {
		t.Errorf("IDPSubject not set: got %q, want %q", user.IDPSubject, "new-idp-subject")
	}
}

// TestE2E_SAML_CookieAuth_ProtectedRoute verifies that the auth middleware
// accepts the mcpgate_session cookie (browser path), not only Bearer tokens.
func TestE2E_SAML_CookieAuth_ProtectedRoute(t *testing.T) {
	ts := newTestServer(t)

	idp := ts.setupSyntheticIdP(t)
	samlResp := ts.buildSAMLResponse(t, idp.idp, "cookieuser@example.com", "cookie-subject")

	acsResp := ts.postACS(t, samlResp)
	defer acsResp.Body.Close() //nolint:errcheck

	var sessionToken string
	for _, c := range acsResp.Cookies() {
		if c.Name == "mcpgate_session" {
			sessionToken = c.Value
			break
		}
	}
	if sessionToken == "" {
		t.Fatal("no mcpgate_session cookie after ACS")
	}

	req, err := http.NewRequest(http.MethodGet, ts.url+"/api/v1/me", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "mcpgate_session", Value: sessionToken}) //nolint:gosec // G124: test cookie, Secure/HttpOnly not meaningful in httptest

	cookieResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cookie request: %v", err)
	}
	defer cookieResp.Body.Close() //nolint:errcheck

	if cookieResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(cookieResp.Body)
		t.Errorf("cookie auth: want 200, got %d: %s", cookieResp.StatusCode, body)
	}
}

// TestE2E_SAML_Metadata_Returns200 verifies that the SP metadata endpoint
// returns valid XML with the correct content type and entity ID.
func TestE2E_SAML_Metadata_Returns200(t *testing.T) {
	ts := newTestServer(t)

	resp := ts.do(t, http.MethodGet, "/auth/saml/metadata", nil, nil)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("metadata: want 200, got %d: %s", resp.StatusCode, body)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/samlmetadata+xml" {
		t.Errorf("Content-Type: got %q, want %q", ct, "application/samlmetadata+xml")
	}

	body, _ := io.ReadAll(resp.Body)
	entityID := ts.url + "/auth/saml/metadata"
	if !bytes.Contains(body, []byte(entityID)) {
		t.Errorf("metadata body does not contain entity ID %q", entityID)
	}
}

func TestE2E_SAML_NewUser_JITProvision_And_AuthenticatedRequest(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	idp := ts.setupSyntheticIdP(t)
	samlResp := ts.buildSAMLResponse(t, idp.idp, "alice@example.com", "alice-subject")

	resp := ts.postACS(t, samlResp)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("ACS: want 200 or 302, got %d: %s", resp.StatusCode, body)
	}

	// User must be JIT-provisioned in the database.
	user, err := ts.userStore.GetByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("user not created by JIT provision: %v", err)
	}
	if user.Status != "active" {
		t.Errorf("user status: got %q, want active", user.Status)
	}

	// Extract the session JWT from the response cookie.
	var sessionToken string
	for _, c := range resp.Cookies() {
		if c.Name == "mcpgate_session" {
			sessionToken = c.Value
			break
		}
	}
	if sessionToken == "" {
		t.Fatal("mcpgate_session cookie not set after ACS")
	}

	// The JWT must grant access to a protected endpoint.
	authResp := ts.do(t, http.MethodGet, "/api/v1/me", nil, map[string]string{
		"Authorization": "Bearer " + sessionToken,
	})
	defer authResp.Body.Close() //nolint:errcheck

	if authResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(authResp.Body)
		t.Errorf("authenticated request: want 200, got %d: %s", authResp.StatusCode, body)
	}
}

func TestE2E_SAML_SuspendedUser_Returns403(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	idp := ts.setupSyntheticIdP(t)

	// Pre-create the user as suspended so JIT-provision finds and rejects it.
	if err := ts.userStore.Create(ctx, &store.User{
		ID:         uuid.New(),
		Email:      "suspended@example.com",
		IDPSubject: "suspended-subject",
		IDPName:    idp.cfg.Name,
		Status:     "suspended",
		Metadata:   map[string]any{},
	}); err != nil {
		t.Fatalf("create suspended user: %v", err)
	}

	samlResp := ts.buildSAMLResponse(t, idp.idp, "suspended@example.com", "suspended-subject")
	resp := ts.postACS(t, samlResp)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("suspended user: want 403, got %d", resp.StatusCode)
	}
}

func TestE2E_SAML_ReplayDetection_Returns400(t *testing.T) {
	ts := newTestServer(t)

	idp := ts.setupSyntheticIdP(t)
	samlResp := ts.buildSAMLResponse(t, idp.idp, "replay@example.com", "replay-subject")

	// First POST — should succeed (200 or 302).
	resp1 := ts.postACS(t, samlResp)
	_ = resp1.Body.Close()

	// Second POST with the identical assertion — must be rejected as a replay.
	resp2 := ts.postACS(t, samlResp)
	defer resp2.Body.Close() //nolint:errcheck

	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("replay: want 400, got %d", resp2.StatusCode)
	}
}

func TestE2E_SAML_Logout_InvalidatesSession(t *testing.T) {
	ts := newTestServer(t)

	idp := ts.setupSyntheticIdP(t)
	samlResp := ts.buildSAMLResponse(t, idp.idp, "logout@example.com", "logout-subject")

	// Log in via ACS.
	acsResp := ts.postACS(t, samlResp)
	defer acsResp.Body.Close() //nolint:errcheck

	var sessionToken string
	for _, c := range acsResp.Cookies() {
		if c.Name == "mcpgate_session" {
			sessionToken = c.Value
			break
		}
	}
	if sessionToken == "" {
		t.Fatal("no mcpgate_session cookie after ACS")
	}

	// Verify the token works before logout.
	preResp := ts.do(t, http.MethodGet, "/api/v1/me", nil, map[string]string{
		"Authorization": "Bearer " + sessionToken,
	})
	_ = preResp.Body.Close()
	if preResp.StatusCode != http.StatusOK {
		t.Fatalf("pre-logout request: want 200, got %d", preResp.StatusCode)
	}

	// Call logout — deletes the session from Redis.
	logoutResp := ts.do(t, http.MethodGet, "/auth/saml/logout", nil, map[string]string{
		"Authorization": "Bearer " + sessionToken,
	})
	_ = logoutResp.Body.Close()

	// Replay the old token — session is gone, middleware must reject it.
	postResp := ts.do(t, http.MethodGet, "/api/v1/me", nil, map[string]string{
		"Authorization": "Bearer " + sessionToken,
	})
	defer postResp.Body.Close() //nolint:errcheck

	if postResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("post-logout request: want 401, got %d", postResp.StatusCode)
	}
}
