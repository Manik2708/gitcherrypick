package resend_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/resend"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// everyKind is the full set from port. A kind with no template is a
// notification the platform decides to send and then cannot.
var everyKind = []port.NotificationKind{
	port.NotifyContactRequest,
	port.NotifyContactAccepted,
	port.NotifyEvaluationComplete,
	port.NotifyAvailabilityLapsing,
	port.NotifyOrgInvitation,
	port.NotifyVerificationDecided,
	port.NotifySkillRequestDecided,
	port.NotifyReevaluationDecided,
	port.NotifyOverdueShortlists,
}

func TestEveryNotificationKindRenders(t *testing.T) {
	r := resend.NewDefaultRenderer()

	for _, kind := range everyKind {
		t.Run(string(kind), func(t *testing.T) {
			subject, body, err := r.Render(port.Notification{Kind: kind, Recipient: "a@b.c"})
			require.NoError(t, err)
			require.NotEmpty(t, subject)
			require.NotEmpty(t, body)
		})
	}
}

func TestUnknownKindIsAnError(t *testing.T) {
	// Sending a contributor something the platform cannot describe is worse
	// than sending nothing and logging why.
	r := resend.NewDefaultRenderer()

	_, _, err := r.Render(port.Notification{Kind: "invented", Recipient: "a@b.c"})
	require.ErrorContains(t, err, "no template")
}

func TestNilDataDoesNotPanic(t *testing.T) {
	r := resend.NewDefaultRenderer()

	require.NotPanics(t, func() {
		_, _, err := r.Render(port.Notification{Kind: port.NotifyContactRequest})
		require.NoError(t, err)
	})
}

func TestContactRequestStatesPaymentVerification(t *testing.T) {
	// ADR-0002 §5: a contributor deciding whether to release their address is
	// told what the platform knows. The unverified case must be unmissable,
	// not a silent omission.
	r := resend.NewDefaultRenderer()

	_, verified, err := r.Render(port.Notification{
		Kind: port.NotifyContactRequest,
		Data: map[string]any{"organization": "Acme", "payment_verified": true},
	})
	require.NoError(t, err)
	require.Contains(t, verified, "Payment verified: yes")

	_, unverified, err := r.Render(port.Notification{
		Kind: port.NotifyContactRequest,
		Data: map[string]any{"organization": "Acme", "payment_verified": false},
	})
	require.NoError(t, err)
	require.Contains(t, unverified, "has not verified payment details")
	require.NotContains(t, unverified, "Payment verified: yes")
}

func TestContactRequestDoesNotLeakTheAddress(t *testing.T) {
	// The whole point of the two-step consent flow: the hirer learns nothing
	// until the contributor accepts.
	r := resend.NewDefaultRenderer()

	_, body, err := r.Render(port.Notification{
		Kind:      port.NotifyContactRequest,
		Recipient: "alice@example.com",
		Data:      map[string]any{"organization": "Acme", "payment_verified": true},
	})
	require.NoError(t, err)
	require.Contains(t, body, "not shared unless you accept")
}

func TestUserSuppliedValuesAreEscaped(t *testing.T) {
	// Organization names are user-chosen and land in an email a third party
	// renders. html/template escapes contextually; fmt would not.
	r := resend.NewDefaultRenderer()

	_, body, err := r.Render(port.Notification{
		Kind: port.NotifyContactRequest,
		Data: map[string]any{
			"organization":     `<script>alert("xss")</script>`,
			"payment_verified": true,
		},
	})
	require.NoError(t, err)

	require.NotContains(t, body, "<script>")
	require.Contains(t, body, "&lt;script&gt;")
}

func TestOptionalReasonIsOmittedWhenAbsent(t *testing.T) {
	r := resend.NewDefaultRenderer()

	_, without, err := r.Render(port.Notification{
		Kind: port.NotifyVerificationDecided,
		Data: map[string]any{"decision": "approved"},
	})
	require.NoError(t, err)
	require.NotContains(t, without, "Reason:")

	_, with, err := r.Render(port.Notification{
		Kind: port.NotifyVerificationDecided,
		Data: map[string]any{"decision": "rejected", "reason": "no public evidence of trading"},
	})
	require.NoError(t, err)
	require.Contains(t, with, "Reason: no public evidence of trading")
}

func TestSubjectsAreDistinct(t *testing.T) {
	// Nine notices that all read the same in an inbox are nine notices nobody
	// opens.
	r := resend.NewDefaultRenderer()

	seen := map[string]port.NotificationKind{}
	for _, kind := range everyKind {
		subject, _, err := r.Render(port.Notification{Kind: kind})
		require.NoError(t, err)

		previous, duplicate := seen[subject]
		require.False(t, duplicate, "%s and %s share the subject %q", kind, previous, subject)
		seen[subject] = kind

		require.False(t, strings.HasPrefix(subject, "Subject for"),
			"placeholder subject left in for %s", kind)
	}
}
