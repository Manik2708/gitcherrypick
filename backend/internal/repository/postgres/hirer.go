package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// HirerRepository owns recruiter seats and the organizations they belong to.
//
// Hiring capability is a property of the ORGANIZATION, not the person:
// verifying one lifts every seat, and an invited member inherits whatever the
// org already has (ADR-0002, ADR-0008 §3a). That is why every capability read
// here joins the organization rather than trusting the seat's own column.
type HirerRepository struct{ db *DB }

// Hirers returns the recruiter repository.
func (db *DB) Hirers() *HirerRepository { return &HirerRepository{db: db} }

var _ port.HirerRepository = (*HirerRepository)(nil)

const hirerColumns = `
	h.id, h.organization_id, h.display_name, h.username, h.email, h.auth_provider,
	h.verified_at, h.disabled_at, coalesce(m.role, 'member'), i.github_user_id,
	o.id, o.name, o.slug, o.website, o.linkedin_url, o.verified_at, o.payment_verified_at`

const hirerFrom = `
	FROM hirer_accounts h
	LEFT JOIN organization_members m
	       ON m.hirer_account_id = h.id AND m.organization_id = h.organization_id
	LEFT JOIN user_github_identities i ON i.hirer_account_id = h.id
	LEFT JOIN organizations o ON o.id = h.organization_id`

// ByID reads one seat.
//
// disabled_at is SELECTED rather than filtered on, unlike ByUsername: sign-in
// must not reveal that a revoked seat exists, but a caller already holding a
// valid token for one is entitled to be told their access was withdrawn. The
// service decides; this returns the fact. Same rule as admin_accounts.
func (r *HirerRepository) ByID(ctx context.Context, id domain.HirerID) (*domain.Hirer, error) {
	h, err := scanHirer(r.db.pool.QueryRow(ctx,
		`SELECT`+hirerColumns+hirerFrom+` WHERE h.id = $1`, string(id)))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("hirer %s", id))
	}
	return h, nil
}

// ByUsername resolves the sign-in identifier (ADR-0016).
//
// A disabled seat is NOT FOUND here. Sign-in must not distinguish a revoked
// account from one that never existed, or the endpoint becomes an oracle over
// who used to work somewhere.
//
// username is citext, so the comparison is case-insensitive in the database
// rather than by lowering here — which would be a second rule to keep in sync
// with the unique constraint.
func (r *HirerRepository) ByUsername(ctx context.Context, username string) (*domain.Hirer, error) {
	h, err := scanHirer(r.db.pool.QueryRow(ctx,
		`SELECT`+hirerColumns+hirerFrom+` WHERE h.username = $1 AND h.disabled_at IS NULL`, username))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("hirer %q", username))
	}
	return h, nil
}

// ByEmail finds a seat by its contact address.
//
// Not an authentication path: email is a contact field since ADR-0016, and two
// seats may share one. Callers scope it themselves.
func (r *HirerRepository) ByEmail(ctx context.Context, email string) (*domain.Hirer, error) {
	h, err := scanHirer(r.db.pool.QueryRow(ctx,
		`SELECT`+hirerColumns+hirerFrom+` WHERE h.email = $1 AND h.disabled_at IS NULL`, email))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("hirer %q", email))
	}
	return h, nil
}

// ListSeats returns an organization's seats, live and revoked.
//
// Revoked ones are included deliberately: they are precisely the seats an owner
// needs in order to resolve the author of an old round (ADR-0016 §5a). Hiding
// them would make the history it protects unreadable.
func (r *HirerRepository) ListSeats(ctx context.Context, orgID domain.OrganizationID) ([]domain.Hirer, error) {
	rows, err := r.db.pool.Query(ctx,
		`SELECT`+hirerColumns+hirerFrom+`
		 WHERE h.organization_id = $1
		 ORDER BY h.disabled_at NULLS FIRST, h.created_at`, string(orgID))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("seats of org %s", orgID))
	}
	defer rows.Close()

	// Never nil: an empty list must serialize as [] rather than null.
	out := make([]domain.Hirer, 0)
	for rows.Next() {
		h, err := scanHirer(rows)
		if err != nil {
			return nil, translate(err, "scanning a seat")
		}
		out = append(out, *h)
	}
	return out, translate(rows.Err(), "reading seats")
}

