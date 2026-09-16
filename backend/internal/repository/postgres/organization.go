package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// OrganizationRepository owns invitations and seat grants.
//
// An organization self-administers its seats, with no admin involvement
// (ADR-0002). Requiring an admin to approve every recruiter at an already
// verified company would make the queue unworkable and buys nothing, since the
// org has already vouched for itself.
type OrganizationRepository struct{ db *DB }

// Organizations returns the seat-management repository.
func (db *DB) Organizations() *OrganizationRepository { return &OrganizationRepository{db: db} }

var _ port.OrganizationRepository = (*OrganizationRepository)(nil)

// rosterColumns resolves the AUTHOR rather than emitting their id
// (ADR-0016 §9). Always used with rosterFrom, which carries the join.
var rosterColumns = `
	e.id, e.organization_id, e.email, e.username, e.role, ` + hirerRefColumns("author") + `,
	e.redeemed_at, e.redeemed_by, e.created_at`

const rosterFrom = `
	FROM organization_hirer_roster e
	JOIN hirer_accounts author ON author.id = e.added_by`

// scanRosterEntry reads one row of rosterColumns, in its order.
func scanRosterEntry(row rowScanner) (*port.RosterEntry, error) {
	var e port.RosterEntry
	targets := scanTargets(
		[]any{&e.ID, &e.OrganizationID, &e.Email, &e.Username, &e.Role},
		&e.AddedBy,
		[]any{&e.RedeemedAt, &e.RedeemedBy, &e.CreatedAt})
	if err := row.Scan(targets...); err != nil {
		return nil, err
	}
	return &e, nil
}

// ClaimUsername reserves a name in the global namespace.
//
// The PRIMARY KEY does the checking. A name held by a live seat, a revoked
// one, or another organisation's roster entry all live in this one table, so
// one insert answers the only question that matters: is this name free?
func (r *OrganizationRepository) ClaimUsername(ctx context.Context, t port.Tx, username string) error {
	_, err := r.db.q(t).Exec(ctx,
		`INSERT INTO hirer_usernames (username) VALUES ($1)`, username)
	return translate(err, fmt.Sprintf("claiming the username %q", username))
}

// ReleaseUsername gives a name back when no seat ever carried it.
//
// The foreign key from hirer_accounts is the guard: if a seat holds the name,
// this DELETE fails and the name stays claimed forever, which is exactly the
// rule. So the caller does not have to be careful — the schema is.
func (r *OrganizationRepository) ReleaseUsername(ctx context.Context, t port.Tx, username string) error {
	_, err := r.db.q(t).Exec(ctx,
		`DELETE FROM hirer_usernames WHERE username = $1`, username)
	return translate(err, fmt.Sprintf("releasing the username %q", username))
}

// AddRosterEntry reserves an address and a username for a seat that does not
// exist yet.
//
// The unique constraints do the checking. A username taken anywhere on the
// platform, or an address already on this organization's roster, comes back as
// ErrConflict — the caller never reads first, exactly as ADR-0009 requires, so
// there is no window between the check and the write and no endpoint that
// answers "does this name exist".
func (r *OrganizationRepository) AddRosterEntry(ctx context.Context, t port.Tx, e *port.RosterEntry) (*port.RosterEntry, error) {
	id := e.ID
	if id == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generating roster entry id: %w", err)
		}
		id = domain.RosterEntryID(generated.String())
	}

	// A CTE so the author can be joined: RETURNING cannot reach another table.
	out, err := scanRosterEntry(r.db.q(t).QueryRow(ctx, `
		WITH e AS (
		    INSERT INTO organization_hirer_roster
		        (id, organization_id, email, username, role, added_by)
		    VALUES ($1, $2, $3, $4, $5, $6)
		    RETURNING id, organization_id, email, username, role, added_by,
		              redeemed_at, redeemed_by, created_at
		)
		SELECT`+rosterColumns+`
		FROM e JOIN hirer_accounts author ON author.id = e.added_by`,
		string(id), string(e.OrganizationID), e.Email, e.Username,
		string(e.Role), string(e.AddedBy.ID)))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("rostering %q at org %s", e.Email, e.OrganizationID))
	}
	return out, nil
}

// RosterEntryByEmail finds an UNREDEEMED entry for an address.
//
// Redeemed entries are excluded rather than returned-and-checked: a redeemed
// entry cannot be redeemed again, so from the redemption path's point of view
// it is not there.
func (r *OrganizationRepository) RosterEntryByEmail(ctx context.Context, t port.Tx, orgID domain.OrganizationID, email string) (*port.RosterEntry, error) {
	e, err := scanRosterEntry(r.db.q(t).QueryRow(ctx,
		`SELECT`+rosterColumns+rosterFrom+`
		  WHERE e.organization_id = $1 AND e.email = $2 AND e.redeemed_at IS NULL`,
		string(orgID), email))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("roster entry %q at org %s", email, orgID))
	}
	return e, nil
}

