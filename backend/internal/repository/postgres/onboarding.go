package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// OnboardingRepository owns submitted organisations — companies that have been
// described but do not exist yet (ADR-0017).
//
// Nothing here writes an `organizations` row except Promote, and Promote runs
// only when an administrator approves. That is the whole point: the submission
// form is public, so anything it could create directly, anyone could create.
type OnboardingRepository struct{ db *DB }

// Onboarding returns the submission repository.
func (db *DB) Onboarding() *OnboardingRepository { return &OnboardingRepository{db: db} }

var _ port.OnboardingRepository = (*OnboardingRepository)(nil)

const onboardingColumns = `
	id, name, coalesce(description, ''), email, coalesce(phone, ''),
	-- ::text, because the column is a nullable enum and "not given" has to
	-- read as the empty band rather than as a NULL nothing can scan.
	coalesce(headcount::text, ''),
	coalesce(country, ''), coalesce(city, ''), coalesce(postal_code, ''),
	coalesce(street1, ''), coalesce(street2, ''),
	coalesce(owner_username, ''), coalesce(owner_display_name, ''),
	email_verified_at, decided_at, decided_by, coalesce(decision_reason, ''),
	organization_id, supersedes_id, created_at`

// scanOnboarding reads one row of onboardingColumns, in its order.
func scanOnboarding(row rowScanner) (*port.OnboardingSubmission, error) {
	var s port.OnboardingSubmission
	if err := row.Scan(
		&s.ID, &s.Name, &s.Description, &s.Email, &s.Phone, &s.Headcount,
		&s.Country, &s.City, &s.PostalCode, &s.Street1, &s.Street2,
		&s.OwnerUsername, &s.OwnerDisplayName,
		&s.EmailVerifiedAt, &s.DecidedAt, &s.DecidedBy, &s.DecisionReason,
		&s.OrganizationID, &s.SupersedesID, &s.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &s, nil
}

// Submit records a form and creates nothing else.
//
// No slug is derived and none is reserved. Two submissions may name the same
// company; whichever an administrator approves first gets it (ADR-0017 §2).
func (r *OnboardingRepository) Submit(ctx context.Context, t port.Tx, in *port.OnboardingSubmission) (*port.OnboardingSubmission, error) {
	id := in.ID
	if id == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generating onboarding id: %w", err)
		}
		id = domain.OnboardingID(generated.String())
	}

	var headcount *string
	if in.Headcount != "" {
		v := string(in.Headcount)
		headcount = &v
	}

	out, err := scanOnboarding(r.db.q(t).QueryRow(ctx, `
		INSERT INTO organization_onboarding
		    (id, name, description, email, phone, headcount,
		     country, city, postal_code, street1, street2, supersedes_id)
		VALUES ($1, $2, NULLIF($3, ''), $4, NULLIF($5, ''), $6::organization_headcount_band,
		        NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''),
		        NULLIF($10, ''), NULLIF($11, ''), $12)
		RETURNING`+onboardingColumns,
		string(id), in.Name, in.Description, in.Email, in.Phone, headcount,
		in.Country, in.City, in.PostalCode, in.Street1, in.Street2,
		idOrNil(in.SupersedesID)))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("submitting onboarding for %q", in.Name))
	}
	return out, nil
}

// ByID reads one submission, decided or not.
func (r *OnboardingRepository) ByID(ctx context.Context, t port.Tx, id domain.OnboardingID) (*port.OnboardingSubmission, error) {
	out, err := scanOnboarding(r.db.q(t).QueryRow(ctx,
		`SELECT`+onboardingColumns+` FROM organization_onboarding WHERE id = $1`, string(id)))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("onboarding submission %s", id))
	}
	return out, nil
}