// Disable revokes a seat without deleting it.
//
// Idempotent by design: disabling an already-disabled seat leaves the original
// timestamp, so the record says when access actually ended rather than when
// somebody last pressed the button.
func (r *HirerRepository) Disable(ctx context.Context, tx port.Tx, id domain.HirerID, by domain.HirerID, now time.Time) error {
	_, err := r.db.q(tx).Exec(ctx,
		`UPDATE hirer_accounts
		    SET disabled_at = $2, disabled_by = $3, updated_at = $2
		  WHERE id = $1 AND disabled_at IS NULL`,
		string(id), now, string(by))
	return translate(err, fmt.Sprintf("disabling hirer %s", id))
}

// ByGitHubUserID resolves a seat that signed up through GitHub.
//
// Scoped to rows whose owner is a HIRER. uq_github_identity_hirer_github makes
// that single-valued, so this cannot return somebody else's seat.
func (r *HirerRepository) ByGitHubUserID(ctx context.Context, githubUserID int64) (*domain.Hirer, error) {
	h, err := scanHirer(r.db.pool.QueryRow(ctx,
		`SELECT`+hirerColumns+hirerFrom+`
		 JOIN user_github_identities ghi
		   ON ghi.hirer_account_id = h.id AND ghi.github_user_id = $1`, githubUserID))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("hirer for github user %d", githubUserID))
	}
	return h, nil
}

// PasswordHash reads the stored hash for the email provider.
//
// Separate from ByEmail so a hash is fetched only when a password is actually
// being checked. A credential that rides along on every read is a credential
// that ends up in a log line.
func (r *HirerRepository) PasswordHash(ctx context.Context, id domain.HirerID) ([]byte, error) {
	var hash []byte
	err := r.db.pool.QueryRow(ctx,
		`SELECT password_hash FROM hirer_accounts WHERE id = $1 AND auth_provider = 'email'`,
		string(id)).Scan(&hash)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("password for hirer %s", id))
	}
	return hash, nil
}