// RosterEntryByID reads one entry, redeemed or not.
func (r *OrganizationRepository) RosterEntryByID(ctx context.Context, t port.Tx, id domain.RosterEntryID) (*port.RosterEntry, error) {
	e, err := scanRosterEntry(r.db.q(t).QueryRow(ctx,
		`SELECT`+rosterColumns+rosterFrom+` WHERE e.id = $1`, string(id)))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("roster entry %s", id))
	}
	return e, nil
}

// ListRoster returns every entry an organization holds, redeemed or not.
//
// The redeemed ones stay because they are the record of why each seat exists —
// removing one is what revokes a seat, so an owner needs to see them.
func (r *OrganizationRepository) ListRoster(ctx context.Context, orgID domain.OrganizationID) ([]port.RosterEntry, error) {
	rows, err := r.db.pool.Query(ctx,
		`SELECT`+rosterColumns+rosterFrom+`
		  WHERE e.organization_id = $1
		  ORDER BY e.redeemed_at NULLS FIRST, e.created_at`, string(orgID))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("roster of org %s", orgID))
	}
	defer rows.Close()

	// Never nil: an empty roster must serialize as [] rather than null.
	out := make([]port.RosterEntry, 0)
	for rows.Next() {
		entry, err := scanRosterEntry(rows)
		if err != nil {
			return nil, translate(err, "scanning a roster entry")
		}
		out = append(out, *entry)
	}
	return out, translate(rows.Err(), "reading the roster")
}

// RemoveRosterEntry deletes the entry.
//
// Only the entry. Revoking the seat it created spans the session repository
// too, so the service owns that — this is the half that says the address may
// not be redeemed again.
func (r *OrganizationRepository) RemoveRosterEntry(ctx context.Context, t port.Tx, id domain.RosterEntryID) error {
	_, err := r.db.q(t).Exec(ctx,
		`DELETE FROM organization_hirer_roster WHERE id = $1`, string(id))
	return translate(err, fmt.Sprintf("removing roster entry %s", id))
}

