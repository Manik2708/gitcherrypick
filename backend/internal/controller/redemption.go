package controller

import (
	"errors"
	"net/http"

	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// Roster redemption: the path from "an organisation named my address" to "I
// hold a seat".
//
// The three redeeming endpoints are methods on AuthController rather than a
// controller of their own, because they live under /auth and chi mounts one
// handler per prefix. That is not merely mechanical — they mint sessions,
// which is what /auth is for.
//
// The public organisation surface — the picker, and onboarding — is
// OrganizationsController, in onboarding.go. It mints nothing.

// --- shapes ------------------------------------------------------------------

type startRedemptionRequest struct {
	OrganizationSlug string `json:"org_slug"`
	Email            string `json:"email"`
}

// completeRedemptionRequest carries NO username and NO role.
//
// Both come from the roster entry the token resolves to. A redeemer who could
// name themselves could take a colleague's name, or ask for owner and then
// roster further people from it (ADR-0016 §3).
type completeRedemptionRequest struct {
	Token       string `json:"token"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

type resendVerificationRequest struct {
	Email string `json:"email"`
}

// --- handlers ----------------------------------------------------------------

// startRedemption sends a proof of address if the address is rostered.
//
// It answers 202 whether or not anything was sent. A roster miss, an unknown
// organisation and an unverified one are indistinguishable on purpose:
// answering differently would turn this into an oracle for who an organisation
// is hiring (ADR-0016 §3).
//
// ADR-0016 §7 tabulates "proof sent, or 401". The two cannot both hold — a 401
// for a miss beside a 202 for a hit IS the oracle §3 forbids — so the silence
// wins here and the discrepancy goes to the Planner rather than being settled
// in a handler.
func (c *AuthController) startRedemption(w http.ResponseWriter, r *http.Request) {
	var body startRedemptionRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidRegistration)
		return
	}

	if err := c.redemption.StartRedemption(r.Context(), body.OrganizationSlug, body.Email); err != nil {
		if errors.Is(err, service.ErrInvalid) {
			// A missing field, not a missing entry: the caller sent no
			// organisation or no address, which is a malformed request and
			// discloses nothing when reported as one.
			writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidRegistration)
			return
		}
		writeError(w, err)
		return
	}

	// 202, not 200: a proof may be on its way, and nothing here promises an
	// account will follow.
	w.WriteHeader(http.StatusAccepted)
}

// completeRedemption consumes the proof and creates the seat, signed in.
//
// 201 with a token pair, unlike registration, which returns no credential:
// registration queues a review, whereas a redeemed entry was already approved
// by the organisation that wrote it. There is nothing left to wait for.
func (c *AuthController) completeRedemption(w http.ResponseWriter, r *http.Request) {
	var body completeRedemptionRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidRegistration)
		return
	}

	hirer, pair, err := c.redemption.CompleteRedemption(r.Context(),
		body.Token, body.DisplayName, body.Password)
	if err != nil {
		writeRedemptionFailure(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, hirerAuthResponse{
		Hirer:     summarizeHirer(hirer),
		tokenPair: pairOf(pair),
	})
}

// resendVerification re-sends an outstanding proof.
//
// 202 and silent, exactly like startRedemption: an address with no proof
// outstanding must not be distinguishable from one that has a link in flight.
func (c *AuthController) resendVerification(w http.ResponseWriter, r *http.Request) {
	var body resendVerificationRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidRegistration)
		return
	}

	if err := c.redemption.ResendVerification(r.Context(), body.Email); err != nil {
		if errors.Is(err, service.ErrInvalid) {
			writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidRegistration)
			return
		}
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// writeRedemptionFailure separates "never existed" from "no longer usable".
//
// A token that never existed is 401 and says no more, so nobody can probe for
// live proofs. A token that WAS valid and is spent or expired is 410 Gone,
// because its holder is entitled to know which happened: the remedies differ —
// sign in, or ask for another link — and a bare 401 would send someone who
// already has an account round the loop again.
func writeRedemptionFailure(w http.ResponseWriter, err error) {
	switch code := service.CodeOf(err); code {
	case service.CodeVerificationSpent, service.CodeVerificationExpired:
		writeCode(w, http.StatusGone, code)
		return
	case service.CodeVerificationRequired:
		writeCode(w, http.StatusUnauthorized, code)
		return
	}

	// The entry was redeemed or removed between minting the proof and using
	// it. Reported as spent: from the holder's side that is what happened —
	// the link no longer buys a seat, and signing in is the next thing to try.
	if errors.Is(err, service.ErrConflict) {
		writeCode(w, http.StatusGone, service.CodeVerificationSpent)
		return
	}
	writeError(w, err)
}
