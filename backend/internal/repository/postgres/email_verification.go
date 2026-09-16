package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// EmailVerificationRepository owns outstanding proofs of an address.
//
// Only the HASH is stored, exactly as sessions store a refresh token: the
// plaintext reaches the recipient's inbox and nowhere else, so neither a
// database read nor a backup yields anything redeemable (ADR-0016 §4).
type EmailVerificationRepository struct{ db *DB }

// EmailVerifications returns the proof repository.
func (db *DB) EmailVerifications() *EmailVerificationRepository {
	return &EmailVerificationRepository{db: db}
}

var _ port.EmailVerificationRepository = (*EmailVerificationRepository)(nil)

const verificationColumns = `
	id, email, purpose, roster_id, onboarding_id, consumed_at, expires_at, created_at`

// scanVerification reads one row of verificationColumns, in its order.
//
// One function rather than three call sites listing the same destinations.
// The bug that cost this: onboarding_id was added to the struct and to the
// schema and to nothing here, so every onboarding proof violated
// ck_verification_subject and POST /organizations was a 500 for everybody —
// while `check`, `db-test` and `e2e` all stayed green, because the service
// tests mock this layer and the repository tests never crossed the seam.
func scanVerification(row rowScanner) (*port.EmailVerification, error) {
	var v port.EmailVerification
	if err := row.Scan(&v.ID, &v.Email, &v.Purpose, &v.RosterID, &v.OnboardingID,
		&v.ConsumedAt, &v.ExpiresAt, &v.CreatedAt); err != nil {
		return nil, err
	}
	return &v, nil
}

// Create records a proof and returns the stored row.
func (r *EmailVerificationRepository) Create(ctx context.Context, t port.Tx, v *port.EmailVerification, tokenHash []byte) (*port.EmailVerification, error) {
	id := v.ID
	if id == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generating verification id: %w", err)
		}
		id = domain.EmailVerificationID(generated.String())
	}

	var rosterID *string
	if v.RosterID != nil {
		s := string(*v.RosterID)
		rosterID = &s
	}
	var onboardingID *string
	if v.OnboardingID != nil {
		s := string(*v.OnboardingID)
		onboardingID = &s
	}

	out, err := scanVerification(r.db.q(t).QueryRow(ctx, `
		INSERT INTO email_verifications
		    (id, email, purpose, roster_id, onboarding_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING`+verificationColumns,
		string(id), v.Email, string(v.Purpose), rosterID, onboardingID,
		tokenHash, v.ExpiresAt))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("creating a %s proof for %q", v.Purpose, v.Email))
	}
	return out, nil
}

// ByTokenHash finds the proof a caller is presenting.
//
// Consumed and expired rows are RETURNED rather than filtered out. The service
// distinguishes "already used" from "never existed" — they are different things
// to be told, and a caller holding a token they just used deserves the first
// answer rather than a bare refusal.
func (r *EmailVerificationRepository) ByTokenHash(ctx context.Context, t port.Tx, hash []byte) (*port.EmailVerification, error) {
	v, err := scanVerification(r.db.q(t).QueryRow(ctx,
		`SELECT`+verificationColumns+` FROM email_verifications WHERE token_hash = $1`, hash))
	if err != nil {
		return nil, translate(err, "resolving a verification token")
	}
	return v, nil
}

// Outstanding finds a live proof for an address, so a resend re-sends rather
// than minting a second redeemable token.
//
// Most recent first: if two exist through some earlier race, the newest is the
// one the recipient was last told about.
func (r *EmailVerificationRepository) Outstanding(ctx context.Context, email string) (*port.EmailVerification, error) {
	// $2, not SQL now(). expires_at is written from the controllable clock
	// (ADR-0012), so under a pinned time the database's wall clock makes a
	// proof minted a second ago look expired — and resend then finds nothing
	// to re-send, for the one person who needs it most.
	v, err := scanVerification(r.db.pool.QueryRow(ctx,
		`SELECT`+verificationColumns+`
		   FROM email_verifications
		  WHERE email = $1
		    AND consumed_at IS NULL AND expires_at > $2
		  ORDER BY created_at DESC
		  LIMIT 1`, email, r.db.now()))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("outstanding proof for %q", email))
	}
	return v, nil
}

// Consume marks a proof spent.
//
// The predicate carries the single-use rule rather than a prior read: two
// callers presenting the same token race on this UPDATE, and exactly one wins.
func (r *EmailVerificationRepository) Consume(ctx context.Context, t port.Tx, id domain.EmailVerificationID, now time.Time) error {
	tag, err := r.db.q(t).Exec(ctx,
		`UPDATE email_verifications
		    SET consumed_at = $2
		  WHERE id = $1 AND consumed_at IS NULL`, string(id), now)
	if err != nil {
		return translate(err, fmt.Sprintf("consuming proof %s", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("proof %s was already consumed: %w", id, port.ErrConflict)
	}
	return nil
}
