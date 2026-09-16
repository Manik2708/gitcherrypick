package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// The mailbox the fixtures read.
//
// ADR-0016 makes an emailed proof the ONLY way to redeem a roster entry: the
// token is minted once, stored as a hash, and handed to the recipient's inbox.
// Nothing in the database, and nothing in any response, can give it back. So a
// fixture that redeems an entry has to read the message, exactly as the person
// it was addressed to would.
//
// This is not a shortcut around the flow — it is the flow. A harness that
// reached into email_verifications for the plaintext could not, because the
// plaintext is not there; one that minted its own token would be testing its
// own arithmetic. Reading the delivered message is the only honest path, and
// it has the side effect of asserting that the proof was actually delivered.
//
// cmd/fakethirdparty records every message the real Resend adapter sent
// (ADR-0010), which is what /_sent/emails returns.

// emailQuery is the body of a READ_EMAIL step: which message to look at, and
// how many the fixture says should be there.
type emailQuery struct {
	// To filters by recipient. Required — "the last email anybody got" is not
	// a thing a fixture should ever assert against.
	To string `json:"to"`

	// Subject narrows further, for a case that sends two kinds of message to
	// one address.
	Subject string `json:"subject"`

	// Count is the assertion. A pointer because 0 is the interesting value:
	// a roster miss must send NOTHING, and "expected no message" is the only
	// way to state that (ADR-0016 §3).
	Count *int `json:"count"`
}

// deliveredEmail is one message as a fixture addresses it.
//
// `token` is extracted rather than delivered as a field: the email is HTML
// written for a person, and the proof is whatever opaque string it carries.
type deliveredEmail struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Token   string `json:"token"`
}

// tokenRe matches an opaque token as crypto.Minter produces one: 32 random
// bytes in base64url, which is 43 characters and no padding.
//
// Anchored on length rather than on link syntax, because whether the proof
// travels as a bare token or inside a URL is a presentation decision, and a
// fixture should not fail when somebody wraps it in an anchor tag.
var tokenRe = regexp.MustCompile(`[A-Za-z0-9_-]{43,}`)

// readEmail answers a READ_EMAIL step.
//
// It returns the message as JSON so the runner compares and captures from it
// the same way it does an HTTP response — a fixture writes `capture:
// {"proof": "$.token"}` and nothing about this step is special.
func readEmail(ctx context.Context, control *Control, bindings map[string]string, step Step) ([]byte, error) {
	var query emailQuery
	if len(step.Request.Body) > 0 {
		resolved, err := resolveJSON(step.Request.Body, bindings)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(resolved, &query); err != nil {
			return nil, fmt.Errorf("decoding the READ_EMAIL query: %w", err)
		}
	}
	if query.To == "" {
		return nil, fmt.Errorf("READ_EMAIL needs a `to` address in its body")
	}

	sent, err := control.SentEmails(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]SentEmail, 0, len(sent))
	for _, message := range sent {
		if !addressedTo(message, query.To) {
			continue
		}
		if query.Subject != "" && message.Subject != query.Subject {
			continue
		}
		matches = append(matches, message)
	}

	if query.Count != nil && len(matches) != *query.Count {
		return nil, fmt.Errorf(
			"expected %d message(s) to %s%s, found %d — subjects so far: %s",
			*query.Count, query.To, describeSubject(query.Subject), len(matches), subjectsOf(sent))
	}
	if len(matches) == 0 {
		// Nothing to capture from, and nothing to compare. A count of 0 has
		// already been satisfied above.
		return nil, nil
	}

	// The LAST match. A resend replaces the outstanding proof, so the newest
	// message is the only one that still works (ADR-0016 §8).
	latest := matches[len(matches)-1]
	out := deliveredEmail{
		To:      query.To,
		Subject: latest.Subject,
		HTML:    latest.HTML,
		Token:   tokenRe.FindString(latest.HTML),
	}

	// A proof with no token in it is a dead end for its recipient: they were
	// told to confirm an address and given nothing to confirm it with. Failing
	// here is the assertion that the message is usable, not merely sent.
	if _, wanted := capturesToken(step); wanted && out.Token == "" {
		return nil, fmt.Errorf(
			"the message to %s carries no token, so its recipient cannot redeem anything\nbody: %s",
			query.To, latest.HTML)
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encoding the message: %w", err)
	}
	return encoded, nil
}

// capturesToken reports whether the step asks for `$.token`.
func capturesToken(step Step) (string, bool) {
	for name, path := range step.Capture {
		if strings.TrimPrefix(path, "$.") == "token" {
			return name, true
		}
	}
	return "", false
}

func addressedTo(message SentEmail, address string) bool {
	for _, recipient := range message.To {
		if strings.EqualFold(recipient, address) {
			return true
		}
	}
	return false
}

func describeSubject(subject string) string {
	if subject == "" {
		return ""
	}
	return fmt.Sprintf(" with subject %q", subject)
}

// subjectsOf lists what WAS sent, so "found 0" names the alternatives rather
// than leaving the reader to guess whether anything was sent at all.
func subjectsOf(sent []SentEmail) string {
	if len(sent) == 0 {
		return "(nothing was sent)"
	}
	parts := make([]string, 0, len(sent))
	for _, message := range sent {
		parts = append(parts, fmt.Sprintf("%s -> %q", strings.Join(message.To, ","), message.Subject))
	}
	return strings.Join(parts, "; ")
}
