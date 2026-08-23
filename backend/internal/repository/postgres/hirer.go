package postgres

import (
	"context"
	"fmt"

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
	h.id, h.organization_id, h.display_name, h.email, h.auth_provider,
	h.verified_at, coalesce(m.role, 'member'), i.github_user_id,
	o.id, o.name, o.slug, o.website, o.linkedin_url, o.verified_at, o.payment_verified_at`

const hirerFrom = `
	FROM hirer_accounts h
	LEFT JOIN organization_members m
	       ON m.hirer_account_id = h.id AND m.organization_id = h.organization_id
	LEFT JOIN user_github_identities i ON i.hirer_account_id = h.id
	LEFT JOIN organizations o ON o.id = h.organization_id`

// ByID reads one seat.
func (r *HirerRepository) ByID(ctx context.Context, id domain.HirerID) (*domain.Hirer, error) {
	h, err := scanHirer(r.db.pool.QueryRow(ctx,
		`SELECT`+hirerColumns+hirerFrom+` WHERE h.id = $1`, string(id)))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("hirer %s", id))
	}
	return h, nil
}

// ByEmail resolves the sign-in identifier.
//
// email is citext, so the comparison is case-insensitive in the database
// rather than by lowering here — which would be a second rule to keep in sync
// with the unique constraint.
func (r *HirerRepository) ByEmail(ctx context.Context, email string) (*domain.Hirer, error) {
	h, err := scanHirer(r.db.pool.QueryRow(ctx,
		`SELECT`+hirerColumns+hirerFrom+` WHERE h.email = $1`, email))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("hirer %q", email))
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
func (r *HirerRepository) Register(ctx context.Context, t port.Tx, h *domain.Hirer, org *domain.Organization, passwordHash []byte) (*domain.Hirer, error) {
	q := r.db.q(t)

	orgID := org.ID
	if orgID == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generating organization id: %w", err)
		}
		orgID = domain.OrganizationID(generated.String())
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO organizations (id, name, slug, website, linkedin_url)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''))`,
		string(orgID), org.Name, org.Slug, org.Website, org.LinkedInURL); err != nil {
		return nil, translate(err, fmt.Sprintf("creating organization %q", org.Slug))
	}

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
	if _, err := q.Exec(ctx, `
		INSERT INTO hirer_accounts
		    (id, organization_id, email, auth_provider, display_name, password_hash)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		string(hirerID), string(orgID), h.Email, string(h.AuthProvider), h.DisplayName, hash); err != nil {
		return nil, translate(err, fmt.Sprintf("creating hirer %q", h.Email))
	}

	// The registrant owns the org they just created.
	if _, err := q.Exec(ctx, `
		INSERT INTO organization_members (organization_id, hirer_account_id, role)
		VALUES ($1, $2, 'owner')`, string(orgID), string(hirerID)); err != nil {
		return nil, translate(err, "creating the owner membership")
	}

	if _, err := q.Exec(ctx, `
		INSERT INTO verification_requests (id, organization_id, status)
		VALUES (gen_random_uuid(), $1, 'pending')`, string(orgID)); err != nil {
		return nil, translate(err, "creating the verification request")
	}

	created := *h
	created.ID = hirerID
	created.OrganizationID = orgID
	created.OrgRole = domain.RoleOwner
	return &created, nil
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
	if err := row.Scan(&h.ID, &orgID, &h.DisplayName, &h.Email, &h.AuthProvider,
		&h.VerifiedAt, &h.OrgRole, &h.GitHubUserID,
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