// Prove stamps the address confirmed and records how the owner will sign in.
//
// The predicate carries the single-use rule rather than a prior read: two
// callers presenting the same code race on this UPDATE and exactly one wins.
func (r *OnboardingRepository) Prove(ctx context.Context, t port.Tx, id domain.OnboardingID, owner port.OnboardingOwner, at time.Time) error {
	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE organization_onboarding
		   SET owner_username      = $2,
		       owner_display_name  = $3,
		       owner_password_hash = $4,
		       email_verified_at   = $5
		 WHERE id = $1 AND email_verified_at IS NULL AND decided_at IS NULL`,
		string(id), owner.Username, owner.DisplayName, owner.PasswordHash, at)
	if err != nil {
		return translate(err, fmt.Sprintf("proving onboarding submission %s", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("submission %s is already proven or decided: %w", id, port.ErrConflict)
	}
	return nil
}

// Queue returns proven, undecided submissions, oldest first.
//
// An unproven submission is not merely filtered out here — the schema refuses
// to let one be decided at all (ck_onboarding_decided_after_verified). This
// query and that constraint say the same thing, and the constraint is the one
// that holds when someone writes a second query.
func (r *OnboardingRepository) Queue(ctx context.Context) ([]port.OnboardingSubmission, error) {
	rows, err := r.db.pool.Query(ctx,
		`SELECT`+onboardingColumns+`
		   FROM organization_onboarding
		  WHERE email_verified_at IS NOT NULL AND decided_at IS NULL
		  ORDER BY created_at`)
	if err != nil {
		return nil, translate(err, "reading the onboarding queue")
	}
	defer rows.Close()

	// Never nil: an empty queue must serialize as [] rather than null.
	out := make([]port.OnboardingSubmission, 0)
	for rows.Next() {
		s, err := scanOnboarding(rows)
		if err != nil {
			return nil, translate(err, "scanning an onboarding submission")
		}
		out = append(out, *s)
	}
	return out, translate(rows.Err(), "reading the onboarding queue")
}

// Promote creates the organisation, its address, the owner seat and the
// membership, and stamps the submission decided — in one transaction.
//
// Ordered so that every foreign key resolves when it is written: the username
// is claimed, then the organisation, then the seat that references both, then
// the address, then the membership. A half-promoted submission would be an
// organisation nobody owns.
func (r *OnboardingRepository) Promote(ctx context.Context, t port.Tx, id domain.OnboardingID, slug string, by domain.AdminID, at time.Time) (*domain.Organization, error) {
	q := r.db.q(t)

	// FOR UPDATE, not a bare read: two administrators deciding the same
	// submission would otherwise both promote it.
	sub, err := scanOnboarding(q.QueryRow(ctx,
		`SELECT`+onboardingColumns+`
		   FROM organization_onboarding
		  WHERE id = $1 AND email_verified_at IS NOT NULL AND decided_at IS NULL
		    FOR UPDATE`, string(id)))
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("submission %s is unproven or already decided: %w",
				id, port.ErrConflict)
		}
		return nil, translate(err, fmt.Sprintf("reading submission %s", id))
	}

	var hash []byte
	if err := q.QueryRow(ctx,
		`SELECT owner_password_hash FROM organization_onboarding WHERE id = $1`,
		string(id)).Scan(&hash); err != nil {
		return nil, translate(err, "reading the owner credential")
	}

	orgID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating organization id: %w", err)
	}
	hirerID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating hirer id: %w", err)
	}

	// The organisation. A taken slug is what makes approval refusable: two
	// submissions may name the same company, and only the first approved can
	// have it (ADR-0017 §Approval can fail).
	// verified_at is stamped HERE, by the approval that creates the row.
	//
	// The administrator's decision IS the verification — there is no second
	// review, and an organisation created unverified would be a company that
	// exists and cannot hire, waiting on an approval that already happened
	// (ADR-0017 §2).
	var org domain.Organization
	err = q.QueryRow(ctx, `
		INSERT INTO organizations
		    (id, name, slug, description, email, phone, headcount,
		     verified_at, verified_by)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''),
		        $7::organization_headcount_band, $8, $9)
		RETURNING id, name, slug, coalesce(description, ''), coalesce(email, ''),
		          coalesce(phone, ''), coalesce(headcount::text, ''),
		          verified_at, payment_verified_at`,
		orgID.String(), sub.Name, slug, sub.Description,
		sub.Email, sub.Phone, nullableBand(sub.Headcount), at, string(by),
	).Scan(&org.ID, &org.Name, &org.Slug, &org.Description, &org.Email,
		&org.Phone, &org.Headcount, &org.VerifiedAt, &org.PaymentVerifiedAt)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("creating organization %q", sub.Name))
	}

	// The name, claimed out of the global namespace. Already reserved at
	// verification (ADR-0017 §Identity), so this is the row the seat points at
	// rather than a new claim.
	if _, err := q.Exec(ctx,
		`INSERT INTO hirer_usernames (username) VALUES ($1) ON CONFLICT DO NOTHING`,
		sub.OwnerUsername); err != nil {
		return nil, translate(err, "claiming the owner username")
	}

	if _, err := q.Exec(ctx, `
		INSERT INTO hirer_accounts
		    (id, organization_id, email, username, auth_provider,
		     display_name, password_hash)
		VALUES ($1, $2, $3, $4, 'email', $5, $6)`,
		hirerID.String(), org.ID, sub.Email, sub.OwnerUsername,
		sub.OwnerDisplayName, hash); err != nil {
		return nil, translate(err, "creating the owner seat")
	}

	if _, err := q.Exec(ctx, `
		INSERT INTO organization_members (organization_id, hirer_account_id, role)
		VALUES ($1, $2, 'owner')`, org.ID, hirerID.String()); err != nil {
		return nil, translate(err, "creating the owner membership")
	}

	// The address, if one was given. A fully remote company has none to give,
	// which is why ck_onboarding_address permits all-or-nothing rather than
	// requiring it.
	if sub.Country != "" {
		addressID, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generating address id: %w", err)
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO organization_addresses
			    (id, organization_id, country, city, postal_code, street1, street2,
			     is_main_office)
			VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, NULLIF($7, ''), true)`,
			addressID.String(), org.ID, sub.Country, sub.City, sub.PostalCode,
			sub.Street1, sub.Street2); err != nil {
			return nil, translate(err, "creating the main office address")
		}
	}

	if _, err := q.Exec(ctx, `
		UPDATE organization_onboarding
		   SET decided_at = $2, decided_by = $3, organization_id = $4
		 WHERE id = $1`, string(id), at, string(by), org.ID); err != nil {
		return nil, translate(err, fmt.Sprintf("stamping submission %s approved", id))
	}
	return &org, nil
}

