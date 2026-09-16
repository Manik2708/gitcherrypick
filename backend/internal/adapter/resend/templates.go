package resend

import (
	"bytes"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// DefaultRenderer turns a notification into a subject and an HTML body.
//
// html/template rather than text/template or fmt: every value in Notification.Data
// originates with a user — an organization name, a display name, a skill slug —
// and lands in an email a third party renders. Contextual escaping is what stops
// an organization called `<script>` from becoming one.
type DefaultRenderer struct {
	// appURL is the web client's origin, used to turn a bare proof into a
	// link somebody can click. Empty is tolerated: the token is then shown on
	// its own with instructions, which is worse to use but still usable —
	// better than a mail with nothing in it at all.
	appURL string

	once      sync.Once
	templates map[port.NotificationKind]*template.Template
	subjects  map[port.NotificationKind]string
	err       error
}

// NewDefaultRenderer returns the built-in templates.
//
// appURL is where a link in a mail points. It is a parameter rather than a
// constant because the web client is separately deployed, and in development
// it is not even on the same port as the API.
func NewDefaultRenderer(appURL string) *DefaultRenderer {
	return &DefaultRenderer{appURL: strings.TrimRight(appURL, "/")}
}

var _ Renderer = (*DefaultRenderer)(nil)

// bodies are the HTML templates, keyed by kind.
//
// Deliberately plain. These are transactional notices, and the one that carries
// a decision — a contact request — must present the payment-verified fact
// plainly rather than bury it (ADR-0002 §5).
var bodies = map[port.NotificationKind]string{
	// The role rides on this message (ADR-0019 §2). It is the same contact
	// request it always was — the address is still released only on acceptance
	// — but an invitation somebody cannot evaluate is one they have to decline
	// on suspicion, and the process fields are disclosed here and nowhere else
	// (ADR-0017).
	port.NotifyContactRequest: `<p><strong>{{.organization}}</strong> is interested in your work{{if .role_title}}, for <strong>{{.role_title}}</strong>{{end}}.</p>
{{if .role_title}}<p>{{.role_engagement}}, {{.role_location}}.</p>{{end}}
<p>Payment verified: {{if .payment_verified}}yes{{else}}<strong>no</strong> — this organization has not verified payment details{{end}}.</p>
<p>They expect to decide by {{.tentative_result_date}}.</p>
{{if .max_interview_rounds}}<p>Their process: at most {{.max_interview_rounds}} interview rounds{{if .avg_days_to_offer}}, and typically {{.avg_days_to_offer}} days from the first round to an offer{{end}}.{{if .requires_online_test}} An online test is part of it.{{end}}</p>{{end}}
<p>Your email address is not shared unless you accept.</p>`,

	port.NotifyContactAccepted: `<p>{{.contributor}} accepted your contact request.</p>
<p>You can reach them at {{.email}}.</p>`,

	port.NotifyEvaluationComplete: `<p>Your claim has been evaluated.</p>
<p>Overall score: {{.overall_score}}.</p>`,

	port.NotifyAvailabilityLapsing: `<p>Your availability window lapses on {{.expires_at}}.</p>
<p>Until you refresh it, hirers searching by default will not see you.</p>`,

	// The token is the whole point of this message. It is minted once, stored
	// as a hash, and returned by no endpoint — so a template that omitted it
	// would make redemption impossible for everybody, silently. It is rendered
	// as a link where an app URL is configured and as the bare proof where it
	// is not, because a token nobody can act on is no better than none.
	port.NotifyEmailVerification: `<p>Confirm this address to finish setting up your seat{{if .organization}} at <strong>{{.organization}}</strong>{{end}}.</p>
{{if .link}}<p><a href="{{.link}}">Confirm this address</a></p>
<p>If the link does not open, paste this code instead: <code>{{.token}}</code></p>
{{else}}<p>Enter this code to continue: <code>{{.token}}</code></p>{{end}}
<p>This expires on {{.expires_at}}, and works once.</p>
<p>If you were not expecting this, ignore it — nothing happens until it is used.</p>`,

	// Onboarding. The reader has no account and no organisation yet — this is
	// the first thing the platform ever sends them — so it says what the code
	// is for rather than assuming they remember submitting a form.
	port.NotifyOnboardingVerification: `<p>Confirm this address to finish listing{{if .organization}} <strong>{{.organization}}</strong>{{else}} your organisation{{end}}.</p>
{{if .link}}<p><a href="{{.link}}">Confirm and choose how you will sign in</a></p>
<p>If the link does not open, paste this code instead: <code>{{.token}}</code></p>
{{else}}<p>Enter this code to continue: <code>{{.token}}</code></p>{{end}}
<p>This expires on {{.expires_at}}, and works once. You will choose a username and a password, and an administrator then reviews the company before it can hire.</p>
<p>If you were not expecting this, ignore it — nothing is created until the code is used.</p>`,

	// The decision. An approval and a refusal are one kind because they answer
	// one question, and a reader who was refused needs the reason and the way
	// back in the same message rather than a bare "no".
	port.NotifyOnboardingDecided: `{{if .approved}}<p><strong>{{.organization}}</strong> has been approved. You can sign in as <code>{{.username}}</code>.</p>
{{if .link}}<p><a href="{{.link}}">Sign in</a></p>{{end}}
{{else}}<p><strong>{{.organization}}</strong> was not approved.</p>
{{if .reason}}<p>Reason: {{.reason}}</p>{{end}}
{{if .link}}<p><a href="{{.link}}">Correct the details and submit again</a></p>
<p>Your previous answers are kept, so you only need to change what was wrong.</p>
{{else if .token}}<p>Enter this code to correct and resubmit: <code>{{.token}}</code></p>{{end}}
{{end}}`,

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
	port.NotifyContactRequest:         "An organization is interested in your work",
	port.NotifyContactAccepted:        "Your contact request was accepted",
	port.NotifyEvaluationComplete:     "Your claim has been evaluated",
	port.NotifyAvailabilityLapsing:    "Your availability is about to lapse",
	port.NotifyEmailVerification:      "Confirm your email address",
	port.NotifyOnboardingVerification: "Confirm your company's email address",
	port.NotifyOnboardingDecided:      "Your organization has been reviewed",
	port.NotifyVerificationDecided:    "Your organization verification was decided",
	port.NotifySkillRequestDecided:    "Your skill request was decided",
	port.NotifyReevaluationDecided:    "Your re-evaluation request was decided",
	port.NotifyOverdueShortlists:      "You have overdue hiring rounds",
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

	data := r.presentable(n)

	var body bytes.Buffer
	if err := tmpl.Execute(&body, data); err != nil {
		return "", "", fmt.Errorf("rendering %s: %w", n.Kind, err)
	}
	return subject, body.String(), nil
}

// presentable turns service values into things a person can read.
//
// Done here rather than at each call site because a service that had to
// remember to format a timestamp would eventually forget, and what reaches the
// recipient is then a Go time.Time printed with its monotonic clock reading and
// the server's timezone — which is exactly what this fixes.
//
// It copies rather than mutating n.Data: the caller's map is not ours, and a
// retry that re-rendered a mutated copy would compound the change.
func (r *DefaultRenderer) presentable(n port.Notification) map[string]any {
	out := make(map[string]any, len(n.Data)+1)
	for k, v := range n.Data {
		if t, ok := v.(time.Time); ok {
			// A date and a time, in UTC, with no monotonic reading. Recipients
			// are worldwide and the server's local zone means nothing to them.
			out[k] = t.UTC().Format("2 January 2006 at 15:04 MST")
			continue
		}
		out[k] = v
	}

	// A proof travels as a link where there is somewhere for it to point, and
	// where the kind says where that is.
	if token, ok := n.Data["token"].(string); ok && token != "" && r.appURL != "" {
		if path := claimPath(n.Kind); path != "" {
			out["link"] = fmt.Sprintf("%s%s?token=%s", r.appURL, path, url.QueryEscape(token))
		}
	}

	// A decision carries a link too. An approved owner is sent to sign in; a
	// refused company to the form that reopens their answers — carrying the
	// CODE, because Revise spends a proof and the original was consumed at
	// verify. A link with only a submission id opens a form that cannot be
	// submitted (ADR-0017 §8).
	if n.Kind == port.NotifyOnboardingDecided && r.appURL != "" {
		if approved, _ := n.Data["approved"].(bool); approved {
			out["link"] = r.appURL + "/signin"
		} else if code, ok := n.Data["token"].(string); ok && code != "" {
			out["link"] = fmt.Sprintf("%s/organisation/revise?token=%s",
				r.appURL, url.QueryEscape(code))
		}
	}
	return out
}

// claimPath is where a proof of address is redeemed, by kind.
//
// A map from kind to route rather than one path for every token: a roster
// redemption claims a seat and an onboarding code finishes describing a
// company, and sending either reader to the other's screen would strand them.
func claimPath(kind port.NotificationKind) string {
	switch kind {
	case port.NotifyEmailVerification:
		return "/redeem"
	case port.NotifyOnboardingVerification:
		return "/organisation/verify"
	}
	return ""
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
