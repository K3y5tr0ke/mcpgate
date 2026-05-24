package saml_test

import (
	"testing"

	cssaml "github.com/crewjam/saml"

	"github.com/K3y5tr0ke/mcpgate/internal/auth/saml"
)

func attr(name, friendlyName string, values ...string) cssaml.Attribute {
	vals := make([]cssaml.AttributeValue, len(values))
	for i, v := range values {
		vals[i] = cssaml.AttributeValue{Value: v}
	}
	return cssaml.Attribute{
		Name:         name,
		FriendlyName: friendlyName,
		Values:       vals,
	}
}

func assertion(attrs ...cssaml.Attribute) *cssaml.Assertion {
	return &cssaml.Assertion{
		AttributeStatements: []cssaml.AttributeStatement{
			{Attributes: attrs},
		},
	}
}

func assertionWithNameID(nameID string, attrs ...cssaml.Attribute) *cssaml.Assertion {
	a := assertion(attrs...)
	a.Subject = &cssaml.Subject{
		NameID: &cssaml.NameID{Value: nameID},
	}
	return a
}

var defaultMapping = map[string]string{
	"urn:oid:1.2.840.113549.1.9.1": "email",
	"mail":                         "email",
	"displayName":                  "display_name",
	"memberOf":                     "groups",
}

func TestExtract_URNMapping(t *testing.T) {
	a := assertion(
		attr("urn:oid:1.2.840.113549.1.9.1", "", "alice@example.com"),
		attr("displayName", "displayName", "Alice"),
	)
	got := saml.Extract(a, defaultMapping)

	if got.Email != "alice@example.com" {
		t.Errorf("Email: got %q", got.Email)
	}
	if got.DisplayName != "Alice" {
		t.Errorf("DisplayName: got %q", got.DisplayName)
	}
}

func TestExtract_FriendlyNameMapping(t *testing.T) {
	a := assertion(
		attr("some:urn:irrelevant", "mail", "bob@example.com"),
		attr("some:urn:irrelevant2", "displayName", "Bob"),
	)
	got := saml.Extract(a, defaultMapping)

	if got.Email != "bob@example.com" {
		t.Errorf("Email: got %q", got.Email)
	}
	if got.DisplayName != "Bob" {
		t.Errorf("DisplayName: got %q", got.DisplayName)
	}
}

func TestExtract_MultiValueGroups(t *testing.T) {
	a := assertion(
		attr("memberOf", "memberOf", "admins", "viewers", "devs"),
	)
	got := saml.Extract(a, defaultMapping)

	if len(got.Groups) != 3 {
		t.Fatalf("Groups: got %v, want 3 items", got.Groups)
	}
	if got.Groups[0] != "admins" || got.Groups[1] != "viewers" || got.Groups[2] != "devs" {
		t.Errorf("Groups: got %v", got.Groups)
	}
}

func TestExtract_NameIDFallback(t *testing.T) {
	// No email attribute → should fall back to NameID.
	a := assertionWithNameID("carol@example.com")
	got := saml.Extract(a, defaultMapping)

	if got.Email != "carol@example.com" {
		t.Errorf("Email (NameID fallback): got %q", got.Email)
	}
}

func TestExtract_EmailAttributeTakesPrecedenceOverNameID(t *testing.T) {
	a := assertionWithNameID("nameid@example.com",
		attr("mail", "mail", "attr@example.com"),
	)
	got := saml.Extract(a, defaultMapping)

	if got.Email != "attr@example.com" {
		t.Errorf("Email: got %q, want attr email", got.Email)
	}
}

func TestExtract_UnmappedAttributesIgnored(t *testing.T) {
	a := assertion(
		attr("unknown:attr", "unknownFriendly", "some-value"),
		attr("mail", "mail", "dave@example.com"),
	)
	got := saml.Extract(a, defaultMapping)

	if got.Email != "dave@example.com" {
		t.Errorf("Email: got %q", got.Email)
	}
	// No panic, no extra fields.
}
