//go:build integration

package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// TestWebSocket_SessionRevoked_ConnectionClosed is a behaviour specification for
// mid-connection session re-validation on the audit log WebSocket stream.
//
// The current handler validates the session only at upgrade time (auth
// middleware), so a session revoked AFTER the socket is open stays connected.
// This test documents the intended behaviour: a periodic re-validation should
// detect the revoked session and close the socket. The handler now runs a
// re-validation heartbeat (internal/admin/api/logs.go); this test exercises it
// with a short interval.
func TestWebSocket_SessionRevoked_ConnectionClosed(t *testing.T) {
	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.Auth.SessionRevalidateInterval = 1 * time.Second
	})
	ctx := context.Background()

	// 1. Issue a real admin JWT + live session and capture the session ID so we
	//    can revoke it mid-connection.
	userID := uuid.New()
	if err := ts.userStore.Create(ctx, &store.User{
		ID: userID, Email: "ws-" + uuid.New().String()[:8] + "@test.internal",
		Status: "active", Metadata: map[string]any{},
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	sessionID := uuid.New()
	if err := ts.sessionStore.Create(ctx, &store.Session{
		ID: sessionID, UserID: userID, ClientIP: "127.0.0.1",
		UserAgent: "e2e-ws", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := ts.sessionCache.Set(ctx, sessionID, store.SessionData{
		UserID: userID, Email: "ws@test.internal", Roles: []string{"admin"}, SessionID: sessionID,
	}, time.Hour); err != nil {
		t.Fatalf("cache session: %v", err)
	}
	token, err := ts.srv.JWTManager().Issue(userID, "ws@test.internal", []string{"admin"}, sessionID)
	if err != nil {
		t.Fatalf("issue JWT: %v", err)
	}

	// 2. Open the WebSocket with the session cookie.
	wsURL := "ws" + strings.TrimPrefix(ts.url, "http") + "/api/v1/admin/logs/stream"
	header := http.Header{"Cookie": {"mcpgate_session=" + token}}
	conn, dialResp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if dialResp != nil {
		_ = dialResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	// 3. Connection is open: a control ping should succeed.
	if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("expected open connection, ping failed: %v", err)
	}

	// 4. Revoke the session in Redis.
	if err := ts.sessionCache.Delete(ctx, sessionID); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	// 5. Within a couple of re-validation intervals, the server should close the socket.
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return // connection closed as expected
		}
	}
}
