package jwks

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// buildJWKS creates a minimal JWKS JSON response for the given RSA key and kid.
func buildJWKS(t *testing.T, kid string, pub *rsa.PublicKey) []byte { //nolint:unparam
	t.Helper()
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	resp := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"kid": kid,
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(eBytes),
			},
		},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestClient_GetKey_CacheHit(t *testing.T) {
	key := newRSAKey(t)
	fetchCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount++
		w.Write(buildJWKS(t, "k1", &key.PublicKey)) //nolint:errcheck,gosec
	}))
	defer srv.Close()

	c := NewClient(srv.URL, time.Hour)

	// First call fetches.
	if _, err := c.GetKey(context.Background(), "k1"); err != nil {
		t.Fatalf("first GetKey: %v", err)
	}
	// Second call must hit cache.
	if _, err := c.GetKey(context.Background(), "k1"); err != nil {
		t.Fatalf("second GetKey: %v", err)
	}
	if fetchCount != 1 {
		t.Errorf("fetchCount: got %d, want 1", fetchCount)
	}
}

func TestClient_GetKey_UnknownKid_ForcesRefresh(t *testing.T) {
	key := newRSAKey(t)
	fetchCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount++
		w.Write(buildJWKS(t, "k1", &key.PublicKey)) //nolint:errcheck,gosec
	}))
	defer srv.Close()

	c := NewClient(srv.URL, time.Hour)

	// Prime the cache with k1.
	if _, err := c.GetKey(context.Background(), "k1"); err != nil {
		t.Fatal(err)
	}

	// Requesting unknown kid k2 forces a re-fetch and still fails.
	_, err := c.GetKey(context.Background(), "k2")
	if err == nil {
		t.Fatal("expected error for unknown kid k2")
	}
	if fetchCount != 2 {
		t.Errorf("fetchCount: got %d, want 2", fetchCount)
	}
}

func TestClient_GetKey_UnknownKid_AfterRefresh_Error(t *testing.T) {
	key := newRSAKey(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(buildJWKS(t, "k1", &key.PublicKey)) //nolint:errcheck,gosec
	}))
	defer srv.Close()

	c := NewClient(srv.URL, time.Hour)
	_, err := c.GetKey(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for key not in JWKS")
	}
}

func TestClient_GetKey_ExpiredCache_Refetches(t *testing.T) {
	key := newRSAKey(t)
	fetchCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount++
		w.Write(buildJWKS(t, "k1", &key.PublicKey)) //nolint:errcheck,gosec
	}))
	defer srv.Close()

	// Very short TTL so it expires immediately.
	c := NewClient(srv.URL, time.Nanosecond)

	if _, err := c.GetKey(context.Background(), "k1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err := c.GetKey(context.Background(), "k1"); err != nil {
		t.Fatal(err)
	}
	if fetchCount < 2 {
		t.Errorf("fetchCount: got %d, want ≥2", fetchCount)
	}
}

func TestClient_GetKey_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, time.Hour)
	_, err := c.GetKey(context.Background(), "k1")
	if err == nil {
		t.Fatal("expected error on server 500")
	}
}

func TestClient_GetKey_MalformedJWKS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not-json")) //nolint:errcheck,gosec
	}))
	defer srv.Close()

	c := NewClient(srv.URL, time.Hour)
	_, err := c.GetKey(context.Background(), "k1")
	if err == nil {
		t.Fatal("expected error on malformed JWKS")
	}
}

func TestClient_GetKey_ConcurrentAccess(t *testing.T) {
	key := newRSAKey(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(buildJWKS(t, "k1", &key.PublicKey)) //nolint:errcheck,gosec
	}))
	defer srv.Close()

	c := NewClient(srv.URL, time.Hour)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.GetKey(context.Background(), "k1"); err != nil {
				t.Errorf("concurrent GetKey: %v", err)
			}
		}()
	}
	wg.Wait()
}
