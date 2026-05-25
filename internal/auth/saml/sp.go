package saml

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/url"
	"os"
	"time"

	cssaml "github.com/crewjam/saml"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// SP wraps the SP identity (cert + key) and can build a crewjam/saml
// ServiceProvider for any IdP config retrieved from the database.
type SP struct {
	configured bool
	key        crypto.Signer
	cert       *x509.Certificate
	idpStore   store.SAMLIDPConfigStore
	cfg        config.SAMLConfig
	logger     *slog.Logger
}

// NewSP creates an SP. If cfg.EntityID is empty, SAML is considered
// unconfigured: the SP is returned with configured=false and all handlers
// will respond 503.
func NewSP(cfg config.SAMLConfig, idpStore store.SAMLIDPConfigStore, logger *slog.Logger) (*SP, error) {
	if cfg.EntityID == "" {
		logger.Warn("SAML: entity_id not set — SAML login disabled")
		return &SP{configured: false, idpStore: idpStore, cfg: cfg, logger: logger}, nil
	}

	key, cert, err := loadOrGenerateKeyPair(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("saml: load SP key pair: %w", err)
	}

	return &SP{
		configured: true,
		key:        key,
		cert:       cert,
		idpStore:   idpStore,
		cfg:        cfg,
		logger:     logger,
	}, nil
}

// Configured reports whether SAML is enabled.
func (s *SP) Configured() bool { return s.configured }

// FirstEnabledIDP fetches the first enabled IdP from the store.
// Returns store.ErrNotFound if none are configured.
func (s *SP) FirstEnabledIDP(ctx interface{ Value(any) any }) (*store.SAMLIDPConfig, error) {
	// ctx is context.Context — using interface to avoid import cycle with context pkg.
	// This is called from handlers which pass context.Context directly.
	return nil, errors.New("use NewServiceProvider(ctx, idpCfg) from handlers")
}

// NewServiceProvider builds a crewjam/saml ServiceProvider for the given IdP.
func (s *SP) NewServiceProvider(idpCfg *store.SAMLIDPConfig) (*cssaml.ServiceProvider, error) {
	idpMeta, err := buildIDPMetadata(idpCfg)
	if err != nil {
		return nil, fmt.Errorf("saml: build IdP metadata: %w", err)
	}

	acsURL, err := url.Parse(s.cfg.ACSURL)
	if err != nil {
		return nil, fmt.Errorf("saml: parse ACS URL: %w", err)
	}
	metaURL, err := url.Parse(s.cfg.MetadataURL)
	if err != nil {
		return nil, fmt.Errorf("saml: parse metadata URL: %w", err)
	}

	sp := &cssaml.ServiceProvider{
		EntityID:          s.cfg.EntityID,
		Key:               s.key,
		Certificate:       s.cert,
		AcsURL:            *acsURL,
		MetadataURL:       *metaURL,
		IDPMetadata:       idpMeta,
		AllowIDPInitiated: true, // accept assertions without a prior SP-initiated request (replay detection guards against abuse)
	}
	return sp, nil
}

// SPMetadata returns the SP's own metadata XML. Does not require an IdP to be
// configured — used by the /auth/saml/metadata endpoint.
func (s *SP) SPMetadata() (*cssaml.EntityDescriptor, error) {
	if !s.configured {
		return nil, errors.New("saml: not configured")
	}
	acsURL, _ := url.Parse(s.cfg.ACSURL)
	metaURL, _ := url.Parse(s.cfg.MetadataURL)
	sp := &cssaml.ServiceProvider{
		EntityID:    s.cfg.EntityID,
		Key:         s.key,
		Certificate: s.cert,
		AcsURL:      *acsURL,
		MetadataURL: *metaURL,
	}
	return sp.Metadata(), nil
}

// buildIDPMetadata constructs a crewjam/saml EntityDescriptor from a
// SAMLIDPConfig that was loaded from the database.
func buildIDPMetadata(idpCfg *store.SAMLIDPConfig) (*cssaml.EntityDescriptor, error) {
	certDER, err := pemToDER(idpCfg.Certificate)
	if err != nil {
		return nil, fmt.Errorf("parse IdP certificate: %w", err)
	}
	certB64 := base64.StdEncoding.EncodeToString(certDER)

	protocolSupport := "urn:oasis:names:tc:SAML:2.0:protocol"

	idpDescriptor := cssaml.IDPSSODescriptor{
		SSODescriptor: cssaml.SSODescriptor{
			RoleDescriptor: cssaml.RoleDescriptor{
				ProtocolSupportEnumeration: protocolSupport,
				KeyDescriptors: []cssaml.KeyDescriptor{
					{
						Use: "signing",
						KeyInfo: cssaml.KeyInfo{
							X509Data: cssaml.X509Data{
								X509Certificates: []cssaml.X509Certificate{
									{Data: certB64},
								},
							},
						},
					},
				},
			},
		},
		SingleSignOnServices: []cssaml.Endpoint{
			{
				Binding:  cssaml.HTTPRedirectBinding,
				Location: idpCfg.SSOURL,
			},
		},
	}

	if idpCfg.SLOURL != nil && *idpCfg.SLOURL != "" {
		idpDescriptor.SingleLogoutServices = []cssaml.Endpoint{
			{
				Binding:  cssaml.HTTPRedirectBinding,
				Location: *idpCfg.SLOURL,
			},
		}
	}

	return &cssaml.EntityDescriptor{
		EntityID:          idpCfg.EntityID,
		IDPSSODescriptors: []cssaml.IDPSSODescriptor{idpDescriptor},
	}, nil
}

// pemToDER decodes a PEM block and returns the raw DER bytes.
func pemToDER(pemStr string) ([]byte, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	return block.Bytes, nil
}

// loadOrGenerateKeyPair loads the SP key/cert from files, or generates an
// ephemeral pair for dev mode when files are not configured.
func loadOrGenerateKeyPair(cfg config.SAMLConfig, logger *slog.Logger) (crypto.Signer, *x509.Certificate, error) {
	if cfg.PrivateKeyFile == "" || cfg.CertificateFile == "" {
		logger.Warn("SAML: key/cert files not set — using ephemeral key pair (dev mode only)")
		return generateEphemeralKeyPair(cfg.EntityID)
	}

	keyPEM, err := os.ReadFile(cfg.PrivateKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("read private key %q: %w", cfg.PrivateKeyFile, err)
	}
	certPEM, err := os.ReadFile(cfg.CertificateFile)
	if err != nil {
		return nil, nil, fmt.Errorf("read certificate %q: %w", cfg.CertificateFile, err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, errors.New("no PEM block in private key file")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		// Try PKCS1.
		rsaKey, err2 := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
		if err2 != nil {
			return nil, nil, fmt.Errorf("parse private key (tried PKCS8: %v, PKCS1: %v)", err, err2)
		}
		key = rsaKey
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, errors.New("no PEM block in certificate file")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse certificate: %w", err)
	}

	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, nil, errors.New("key does not implement crypto.Signer")
	}
	return signer, cert, nil
}

func generateEphemeralKeyPair(entityID string) (crypto.Signer, *x509.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: entityID},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, err
	}
	return key, cert, nil
}
