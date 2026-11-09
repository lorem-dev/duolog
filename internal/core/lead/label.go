package lead

import (
	"strings"
	"unicode"
)

// maxLeadName bounds the lead's display name in a session label, in bytes.
const maxLeadName = 120

// SessionLabel is the name a Claude worker or peer starts under, such as
// "duolog-worker-implement (lead: Fix the parser)": what the session is, its
// role and whose it is. kind is "worker" or "peer". leadName is the lead's
// display name, any text; line breaks and control characters fold to single
// spaces, and a long name is cut. A lead without a readable name is named by
// the first 6 characters of its id. The label holds no path.
func SessionLabel(kind, role, leadName, leadID string) string {
	name := strings.Join(strings.FieldsFunc(leadName, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if len(name) > maxLeadName {
		name = strings.TrimSpace(strings.ToValidUTF8(name[:maxLeadName], ""))
	}
	if name == "" {
		name = leadID
		if len(name) > 6 {
			name = name[:6]
		}
	}
	return "duolog-" + kind + "-" + role + " (lead: " + name + ")"
}
