package saml

import (
	"github.com/crewjam/saml"
)

// MappedAttributes holds the user attributes extracted from a SAML assertion.
type MappedAttributes struct {
	Email       string
	DisplayName string
	Groups      []string
}

// Extract maps SAML assertion attributes to MappedAttributes using the provided
// attribute mapping. mapping keys are the friendly names or URNs used in the
// assertion; values are the canonical field names ("email", "display_name",
// "groups"). Unmapped attributes are silently ignored.
func Extract(assertion *saml.Assertion, mapping map[string]string) MappedAttributes {
	var out MappedAttributes

	for _, stmt := range assertion.AttributeStatements {
		for _, attr := range stmt.Attributes {
			// Match by friendly name first, then by Name (URN/OID).
			canonical, ok := mapping[attr.FriendlyName]
			if !ok {
				canonical, ok = mapping[attr.Name]
			}
			if !ok {
				continue
			}

			values := attrValues(attr)
			if len(values) == 0 {
				continue
			}

			switch canonical {
			case "email":
				out.Email = values[0]
			case "display_name":
				out.DisplayName = values[0]
			case "groups":
				out.Groups = append(out.Groups, values...)
			}
		}
	}

	// Fall back to NameID for email if not mapped via attributes.
	if out.Email == "" && assertion.Subject != nil && assertion.Subject.NameID != nil {
		out.Email = assertion.Subject.NameID.Value
	}

	return out
}

func attrValues(attr saml.Attribute) []string {
	out := make([]string, 0, len(attr.Values))
	for _, v := range attr.Values {
		if v.Value != "" {
			out = append(out, v.Value)
		}
	}
	return out
}
