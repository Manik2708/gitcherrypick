package resend_test

import (
	"strings"
	"testing"
	"time"

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
	port.NotifyEmailVerification,
	port.NotifyOnboardingVerification,
	port.NotifyOnboardingDecided,
	port.NotifyVerificationDecided,
	port.NotifySkillRequestDecided,
	port.NotifyReevaluationDecided,
	port.NotifyOverdueShortlists,
}

func TestEveryNotificationKindRenders(t *testing.T) {
	r := resend.NewDefaultRenderer("https://app.example")

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
	r := resend.NewDefaultRenderer("https://app.example")

	_, _, err := r.Render(port.Notification{Kind: "invented", Recipient: "a@b.c"})
	require.ErrorContains(t, err, "no template")
}

func TestNilDataDoesNotPanic(t *testing.T) {
	r := resend.NewDefaultRenderer("https://app.example")

	require.NotPanics(t, func() {
		_, _, err := r.Render(port.Notification{Kind: port.NotifyContactRequest})
		require.NoError(t, err)
	})
}

func TestContactRequestStatesPaymentVerification(t *testing.T) {
	// ADR-0002 §5: a contributor deciding whether to release their address is
	// told what the platform knows. The unverified case must be unmissable,
	// not a silent omission.
	r := resend.NewDefaultRenderer("https://app.example")

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
	r := resend.NewDefaultRenderer("https://app.example")

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
	r := resend.NewDefaultRenderer("https://app.example")

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
	r := resend.NewDefaultRenderer("https://app.example")

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
	r := resend.NewDefaultRenderer("https://app.example")

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

// The token is the ONLY copy.
//
// It is minted once, stored as a SHA-256 hash, and returned by no endpoint.
// A template that rendered everything except the proof would make redemption
// impossible for every recipient, and nothing else in the system would notice:
// the send succeeds, the row is written, and the person just never gets in.
func TestVerificationMailCarriesTheProof(t *testing.T) {
	r := resend.NewDefaultRenderer("https://app.example")

	_, body, err := r.Render(port.Notification{
		Kind: port.NotifyEmailVerification, Recipient: "rita@acme.com",
		Data: map[string]any{
			"token":        "vB3s1Q7nQx2fK9aL0pR4tE6yU8iO2wZ5cM7xN1bV3dQ",
			"expires_at":   time.Date(2026, 9, 14, 9, 30, 0, 0, time.UTC),
			"organization": "Acme Corp",
		},
	})
	require.NoError(t, err)
	require.Contains(t, body, "vB3s1Q7nQx2fK9aL0pR4tE6yU8iO2wZ5cM7xN1bV3dQ")
	require.Contains(t, body, "https://app.example/redeem?token=")
	require.Contains(t, body, "Acme Corp")
}

// With nowhere for a link to point, the bare proof still has to be there. A
// harder message to act on is not the same as an unusable one.
func TestVerificationMailWithNoAppURLStillCarriesTheToken(t *testing.T) {
	r := resend.NewDefaultRenderer("")

	_, body, err := r.Render(port.Notification{
		Kind: port.NotifyEmailVerification, Recipient: "rita@acme.com",
		Data: map[string]any{
			"token":      "vB3s1Q7nQx2fK9aL0pR4tE6yU8iO2wZ5cM7xN1bV3dQ",
			"expires_at": time.Date(2026, 9, 14, 9, 30, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)
	require.Contains(t, body, "vB3s1Q7nQx2fK9aL0pR4tE6yU8iO2wZ5cM7xN1bV3dQ")
	require.NotContains(t, body, "href")
}

// A time.Time printed by a template carries its monotonic reading and the
// server's timezone: "2026-08-13 14:30:11.080659 +0530 IST m=-2680040.6093".
// Formatting here rather than at each call site is what stops that reaching an
// inbox the next time somebody adds a template with a date in it.
func TestTimestampsAreFormattedForPeople(t *testing.T) {
	r := resend.NewDefaultRenderer("https://app.example")

	for _, kind := range []port.NotificationKind{
		port.NotifyEmailVerification,
		port.NotifyOnboardingVerification,
		port.NotifyAvailabilityLapsing,
	} {
		t.Run(string(kind), func(t *testing.T) {
			_, body, err := r.Render(port.Notification{
				Kind: kind, Recipient: "a@b.c",
				Data: map[string]any{
					"token":      "vB3s1Q7nQx2fK9aL0pR4tE6yU8iO2wZ5cM7xN1bV3dQ",
					"expires_at": time.Date(2026, 9, 14, 9, 30, 0, 0, time.UTC),
				},
			})
			require.NoError(t, err)
			require.Contains(t, body, "14 September 2026 at 09:30 UTC")
			require.NotContains(t, body, "m=", "a monotonic clock reading reached the body")
			require.NotContains(t, body, "+0000")
		})
	}
}

// Each proof goes to the screen that can redeem it.
//
// The bug this pins: one hardcoded /redeem path for every token. A company
// finishing onboarding sent to the seat-claiming screen has a valid code and
// nowhere to use it — and nothing fails, because both screens exist.
func TestProofsLinkToTheirOwnScreen(t *testing.T) {
	r := resend.NewDefaultRenderer("https://app.example")
	const token = "vB3s1Q7nQx2fK9aL0pR4tE6yU8iO2wZ5cM7xN1bV3dQ"

	cases := map[port.NotificationKind]string{
		port.NotifyEmailVerification:      "https://app.example/redeem?token=",
		port.NotifyOnboardingVerification: "https://app.example/organisation/verify?token=",
	}
	for kind, want := range cases {
		t.Run(string(kind), func(t *testing.T) {
			_, body, err := r.Render(port.Notification{
				Kind: kind, Recipient: "jo@acme.example",
				Data: map[string]any{
					"token": token, "organization": "Acme Corp",
					"expires_at": time.Date(2026, 9, 14, 9, 30, 0, 0, time.UTC),
				},
			})
			require.NoError(t, err)
			require.Contains(t, body, want)
			require.Contains(t, body, token, "the code is the fallback when a link will not open")
		})
	}
}

// A refusal that does not say why, and does not say what to do, is a dead end
// for a company that mistyped a postcode (ADR-0017 §8).
func TestRejectionCarriesTheReasonAndTheWayBack(t *testing.T) {
	r := resend.NewDefaultRenderer("https://app.example")

	_, body, err := r.Render(port.Notification{
		Kind: port.NotifyOnboardingDecided, Recipient: "jo@acme.example",
		Data: map[string]any{
			"approved": false, "organization": "Acme Corp",
			"reason":        "The address does not match the website.",
			"submission_id": "01920000-0000-7000-8000-00000000e001",
			"token":         "vB3s1Q7nQx2fK9aL0pR4tE6yU8iO2wZ5cM7xN1bV3dQ",
		},
	})
	require.NoError(t, err)
	require.Contains(t, body, "The address does not match the website.")

	// The link carries the CODE, not the submission id. Revise spends a proof
	// and the original was consumed at verify, so a link with only an id opens
	// a form the company cannot submit — the route would exist and reach
	// nobody.
	require.Contains(t, body, "/organisation/revise?token=vB3s1Q7nQx2fK9aL0pR4tE6yU8iO2wZ5cM7xN1bV3dQ")
	require.NotContains(t, body, "?submission=")
	// "was not approved" contains "approved", so the assertion is on the
	// approval's own sentence rather than on the word.
	require.Contains(t, body, "was not approved")
	require.NotContains(t, body, "has been approved")
	require.NotContains(t, body, "/signin")
}

func TestApprovalSendsTheOwnerToSignIn(t *testing.T) {
	r := resend.NewDefaultRenderer("https://app.example")

	_, body, err := r.Render(port.Notification{
		Kind: port.NotifyOnboardingDecided, Recipient: "jo@acme.example",
		Data: map[string]any{
			"approved": true, "organization": "Acme Corp", "username": "jo",
		},
	})
	require.NoError(t, err)
	require.Contains(t, body, "has been approved")
	require.Contains(t, body, "jo")
	require.Contains(t, body, "https://app.example/signin")
}

// A resend knows the address and the purpose, but not the company name — it
// rotates a stored proof rather than re-running the form. The template has to
// read properly without it.
func TestOnboardingMailReadsWithoutACompanyName(t *testing.T) {
	r := resend.NewDefaultRenderer("https://app.example")

	_, body, err := r.Render(port.Notification{
		Kind: port.NotifyOnboardingVerification, Recipient: "jo@acme.example",
		Data: map[string]any{
			"token":      "vB3s1Q7nQx2fK9aL0pR4tE6yU8iO2wZ5cM7xN1bV3dQ",
			"expires_at": time.Date(2026, 9, 14, 9, 30, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)
	require.Contains(t, body, "finish listing your organisation")
	require.NotContains(t, body, "listing  .", "an empty name left a gap in the sentence")
	require.Contains(t, body, "https://app.example/organisation/verify?token=")
}