// Register creates the organization, the seat, the membership and the
// verification request.
//
// All four or none. ADR-0002 requires registration to create the account and a
// pending verification request in the same transaction: an account with no
// request would never be verified by anything, and a request with no account
// is a queue item an admin cannot act on.
//
// The request names the ORGANIZATION, not the seat. ck_verification_single_subject
// permits either, and per-organization is what makes approval lift every seat
// at once.
func (r *HirerRepository) Register(ctx context.Context, t port.Tx, in port.NewHirerAccount) (*port.HirerRegistration, error) {
	q := r.db.q(t)
	h, passwordHash := in.Hirer, in.PasswordHash

	// The name is claimed out of the global namespace first, the same table a
	// roster entry draws from. Registration and rostering are the only two
	// ways a name enters the system, and they must not be able to pick the
	// same one (ADR-0016 §Identity).
	if _, err := q.Exec(ctx,
		`INSERT INTO hirer_usernames (username) VALUES ($1)`, h.Username); err != nil {
		return nil, translate(err, fmt.Sprintf("claiming the username %q", h.Username))
	}

	// No organisation. Registration creates an INDEPENDENT hirer — someone
	// hiring on their own account rather than a company's — and onboarding is
	// the only thing that creates an organisation (ADR-0017 §1).
	//
	// hirer_accounts.organization_id is nullable, so this is a seat that
	// belongs to nobody but itself.

	hirerID := h.ID
	if hirerID == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generating hirer id: %w", err)
		}
		hirerID = domain.HirerID(generated.String())
	}

	// ck_hirer_password_only_for_email: a hash exists for the email provider
	// and for no other. An OAuth account with one would mean two ways in, one
	// of which nobody set.
	var hash *[]byte
	if h.AuthProvider == domain.ProviderEmail {
		hash = &passwordHash
	}
	// username is NOT NULL and unconditionally unique (ADR-0016). A collision
	// surfaces as port.ErrConflict through translate, never as a prior check:
	// checking first is both a race and an enumeration oracle.
	if _, err := q.Exec(ctx, `
		INSERT INTO hirer_accounts
		    (id, email, username, auth_provider, display_name, password_hash)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		string(hirerID), h.Email, h.Username,
		string(h.AuthProvider), h.DisplayName, hash); err != nil {
		return nil, translate(err, fmt.Sprintf("creating hirer %q", h.Username))
	}

	// The request names the HIRER, not an organisation. There is no
	// organisation to name, and ck_verification_single_subject wants exactly
	// one of the two — which is why an independent hirer is reviewed
	// individually rather than through a company (ADR-0017 §1).
	var request port.VerificationRequest
	if err := q.QueryRow(ctx, `
		INSERT INTO verification_requests (id, hirer_account_id, status)
		VALUES (gen_random_uuid(), $1, 'pending')
		RETURNING id, status, created_at`, string(hirerID),
	).Scan(&request.ID, &request.Status, &request.CreatedAt); err != nil {
		return nil, translate(err, "creating the verification request")
	}

	for _, proof := range in.Proofs {
		if _, err := q.Exec(ctx, `
			INSERT INTO verification_proofs (id, request_id, kind, value, notes, attachment_url)
			VALUES (gen_random_uuid(), $1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''))`,
			string(request.ID), string(proof.Kind), proof.Value, proof.Notes,
			proof.AttachmentURL); err != nil {
			return nil, translate(err, fmt.Sprintf("attaching %s proof", proof.Kind))
		}
	}

	// No organisation, and therefore no org role: an independent hirer is not
	// an owner of anything, and reporting them as one would put a member-only
	// refusal in front of an account with nobody to be a member of.
	created := *h
	created.ID = hirerID
	return &port.HirerRegistration{Hirer: &created, VerificationRequest: &request}, nil
}

// Organization reads one organization.
func (r *HirerRepository) Organization(ctx context.Context, id domain.OrganizationID) (*domain.Organization, error) {
	var o domain.Organization
	err := r.db.pool.QueryRow(ctx, `
		SELECT id, name, slug, coalesce(website, ''), coalesce(linkedin_url, ''),
		       verified_at, payment_verified_at
		FROM organizations WHERE id = $1`, string(id),
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Website, &o.LinkedInURL, &o.VerifiedAt, &o.PaymentVerifiedAt)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("organization %s", id))
	}
	return &o, nil
}

// OrganizationBySlug resolves the organization a redeemer named.
//
// Verified only. An unverified organization may build a roster, but nobody may
// redeem one — that is the moment an unknown person becomes a hirer inside it
// (ADR-0016 §2). Returning the row for an unverified org would let the
// redemption path answer differently for "not verified" than for "no such
// org", which is a distinction the caller has no business learning.
func (r *HirerRepository) OrganizationBySlug(ctx context.Context, slug string) (*domain.Organization, error) {
	var o domain.Organization
	err := r.db.pool.QueryRow(ctx, `
		SELECT id, name, slug, coalesce(website, ''), coalesce(linkedin_url, ''),
		       verified_at, payment_verified_at
		FROM organizations WHERE slug = $1 AND verified_at IS NOT NULL`, slug,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Website, &o.LinkedInURL, &o.VerifiedAt, &o.PaymentVerifiedAt)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("organization %q", slug))
	}
	return &o, nil
}

// ListVerifiedOrganizations backs the public picker.
//
// Name and slug only at the service boundary; this returns the row and the
// controller narrows it. Nothing about roster size, seat count or payment
// status may leave — the list already discloses who is approved to hire, and
// that is as far as ADR-0016 §3a goes.
func (r *HirerRepository) ListVerifiedOrganizations(ctx context.Context) ([]domain.Organization, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT id, name, slug, coalesce(website, ''), coalesce(linkedin_url, ''),
		       verified_at, payment_verified_at
		FROM organizations WHERE verified_at IS NOT NULL ORDER BY name`)
	if err != nil {
		return nil, translate(err, "listing verified organizations")
	}
	defer rows.Close()

	out := make([]domain.Organization, 0)
	for rows.Next() {
		var o domain.Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.Website, &o.LinkedInURL,
			&o.VerifiedAt, &o.PaymentVerifiedAt); err != nil {
			return nil, translate(err, "scanning an organization")
		}
		out = append(out, o)
	}
	return out, translate(rows.Err(), "reading organizations")
}