// RedeemRosterEntry creates the seat, the membership, and marks the entry
// redeemed — in one transaction.
//
// The seat inherits the organization's verification rather than earning its
// own, which is the point of verifying an org rather than a person. Nothing
// here writes verified_at: capability is read through the organization.
//
// The username and role come from the ENTRY, never from the caller. A redeemer
// who could name themselves could take a colleague's name, or claim an owner
// seat and roster further people from it.
func (r *OrganizationRepository) RedeemRosterEntry(ctx context.Context, t port.Tx, id domain.RosterEntryID, h *domain.Hirer, passwordHash []byte, now time.Time) (*domain.Hirer, error) {
	q := r.db.q(t)

	hirerID := h.ID
	if hirerID == "" {
		generated, gerr := uuid.NewV7()
		if gerr != nil {
			return nil, fmt.Errorf("generating hirer id: %w", gerr)
		}
		hirerID = domain.HirerID(generated.String())
	}

	// SELECT ... FOR UPDATE, then insert, then stamp.
	//
	// The stamp cannot come first: redeemed_by references hirer_accounts, and
	// the constraint is checked at statement time, so pointing it at a seat
	// that does not exist yet fails immediately.
	//
	// FOR UPDATE carries the single-use rule instead. A second redeemer blocks
	// here until this transaction ends and then sees redeemed_at set, which is
	// the same exclusion the UPDATE predicate gave — and everything after it
	// is in the same transaction, so a failed insert rolls the claim back.
	var (
		orgID    string
		role     string
		email    string
		username string
	)
	err := q.QueryRow(ctx, `
		SELECT organization_id, role, email, username
		  FROM organization_hirer_roster
		 WHERE id = $1 AND redeemed_at IS NULL
		   FOR UPDATE`, string(id),
	).Scan(&orgID, &role, &email, &username)
	if err != nil {
		if isNotFound(err) {
			// No unredeemed row: the entry was consumed by a concurrent
			// redemption, or removed between the proof and its use.
			return nil, fmt.Errorf("roster entry %s is no longer redeemable: %w",
				id, port.ErrRosterEntryRedeemed)
		}
		return nil, translate(err, fmt.Sprintf("redeeming roster entry %s", id))
	}

	var hash *[]byte
	if h.AuthProvider == domain.ProviderEmail {
		hash = &passwordHash
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO hirer_accounts
		    (id, organization_id, username, email, auth_provider, display_name, password_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		string(hirerID), orgID, username, email, string(h.AuthProvider), h.DisplayName, hash); err != nil {
		return nil, translate(err, "creating the redeemed hirer")
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO organization_members (organization_id, hirer_account_id, role)
		VALUES ($1, $2, $3)`, orgID, string(hirerID), role); err != nil {
		return nil, translate(err, "creating the membership")
	}

	// The entry is stamped, not deleted: it is the record of why this seat
	// exists, and removing it is what revokes access later (ADR-0016 §5).
	if _, err := q.Exec(ctx, `
		UPDATE organization_hirer_roster
		   SET redeemed_at = $2, redeemed_by = $3
		 WHERE id = $1`, string(id), now, string(hirerID)); err != nil {
		return nil, translate(err, fmt.Sprintf("stamping roster entry %s", id))
	}

	created := *h
	created.ID = hirerID
	created.OrganizationID = domain.OrganizationID(orgID)
	created.OrgRole = domain.OrgRole(role)
	created.Email = email
	created.Username = username

	// The organization travels with the seat. Capability is a property of the
	// org, not the person (ADR-0002), so a seat reported without it cannot say
	// whether it may hire — which is the first thing the new member asks.
	org, err := r.db.Hirers().Organization(ctx, created.OrganizationID)
	if err != nil {
		return nil, err
	}
	created.Organization = org
	return &created, nil
}

// VerificationFor reads a seat's own verification request.
//
// Ordered most recent first: registration raises one against the organization,
// and a rejected org may raise another later. A hirer asking where they stand
// means the current attempt, not the one that was refused last year.
func (r *OrganizationRepository) VerificationFor(ctx context.Context, hirer domain.HirerID, org domain.OrganizationID) (*port.VerificationRequest, error) {
	var v port.VerificationRequest
	var reason, hirerID, orgID *string

	// No join here, unlike the admin queue: the caller already knows who they
	// are and is asking only where their request stands.
	err := r.db.pool.QueryRow(ctx, `
		SELECT id, hirer_account_id, organization_id, status, created_at, reviewed_at, decision_reason
		FROM verification_requests
		WHERE hirer_account_id = $1::uuid OR organization_id = $2::uuid
		ORDER BY created_at DESC
		LIMIT 1`, string(hirer), string(org),
	).Scan(&v.ID, &hirerID, &orgID, &v.Status, &v.CreatedAt, &v.ReviewedAt, &reason)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("verification for hirer %s", hirer))
	}
	if reason != nil {
		v.DecisionReason = *reason
	}
	if hirerID != nil {
		v.Hirer = &port.VerificationHirer{ID: domain.HirerID(*hirerID)}
	}
	if orgID != nil {
		v.Organization = &port.VerificationOrganization{ID: domain.OrganizationID(*orgID)}
	}
	return &v, nil
}

// VerifyOrganization approves an organization, lifting every seat at once.
//
// One UPDATE against organizations, and none against hirer_accounts. Stamping
// each seat would leave two sources of truth that could disagree, and would
// miss any seat invited afterwards — which is exactly the case ADR-0008 §3a
// depends on.
func (r *OrganizationRepository) VerifyOrganization(ctx context.Context, t port.Tx, id domain.OrganizationID, by domain.AdminID, reason string) error {
	q := r.db.q(t)

	tag, err := q.Exec(ctx, `
		UPDATE organizations
		SET verified_at = now(), verified_by = $2, updated_at = now()
		WHERE id = $1 AND verified_at IS NULL`, string(id), string(by))
	if err != nil {
		return translate(err, fmt.Sprintf("verifying organization %s", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("organization %s is unknown or already verified: %w", id, port.ErrConflict)
	}

	if _, err := q.Exec(ctx, `
		UPDATE verification_requests
		SET status = 'approved', reviewed_by = $2, reviewed_at = now(),
		    decision_reason = NULLIF($3, ''), updated_at = now()
		WHERE organization_id = $1 AND status = 'pending'`,
		string(id), string(by), reason); err != nil {
		return translate(err, "closing the verification request")
	}
	return nil
}

// Addresses lists an organisation's offices.
//
// Main office first, then by city, so a picker's default is the answer most
// roles want. Written during onboarding (ADR-0017); read by roles, which point
// at a row rather than retyping one.
func (r *OrganizationRepository) Addresses(ctx context.Context, id domain.OrganizationID) ([]domain.Address, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT id, organization_id, country, city, coalesce(postal_code, ''),
		       street1, coalesce(street2, ''), is_main_office
		  FROM organization_addresses
		 WHERE organization_id = $1
		 ORDER BY is_main_office DESC, city`, string(id))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("listing addresses for organization %s", id))
	}
	defer rows.Close()

	out := []domain.Address{}
	for rows.Next() {
		var a domain.Address
		var addressID, orgID string
		if err := rows.Scan(&addressID, &orgID, &a.Country, &a.City,
			&a.PostalCode, &a.Street1, &a.Street2, &a.IsMainOffice); err != nil {
			return nil, translate(err, "scanning an address")
		}
		a.ID, a.OrganizationID = domain.AddressID(addressID), domain.OrganizationID(orgID)
		out = append(out, a)
	}
	return out, translate(rows.Err(), "listing addresses")
}
