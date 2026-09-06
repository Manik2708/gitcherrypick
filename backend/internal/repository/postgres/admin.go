package postgres

import (
	"context"
	"fmt"
	"time"

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

// ByID resolves an admin from an access token's subject.
//
// disabled_at is SELECTED rather than filtered on, unlike ByEmail: sign-in must
// not reveal that a disabled account exists, but a caller already holding a
// valid token for one is entitled to be told their access was withdrawn. The
// service decides; this returns the fact.
func (r *AdminRepository) ByID(ctx context.Context, id domain.AdminID) (*domain.Admin, error) {
	var a domain.Admin
	err := r.db.pool.QueryRow(ctx,
		`SELECT id, display_name, email, disabled_at
		 FROM admin_accounts WHERE id = $1::uuid`, string(id),
	).Scan(&a.ID, &a.DisplayName, &a.Email, &a.DisabledAt)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("admin %s", id))
	}
	return &a, nil
}

// ByEmail resolves an administrator for sign-in.
//
// A disabled account is NOT FOUND here, unlike in ByID. Sign-in must not reveal
// that a disabled account exists, while a caller already holding a valid token
// for one is entitled to learn their access was withdrawn.
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
	// LEFT JOINs on both subjects: a request names a hirer OR an organization,
	// never both, so one side is always null.
	rows, err := r.db.pool.Query(ctx, `
		SELECT v.id, v.status, v.created_at, v.reviewed_at,
		       coalesce(h.id, seat.id), coalesce(h.display_name, seat.display_name),
		       coalesce(h.email, seat.email),
		       coalesce(o.id, hirer_org.id), coalesce(o.name, hirer_org.name)
		FROM verification_requests v
		LEFT JOIN hirer_accounts h ON h.id = v.hirer_account_id
		LEFT JOIN organizations  o ON o.id = v.organization_id
		LEFT JOIN organizations  hirer_org ON hirer_org.id = h.organization_id
		-- The seat that asked. A request names an organization OR a hirer,
		-- never both, but an admin deciding either one is looking at a person
		-- and a company together — so the other side is resolved rather than
		-- left for the reader to go and find.
		LEFT JOIN LATERAL (
		    SELECT ha.id, ha.display_name, ha.email
		    FROM organization_members om
		    JOIN hirer_accounts ha ON ha.id = om.hirer_account_id
		    WHERE om.organization_id = o.id
		    ORDER BY om.role = 'owner' DESC, ha.created_at
		    LIMIT 1
		) seat ON true
		WHERE v.status = 'pending'
		ORDER BY v.created_at`)
	if err != nil {
		return nil, translate(err, "listing pending verifications")
	}
	defer rows.Close()

	var out []port.VerificationRequest
	for rows.Next() {
		var (
			v                  port.VerificationRequest
			hirerID, hirerName *string
			hirerEmail         *string
			orgID, orgName     *string
		)
		if err := rows.Scan(&v.ID, &v.Status, &v.CreatedAt, &v.ReviewedAt,
			&hirerID, &hirerName, &hirerEmail, &orgID, &orgName); err != nil {
			return nil, translate(err, "scanning verification request")
		}
		if hirerID != nil {
			v.Hirer = &port.VerificationHirer{
				ID: domain.HirerID(*hirerID), DisplayName: deref(hirerName), Email: deref(hirerEmail),
			}
		}
		if orgID != nil {
			v.Organization = &port.VerificationOrganization{
				ID: domain.OrganizationID(*orgID), Name: deref(orgName),
			}
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err, "listing pending verifications")
	}
	return out, r.attachProofs(ctx, out)
}

// attachProofs fills in the evidence for a page of requests.
//
// One query for the whole page rather than one per row: the queue is read on
// every admin page load, and a per-row fetch would make its cost grow with the
// backlog.
func (r *AdminRepository) attachProofs(ctx context.Context, requests []port.VerificationRequest) error {
	if len(requests) == 0 {
		return nil
	}

	ids := make([]string, 0, len(requests))
	for _, v := range requests {
		ids = append(ids, string(v.ID))
	}

	rows, err := r.db.pool.Query(ctx, `
		SELECT request_id, kind, coalesce(value, ''), coalesce(notes, ''),
		       coalesce(attachment_url, '')
		FROM verification_proofs
		WHERE request_id = ANY($1)
		ORDER BY request_id, created_at`, ids)
	if err != nil {
		return translate(err, "reading verification proofs")
	}
	defer rows.Close()

	byRequest := map[string][]domain.VerificationProof{}
	for rows.Next() {
		var requestID string
		var proof domain.VerificationProof
		if err := rows.Scan(&requestID, &proof.Kind, &proof.Value,
			&proof.Notes, &proof.AttachmentURL); err != nil {
			return translate(err, "scanning verification proof")
		}
		byRequest[requestID] = append(byRequest[requestID], proof)
	}
	if err := rows.Err(); err != nil {
		return translate(err, "reading verification proofs")
	}

	for i := range requests {
		// An empty slice rather than nil: "no proofs were attached" is a fact
		// the admin needs, and it serializes as [] rather than vanishing.
		proofs := byRequest[string(requests[i].ID)]
		if proofs == nil {
			proofs = []domain.VerificationProof{}
		}
		requests[i].Proofs = proofs
	}
	return nil
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
func (r *AdminRepository) DecideVerification(ctx context.Context, t port.Tx, id domain.RequestID, by domain.AdminID, d domain.VerificationDecision) error {
	q := r.db.q(t)

	status := "rejected"
	if d.Approve {
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
		string(id), status, string(by), d.Reason).Scan(&hirerID, &orgID)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("verification request %s is not pending: %w", id, port.ErrConflict)
		}
		return translate(err, fmt.Sprintf("deciding verification %s", id))
	}
	if !d.Approve {
		return nil
	}

	// NULL rather than false when payment was not established. The column
	// records WHEN it was verified, and a stamp of "never" is a null.
	var paymentAt *time.Time
	if d.PaymentVerified {
		at := r.db.now()
		paymentAt = &at
	}

	switch {
	case orgID != nil:
		if _, err := q.Exec(ctx, `
			UPDATE organizations
			SET verified_at = now(), verified_by = $2,
			    payment_verified_at = coalesce(payment_verified_at, $3),
			    updated_at = now()
			WHERE id = $1 AND verified_at IS NULL`, *orgID, string(by), paymentAt); err != nil {
			return translate(err, "verifying the organization")
		}
		// Approving an organization lifts every seat in it (ADR-0008 §3a).
		if _, err := q.Exec(ctx, `
			UPDATE hirer_accounts
			SET verified_at = now(), verified_by = $2,
			    payment_verified_at = coalesce(payment_verified_at, $3),
			    updated_at = now()
			WHERE organization_id = $1 AND verified_at IS NULL`,
			*orgID, string(by), paymentAt); err != nil {
			return translate(err, "verifying the organization's seats")
		}
	case hirerID != nil:
		if _, err := q.Exec(ctx, `
			UPDATE hirer_accounts
			SET verified_at = now(), verified_by = $2,
			    payment_verified_at = coalesce(payment_verified_at, $3),
			    updated_at = now()
			WHERE id = $1 AND verified_at IS NULL`, *hirerID, string(by), paymentAt); err != nil {
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
