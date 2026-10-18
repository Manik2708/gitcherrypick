package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// OfficeRequestRepository owns fully remote organisations asking for an office
// (ADR-0024 decision 13).
//
// The address is held flat on the request and written to
// organization_addresses only on approval, on the pattern of onboarding: until
// a person decides, the organisation has no address at all, and no reader of
// that table has to remember to skip a pending row.
type OfficeRequestRepository struct{ db *DB }

// OfficeRequests returns the office-request repository.
func (db *DB) OfficeRequests() *OfficeRequestRepository { return &OfficeRequestRepository{db: db} }

var _ port.OfficeRequestRepository = (*OfficeRequestRepository)(nil)

// officeRequestSelect reads a request with its organisation, its requester and
// the request it supersedes.
//
// "Supersedes" is not stored: it is the organisation's previous request when
// that one was rejected, read here so an administrator looking at a second
// attempt can find the first (ADR-0024 amendment 1 §1.2).
var officeRequestSelect = `
	SELECT q.id, q.organization_id, org.name,
	       q.country, q.city, coalesce(q.postal_code, ''), q.street1,
	       coalesce(q.street2, ''),
	       ` + hirerRefColumns("author") + `,
	       q.decided_at, q.decided_by, coalesce(q.decision_reason, ''),
	       q.address_id,
	       (SELECT prev.id FROM organization_office_requests prev
	         WHERE prev.organization_id = q.organization_id
	           AND (prev.created_at, prev.id) < (q.created_at, q.id)
	         ORDER BY prev.created_at DESC, prev.id DESC
	         LIMIT 1) AS previous_id,
	       (SELECT prev.decided_at IS NOT NULL AND prev.address_id IS NULL
	          FROM organization_office_requests prev
	         WHERE prev.organization_id = q.organization_id
	           AND (prev.created_at, prev.id) < (q.created_at, q.id)
	         ORDER BY prev.created_at DESC, prev.id DESC
	         LIMIT 1) AS previous_rejected,
	       q.created_at
	  FROM organization_office_requests q
	  JOIN organizations org ON org.id = q.organization_id
	  JOIN hirer_accounts author ON author.id = q.requested_by`

// officeRequestRow is the storage shape of one request.
type officeRequestRow struct {
	id, orgID, orgName                      string
	country, city, postal, street1, street2 string
	authorID, authorUsername, authorName    string
	authorActive                            bool
	decidedAt                               *time.Time
	decidedBy                               *string
	reason                                  string
	addressID                               *string
	previousID                              *string
	previousRejected                        *bool
	createdAt                               time.Time
}

func (r *officeRequestRow) targets() []any {
	return []any{&r.id, &r.orgID, &r.orgName,
		&r.country, &r.city, &r.postal, &r.street1, &r.street2,
		&r.authorID, &r.authorUsername, &r.authorName, &r.authorActive,
		&r.decidedAt, &r.decidedBy, &r.reason, &r.addressID,
		&r.previousID, &r.previousRejected, &r.createdAt}
}

func (r *officeRequestRow) domain() domain.OfficeRequest {
	out := domain.OfficeRequest{
		ID:               domain.OfficeRequestID(r.id),
		OrganizationID:   domain.OrganizationID(r.orgID),
		OrganizationName: r.orgName,
		Country:          r.country, City: r.city, PostalCode: r.postal,
		Street1: r.street1, Street2: r.street2,
		RequestedBy: domain.HirerRef{
			ID: domain.HirerID(r.authorID), Username: r.authorUsername,
			DisplayName: r.authorName, Active: r.authorActive,
		},
		DecidedAt: r.decidedAt, Reason: r.reason, CreatedAt: r.createdAt,
	}
	if r.decidedBy != nil {
		a := domain.AdminID(*r.decidedBy)
		out.DecidedBy = &a
	}
	if r.addressID != nil {
		a := domain.AddressID(*r.addressID)
		out.AddressID = &a
	}
	if r.previousID != nil && r.previousRejected != nil && *r.previousRejected {
		p := domain.OfficeRequestID(*r.previousID)
		out.Supersedes = &p
	}
	return out
}

// one reads a single request by a WHERE clause on q.
func (r *OfficeRequestRepository) one(ctx context.Context, t port.Tx, what, where string, args ...any) (*domain.OfficeRequest, error) {
	var row officeRequestRow
	if err := r.db.q(t).QueryRow(ctx, officeRequestSelect+" "+where, args...).Scan(row.targets()...); err != nil {
		return nil, translate(err, what)
	}
	out := row.domain()
	return &out, nil
}