// Members lists an organization's seats, owners first.
func (r *HirerRepository) Members(ctx context.Context, id domain.OrganizationID) ([]domain.Hirer, error) {
	rows, err := r.db.pool.Query(ctx,
		`SELECT`+hirerColumns+hirerFrom+`
		 WHERE h.organization_id = $1
		 ORDER BY m.role, h.created_at`, string(id))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("listing members of %s", id))
	}
	defer rows.Close()

	var out []domain.Hirer
	for rows.Next() {
		h, err := scanHirer(rows)
		if err != nil {
			return nil, translate(err, "scanning member")
		}
		out = append(out, *h)
	}
	return out, translate(rows.Err(), "listing members")
}

// SharesGitHubIdentity backs AssertNotSelf.
//
// Asked as a question rather than by fetching both identities, so the
// comparison lives in one place instead of being re-derived at each of the
// three call sites ADR-0002 names — search, scorecard read, and shortlist add.
//
// ADR-0009 makes each namespace single-valued, so this is an existence check
// rather than a set intersection.
func (r *HirerRepository) SharesGitHubIdentity(ctx context.Context, hirer domain.HirerID, target domain.UserID) (bool, error) {
	var shares bool
	err := r.db.pool.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1
		    FROM user_github_identities h
		    JOIN user_github_identities u ON u.github_user_id = h.github_user_id
		    WHERE h.hirer_account_id = $1 AND u.user_id = $2
		)`, string(hirer), string(target)).Scan(&shares)
	if err != nil {
		return false, translate(err, "checking for a shared github identity")
	}
	return shares, nil
}

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface{ Scan(dest ...any) error }

func scanHirer(row rowScanner) (*domain.Hirer, error) {
	var (
		h     domain.Hirer
		orgID *string
		org   domain.Organization

		// The organization join is LEFT, so every column of it may be null.
		// Scanning into pointers keeps a seat whose organization row is
		// missing readable rather than failing the whole query.
		orgRowID    *string
		orgName     *string
		orgSlug     *string
		orgWebsite  *string
		orgLinkedIn *string
	)
	if err := row.Scan(&h.ID, &orgID, &h.DisplayName, &h.Username, &h.Email, &h.AuthProvider,
		&h.VerifiedAt, &h.DisabledAt, &h.OrgRole, &h.GitHubUserID,
		&orgRowID, &orgName, &orgSlug, &orgWebsite, &orgLinkedIn,
		&org.VerifiedAt, &org.PaymentVerifiedAt); err != nil {
		return nil, err
	}
	if orgID != nil {
		h.OrganizationID = domain.OrganizationID(*orgID)
	}
	if orgRowID != nil {
		org.ID = domain.OrganizationID(*orgRowID)
		org.Name = deref(orgName)
		org.Slug = deref(orgSlug)
		org.Website = deref(orgWebsite)
		org.LinkedInURL = deref(orgLinkedIn)
		h.Organization = &org
	}
	return &h, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