// Reject stamps a submission decided and creates nothing.
//
// The row survives: it is the record of what was claimed and why it was
// refused, and a company revising needs it to point at (ADR-0017 §8).
func (r *OnboardingRepository) Reject(ctx context.Context, t port.Tx, id domain.OnboardingID, by domain.AdminID, reason string, at time.Time) error {
	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE organization_onboarding
		   SET decided_at = $2, decided_by = $3, decision_reason = NULLIF($4, '')
		 WHERE id = $1 AND email_verified_at IS NOT NULL AND decided_at IS NULL`,
		string(id), at, string(by), reason)
	if err != nil {
		return translate(err, fmt.Sprintf("rejecting submission %s", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("submission %s is unproven or already decided: %w", id, port.ErrConflict)
	}
	return nil
}

// ExpireUnproven deletes submissions whose code lapsed without ever being used.
//
// Deleting rather than archiving is deliberate (ADR-0017 §2). It loses the
// trail — somebody submitting one name ten times and never verifying leaves
// nothing — but abuse of an endpoint that creates nothing belongs to rate
// limiting, not to a table nobody prunes.
func (r *OnboardingRepository) ExpireUnproven(ctx context.Context, before time.Time) (int, error) {
	tag, err := r.db.pool.Exec(ctx, `
		DELETE FROM organization_onboarding
		 WHERE email_verified_at IS NULL
		   AND decided_at IS NULL
		   AND created_at < $1`, before)
	if err != nil {
		return 0, translate(err, "expiring unproven onboarding submissions")
	}
	return int(tag.RowsAffected()), nil
}

// idOrNil renders an optional id for a nullable column.
func idOrNil(id *domain.OnboardingID) *string {
	if id == nil {
		return nil
	}
	s := string(*id)
	return &s
}

// nullableBand renders an unset band as NULL rather than as the empty string,
// which is not a member of the enum.
func nullableBand(b domain.HeadcountBand) *string {
	if b == "" {
		return nil
	}
	s := string(b)
	return &s
}
