package jwks

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// Client fetches and caches RSA public keys from a JWKS endpoint.
// One Client is created per JWKSConfig; the Validator owns the map.
type Client struct {
	url        string
	httpClient *http.Client
	ttl        time.Duration

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey // kid → key
	fetchedAt time.Time
}

// NewClient creates a Client for the given JWKS URL.
func NewClient(url string, ttl time.Duration) *Client {
	return &Client{
		url: url,
		ttl: ttl,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		keys: make(map[string]*rsa.PublicKey),
	}
}

// GetKey returns the RSA public key for kid.
// Cache is refreshed when stale or when kid is absent after a fresh fetch.
func (c *Client) GetKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	// Fast path: cache is fresh and kid exists.
	c.mu.RLock()
	if c.cacheValid() {
		if key, ok := c.keys[kid]; ok {
			c.mu.RUnlock()
			return key, nil
		}
	}
	c.mu.RUnlock()

	// Slow path: acquire write lock, re-check, then fetch.
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cacheValid() {
		if key, ok := c.keys[kid]; ok {
			return key, nil
		}
	}

	if err := c.fetch(ctx); err != nil {
		return nil, fmt.Errorf("jwks fetch %q: %w", c.url, err)
	}

	key, ok := c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("jwks: key %q not found at %s", kid, c.url)
	}
	return key, nil
}

func (c *Client) cacheValid() bool {
	return !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < c.ttl
}

// fetch unconditionally GETs the JWKS URL and replaces the key cache.
// Must be called with the write lock held.
func (c *Client) fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	var jwksResp jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwksResp); err != nil {
		return fmt.Errorf("decode JWKS: %w", err)
	}

	newKeys := make(map[string]*rsa.PublicKey, len(jwksResp.Keys))
	for _, k := range jwksResp.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := parseRSAKey(k)
		if err != nil {
			return fmt.Errorf("parse JWK kid=%q: %w", k.Kid, err)
		}
		newKeys[k.Kid] = pub
	}

	c.keys = newKeys
	c.fetchedAt = time.Now()
	return nil
}

// --- JSON types ---

type jwksResponse struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func parseRSAKey(k jwk) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode e: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)

	if !e.IsInt64() {
		return nil, fmt.Errorf("exponent too large")
	}

	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}
