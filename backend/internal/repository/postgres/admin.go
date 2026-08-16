package postgres

import (
	"context"
	"fmt"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// AdminRepository owns administrator accounts and the verification queue.
//
// Admins authenticate through their own endpoint only. There is deliberately
// no OAuth path to an admin session (ADR-0002): an admin decides hirer
// verification, and an account mintable by whoever controls an identity
// provider would make that decision worth nothing.
type AdminRepository struct{ db *DB }

// Admins returns the administrator repository.
func (db *DB) Admins() *AdminRepository { return &AdminRepository{db: db} }

var _ port.AdminRepository = (*AdminRepository)(nil)

// ByEmail resolves an administrator for sign-in.
//
// A disabled account is not found. Returning it and leaving the caller to
// check would make disablement a rule every call site has to remember.
func (r *AdminRepository) ByEmail(ctx context.Context, email string) (*domain.Admin, error) {
	var a domain.Admin
	err := r.db.pool.QueryRow(ctx,
		`SELECT id, display_name, email, disabled_at
		 FROM admin_accounts WHERE email = $1 AND disabled_at IS NULL`, email,
	).Scan(&a.ID, &a.DisplayName, &a.Email, &a.DisabledAt)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("admin %q", email))
	}
	return &a, nil
}

// PasswordHash reads the stored hash.
//
// Separate from ByEmail so a credential is fetched only when a password is
// actually being checked, rather than riding along on every read.
func (r *AdminRepository) PasswordHash(ctx context.Context, id domain.AdminID) ([]byte, error) {
	var hash []byte
	err := r.db.pool.QueryRow(ctx,
		`SELECT password_hash FROM admin_accounts WHERE id = $1 AND disabled_at IS NULL`,
		string(id)).Scan(&hash)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("password for admin %s", id))
	}
	return hash, nil
}

// PendingVerifications drains the queue, oldest first.
func (r *AdminRepository) PendingVerifications(ctx context.Context) ([]port.VerificationRequest, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT id, hirer_account_id, organization_id, status, created_at, reviewed_at
		FROM verification_requests
		WHERE status = 'pending'
		ORDER BY created_at`)
	if err != nil {
		return nil, translate(err, "listing pending verifications")
	}
	defer rows.Close()

	var out []port.VerificationRequest
	for rows.Next() {
		var v port.VerificationRequest
		if err := rows.Scan(&v.ID, &v.HirerID, &v.OrganizationID, &v.Status,
			&v.CreatedAt, &v.ReviewedAt); err != nil {
			return nil, translate(err, "scanning verification request")
		}
		out = append(out, v)
	}
	return out, translate(rows.Err(), "listing pending verifications")
}

// DecideVerification approves or rejects, and on approval stamps the subject.
//
// The stamp goes on whichever subject the request names —
// ck_verification_single_subject guarantees exactly one — so approving an
// organization-scoped request lifts every seat at once, which is what
// ADR-0008 §3a depends on.
//
// A decision is final: the WHERE requires status='pending', so re-deciding
// affects no row and reports a conflict.
func (r *AdminRepository) DecideVerification(ctx context.Context, t port.Tx, id domain.RequestID, by domain.AdminID, approve bool, reason string) error {
	q := r.db.q(t)

	status := "rejected"
	if approve {
		status = "approved"
	}

	var (
		hirerID *string
		orgID   *string
	)
	err := q.QueryRow(ctx, `
		UPDATE verification_requests
		SET status = $2::verification_status, reviewed_by = $3, reviewed_at = now(),
		    decision_reason = NULLIF($4, ''), updated_at = now()
		WHERE id = $1 AND status = 'pending'
		RETURNING hirer_account_id, organization_id`,
		string(id), status, string(by), reason).Scan(&hirerID, &orgID)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("verification request %s is not pending: %w", id, port.ErrConflict)
		}
		return translate(err, fmt.Sprintf("deciding verification %s", id))
	}
	if !approve {
		return nil
	}

	switch {
	case orgID != nil:
		if _, err := q.Exec(ctx, `
			UPDATE organizations SET verified_at = now(), verified_by = $2, updated_at = now()
			WHERE id = $1 AND verified_at IS NULL`, *orgID, string(by)); err != nil {
			return translate(err, "verifying the organization")
		}
	case hirerID != nil:
		if _, err := q.Exec(ctx, `
			UPDATE hirer_accounts SET verified_at = now(), verified_by = $2, updated_at = now()
			WHERE id = $1 AND verified_at IS NULL`, *hirerID, string(by)); err != nil {
			return translate(err, "verifying the hirer")
		}
	}
	return nil
}

// SeedAdmin creates the first administrator, and only the first.
//
// The INSERT ... SELECT ... WHERE NOT EXISTS is one statement, so two servers
// booting simultaneously cannot both decide the table is empty. Reports
// whether it created one, so a caller can log that it happened — a silent
// admin appearing is worse than a noisy one.
func (r *AdminRepository) SeedAdmin(ctx context.Context, email string, passwordHash []byte) (bool, error) {
	tag, err := r.db.pool.Exec(ctx, `
		INSERT INTO admin_accounts (id, email, password_hash, display_name)
		SELECT gen_random_uuid(), $1, $2, 'Seed Admin'
		WHERE NOT EXISTS (SELECT 1 FROM admin_accounts)`, email, passwordHash)
	if err != nil {
		return false, translate(err, "seeding the first admin")
	}
	return tag.RowsAffected() == 1, nil
}
