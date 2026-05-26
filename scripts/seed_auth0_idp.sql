-- Idempotent seed for the Auth0 SAML IdP config used in local dev.
-- Safe to run repeatedly: ON CONFLICT updates all mutable columns.
-- Certificate, SSO URL, and entity ID match the dev-0ziq1qjvgrwq4w2g tenant.

INSERT INTO saml_idp_configs (
    id,
    name,
    entity_id,
    sso_url,
    slo_url,
    certificate,
    attribute_map,
    group_role_map,
    is_enabled,
    created_at,
    updated_at
) VALUES (
    'c364ac04-8594-4c06-a8bf-d5b4128888d3',
    'auth0-dev',
    'urn:dev-0ziq1qjvgrwq4w2g.us.auth0.com',
    'https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/samlp/k39kf4mjdKlScqCp6B1mtSgGpSmmROk8',
    NULL,
    '-----BEGIN CERTIFICATE-----
MIIDHTCCAgWgAwIBAgIJOPaDmmzSx0MHMA0GCSqGSIb3DQEBCwUAMCwxKjAoBgNV
BAMTIWRldi0wemlxMXFqdmdyd3E0dzJnLnVzLmF1dGgwLmNvbTAeFw0yNTA2Mjgx
NzA3MTZaFw0zOTAzMDcxNzA3MTZaMCwxKjAoBgNVBAMTIWRldi0wemlxMXFqdmdy
d3E0dzJnLnVzLmF1dGgwLmNvbTCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoC
ggEBAJqVnXWepO+TMqqZXbPsKBxg9pMpjTU8pGBw5yF2NUAS3z8qnCrKGbuZy49L
bNQh7VVPkfzdxyaHVcRJy5xYTBOr8igOBnkTGDc50V2G9gW7rdTm9fh0G5M0lyUm
SRvhmPdIwXO302EJCnxX8Tx2VTYAR1yUscbJMFBGjhZqYHIz/yE9cph/8x0QKwyS
9wptgIAZ7aosYwI119lQvzKrsYt9HpVXQtd/mHoYy7dSJue8RUjyjDExPfEhRx64
mw0Ece6hGyJ8qfPjFAW3kbjCwTvLPSWTabRcxg95b9mo14hPtd9t6mKkiVhHcKYy
Psuej5sqhWLfD8kNGrAdQ6h2wE0CAwEAAaNCMEAwDwYDVR0TAQH/BAUwAwEB/zAd
BgNVHQ4EFgQUn1s4D05lLFsAHHveyAnPH+9dXQAwDgYDVR0PAQH/BAQDAgKEMA0G
CSqGSIb3DQEBCwUAA4IBAQBJEb/qjrF+VA4D4fsu5KUJH0bfjIIkUj5MV+aPSvHC
EtCzOrUlW1MZCvvjUyD0tgzMDpY8IDP5zzwy2M1ZV/w8FdwoTE3cgk99scNJOXda
bLSz8KxNUbs1wy7ZJvlY270zADam0CynHhgZqs5bzwi+HEkcJeQ9n+69QkGKGXrW
yTawEpdGLhwSSUb2TlwyXcZplrB/s97JNNmIaIY47ED4qrrxEg5jFtCp4LAw/Aca
BUm46QRLVgSpdkXe8d04bz+sZ1pZsOvhHq3z1NPSLr4uGho2/3HDcAzyCcVKIfah
zaCS7VV0c7rDPWcAqtQMD4CEX7J8ZC43ZnmZB46baPIU
-----END CERTIFICATE-----',
    '{"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress": "email", "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name": "display_name"}',
    '{}',
    true,
    now(),
    now()
)
ON CONFLICT (name) DO UPDATE SET
    entity_id     = EXCLUDED.entity_id,
    sso_url       = EXCLUDED.sso_url,
    certificate   = EXCLUDED.certificate,
    attribute_map = EXCLUDED.attribute_map,
    is_enabled    = EXCLUDED.is_enabled,
    updated_at    = now();
