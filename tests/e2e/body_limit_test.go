//go:build integration

package e2e_test

import (
	"bytes"
	"net/http"
	"testing"
)

// TestE2E_AdminAPI_OversizedBody_Rejected verifies the admin body-size limit
// (limitBody middleware): a payload larger than the 1 MiB cap is rejected rather
// than buffered, so an admin-authed client can't exhaust memory with a huge JSON
// body. MaxBytesReader trips, the handler's Decode errors, and it returns 4xx.
func TestE2E_AdminAPI_OversizedBody_Rejected(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	// ~2 MiB of padding inside an otherwise-valid create-server body.
	pad := bytes.Repeat([]byte("a"), 2<<20)
	body := append([]byte(`{"name":"big","transport":"streamable_http","config":{"endpoint":"http://127.0.0.1:1","pad":"`), pad...)
	body = append(body, []byte(`"}}`)...)

	resp := ts.do(t, http.MethodPost, "/api/v1/admin/servers", bytes.NewReader(body),
		map[string]string{"Authorization": "Bearer " + adminToken, "Content-Type": "application/json"})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: want 400 or 413, got %d", resp.StatusCode)
	}
}