// Submit records a request. The partial unique index on undecided requests
// turns a second one into ErrConflict, with no prior read to race.
func (r *OfficeRequestRepository) Submit(ctx context.Context, t port.Tx, in *domain.OfficeRequest) (*domain.OfficeRequest, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating office request id: %w", err)
	}
	if _, err := r.db.q(t).Exec(ctx, `
		INSERT INTO organization_office_requests
		    (id, organization_id, country, city, postal_code, street1, street2, requested_by)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, NULLIF($7, ''), $8)`,
		id.String(), string(in.OrganizationID), in.Country, in.City, in.PostalCode,
		in.Street1, in.Street2, string(in.RequestedBy.ID)); err != nil {
		return nil, translate(err, fmt.Sprintf("requesting an office for organization %s", in.OrganizationID))
	}
	return r.one(ctx, t, "reading the office request back", `WHERE q.id = $1`, id.String())
}

// ByID reads one request. Inside a transaction it locks the row, so two
// administrators deciding it serialise.
func (r *OfficeRequestRepository) ByID(ctx context.Context, t port.Tx, id domain.OfficeRequestID) (*domain.OfficeRequest, error) {
	where := `WHERE q.id = $1`
	if t != nil {
		where += ` FOR UPDATE OF q`
	}
	return r.one(ctx, t, fmt.Sprintf("office request %s", id), where, string(id))
}

// Latest is the organisation's most recent request.
func (r *OfficeRequestRepository) Latest(ctx context.Context, org domain.OrganizationID) (*domain.OfficeRequest, error) {
	return r.one(ctx, nil, fmt.Sprintf("latest office request of organization %s", org),
		`WHERE q.organization_id = $1 ORDER BY q.created_at DESC, q.id DESC LIMIT 1`, string(org))
}

// Queue is every undecided request, oldest first.
func (r *OfficeRequestRepository) Queue(ctx context.Context) ([]domain.OfficeRequest, error) {
	rows, err := r.db.pool.Query(ctx, officeRequestSelect+`
		 WHERE q.decided_at IS NULL
		 ORDER BY q.created_at, q.id`)
	if err != nil {
		return nil, translate(err, "reading the office request queue")
	}
	defer rows.Close()

	out := []domain.OfficeRequest{}
	for rows.Next() {
		var row officeRequestRow
		if err := rows.Scan(row.targets()...); err != nil {
			return nil, translate(err, "scanning an office request")
		}
		out = append(out, row.domain())
	}
	return out, translate(rows.Err(), "reading the office request queue")
}

// Approve writes the main office, flips the kind and stamps the request.
//
// The caller holds the organisation's lock. The address is added by the owner
// who asked: they are who put it forward, and an administrator agreeing does
// not make it theirs.
func (r *OfficeRequestRepository) Approve(ctx context.Context, t port.Tx, id domain.OfficeRequestID, by domain.AdminID, at time.Time) (*domain.Address, error) {
	q := r.db.q(t)

	var (
		orgID, country, city, street1, requestedBy string
		postal, street2                            *string
	)
	if err := q.QueryRow(ctx, `
		SELECT organization_id, country, city, postal_code, street1, street2, requested_by
		  FROM organization_office_requests
		 WHERE id = $1 AND decided_at IS NULL
		   FOR UPDATE`, string(id)).
		Scan(&orgID, &country, &city, &postal, &street1, &street2, &requestedBy); err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("office request %s is unknown or decided: %w", id, port.ErrConflict)
		}
		return nil, translate(err, fmt.Sprintf("reading office request %s", id))
	}

	addressID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating address id: %w", err)
	}
	var row addressRow
	if err := q.QueryRow(ctx, `
		INSERT INTO organization_addresses AS a
		    (id, organization_id, country, city, postal_code, street1, street2,
		     is_main_office, added_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, true, $8)
		RETURNING`+addressColumns("a"),
		addressID.String(), orgID, country, city, postal, street1, street2, requestedBy,
	).Scan(row.targets()...); err != nil {
		return nil, translate(err, "writing the approved main office")
	}

	if _, err := q.Exec(ctx, `
		UPDATE organizations SET workplace = 'office', updated_at = $2 WHERE id = $1`,
		orgID, at); err != nil {
		return nil, translate(err, fmt.Sprintf("making organization %s an office one", orgID))
	}
	if _, err := q.Exec(ctx, `
		UPDATE organization_office_requests
		   SET decided_at = $2, decided_by = $3, address_id = $4
		 WHERE id = $1`, string(id), at, string(by), addressID.String()); err != nil {
		return nil, translate(err, fmt.Sprintf("stamping office request %s approved", id))
	}
	out := row.domain()
	return &out, nil
}

// Reject stamps the request decided with no address.
func (r *OfficeRequestRepository) Reject(ctx context.Context, t port.Tx, id domain.OfficeRequestID, by domain.AdminID, reason string, at time.Time) error {
	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE organization_office_requests
		   SET decided_at = $2, decided_by = $3, decision_reason = NULLIF($4, '')
		 WHERE id = $1 AND decided_at IS NULL`, string(id), at, string(by), reason)
	if err != nil {
		return translate(err, fmt.Sprintf("rejecting office request %s", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("office request %s is unknown or decided: %w", id, port.ErrConflict)
	}
	return nil
}
