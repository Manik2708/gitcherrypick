package resend

import (
	"bytes"
	"fmt"
	"html/template"
	"sync"

	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// DefaultRenderer turns a notification into a subject and an HTML body.
//
// html/template rather than text/template or fmt: every value in Notification.Data
// originates with a user — an organization name, a display name, a skill slug —
// and lands in an email a third party renders. Contextual escaping is what stops
// an organization called `<script>` from becoming one.
type DefaultRenderer struct {
	once      sync.Once
	templates map[port.NotificationKind]*template.Template
	subjects  map[port.NotificationKind]string
	err       error
}

// NewDefaultRenderer returns the built-in templates.
func NewDefaultRenderer() *DefaultRenderer { return &DefaultRenderer{} }

var _ Renderer = (*DefaultRenderer)(nil)

// bodies are the HTML templates, keyed by kind.
//
// Deliberately plain. These are transactional notices, and the one that carries
// a decision — a contact request — must present the payment-verified fact
// plainly rather than bury it (ADR-0002 §5).
var bodies = map[port.NotificationKind]string{
	port.NotifyContactRequest: `<p><strong>{{.organization}}</strong> is interested in your work.</p>
<p>Payment verified: {{if .payment_verified}}yes{{else}}<strong>no</strong> — this organization has not verified payment details{{end}}.</p>
<p>They expect to decide by {{.tentative_result_date}}.</p>
<p>Your email address is not shared unless you accept.</p>`,

	port.NotifyContactAccepted: `<p>{{.contributor}} accepted your contact request.</p>
<p>You can reach them at {{.email}}.</p>`,

	port.NotifyEvaluationComplete: `<p>Your claim has been evaluated.</p>
<p>Overall score: {{.overall_score}}.</p>`,

	port.NotifyAvailabilityLapsing: `<p>Your availability window lapses on {{.expires_at}}.</p>
<p>Until you refresh it, hirers searching by default will not see you.</p>`,

	port.NotifyOrgInvitation: `<p>{{.inviter}} invited you to join <strong>{{.organization}}</strong>.</p>
<p>This invitation expires on {{.expires_at}}.</p>`,

	port.NotifyVerificationDecided: `<p>Your organization verification was {{.decision}}.</p>
{{if .reason}}<p>Reason: {{.reason}}</p>{{end}}`,

	port.NotifySkillRequestDecided: `<p>Your request for the skill "{{.skill}}" was {{.decision}}.</p>
{{if .reason}}<p>Reason: {{.reason}}</p>{{end}}`,

	port.NotifyReevaluationDecided: `<p>Your re-evaluation request was {{.decision}}.</p>
{{if .reason}}<p>Reason: {{.reason}}</p>{{end}}`,

	port.NotifyOverdueShortlists: `<p>{{.overdue_count}} of your hiring rounds are past their stated result date.</p>
<p>Contributors were told to expect a decision. Please close or update them.</p>`,
}

var subjects = map[port.NotificationKind]string{
	port.NotifyContactRequest:      "An organization is interested in your work",
	port.NotifyContactAccepted:     "Your contact request was accepted",
	port.NotifyEvaluationComplete:  "Your claim has been evaluated",
	port.NotifyAvailabilityLapsing: "Your availability is about to lapse",
	port.NotifyOrgInvitation:       "You have been invited to an organization",
	port.NotifyVerificationDecided: "Your organization verification was decided",
	port.NotifySkillRequestDecided: "Your skill request was decided",
	port.NotifyReevaluationDecided: "Your re-evaluation request was decided",
	port.NotifyOverdueShortlists:   "You have overdue hiring rounds",
}

// Render produces the subject and body for a notification.
//
// An unknown kind is an ERROR rather than a generic message. Sending a
// contributor something the platform cannot describe is worse than sending
// nothing and logging why.
func (r *DefaultRenderer) Render(n port.Notification) (string, string, error) {
	r.once.Do(r.parse)
	if r.err != nil {
		return "", "", r.err
	}

	tmpl, ok := r.templates[n.Kind]
	if !ok {
		return "", "", fmt.Errorf("no template for notification kind %q", n.Kind)
	}
	subject := r.subjects[n.Kind]

	data := n.Data
	if data == nil {
		data = map[string]any{}
	}

	var body bytes.Buffer
	if err := tmpl.Execute(&body, data); err != nil {
		return "", "", fmt.Errorf("rendering %s: %w", n.Kind, err)
	}
	return subject, body.String(), nil
}

// parse compiles every template once.
//
// A parse failure is stored rather than panicking: a malformed template is a
// programming error, but it must surface as a failed notification rather than
// take down whatever goroutine happened to send first.
func (r *DefaultRenderer) parse() {
	r.templates = make(map[port.NotificationKind]*template.Template, len(bodies))
	r.subjects = subjects

	for kind, body := range bodies {
		tmpl, err := template.New(string(kind)).Parse(body)
		if err != nil {
			r.err = fmt.Errorf("parsing the %s template: %w", kind, err)
			return
		}
		r.templates[kind] = tmpl
	}
}
