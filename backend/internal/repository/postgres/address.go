package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// addressColumns is one organization_addresses row, in the order scanAddress
// reads it. Prefixed with the alias the caller gives the table.
func addressColumns(alias string) string {
	return fmt.Sprintf(`
	%[1]s.id, %[1]s.organization_id, %[1]s.country, %[1]s.city,
	coalesce(%[1]s.postal_code, ''), %[1]s.street1, coalesce(%[1]s.street2, ''),
	%[1]s.is_main_office, %[1]s.added_by, %[1]s.removed_at, %[1]s.removed_by,
	%[1]s.created_at`, alias)
}

// addressColumnsNullable is addressColumns for a LEFT JOINed table, whose
// NOT NULL columns may read as NULL when nothing joined. The caller checks for
// a join separately and ignores the zero values when there was none.
func addressColumnsNullable(alias string) string {
	return fmt.Sprintf(`
	coalesce(%[1]s.id::text, ''), coalesce(%[1]s.organization_id::text, ''),
	coalesce(%[1]s.country, ''), coalesce(%[1]s.city, ''),
	coalesce(%[1]s.postal_code, ''), coalesce(%[1]s.street1, ''),
	coalesce(%[1]s.street2, ''), coalesce(%[1]s.is_main_office, false),
	%[1]s.added_by, %[1]s.removed_at, %[1]s.removed_by,
	coalesce(%[1]s.created_at, 'epoch'::timestamptz)`, alias)
}

// addressRow is the storage shape of an address, before translation.
//
// Kept apart from domain.Address so that the domain type never carries a
// driver's notion of a nullable column.
type addressRow struct {
	id, orgID     string
	country, city string
	postal        string
	street1       string
	street2       string
	main          bool
	addedBy       *string
	removedAt     *time.Time
	removedBy     *string
	createdAt     time.Time
}

func (a *addressRow) targets() []any {
	return []any{&a.id, &a.orgID, &a.country, &a.city, &a.postal, &a.street1,
		&a.street2, &a.main, &a.addedBy, &a.removedAt, &a.removedBy, &a.createdAt}
}

func (a *addressRow) domain() domain.Address {
	out := domain.Address{
		ID: domain.AddressID(a.id), OrganizationID: domain.OrganizationID(a.orgID),
		Country: a.country, City: a.city, PostalCode: a.postal,
		Street1: a.street1, Street2: a.street2, IsMainOffice: a.main,
		RemovedAt: a.removedAt, CreatedAt: a.createdAt,
	}
	if a.addedBy != nil {
		h := domain.HirerID(*a.addedBy)
		out.AddedBy = &h
	}
	if a.removedBy != nil {
		h := domain.HirerID(*a.removedBy)
		out.RemovedBy = &h
	}
	return out
}

// queryAddresses runs a query selecting addressColumns("a") and translates.
func (r *OrganizationRepository) queryAddresses(ctx context.Context, t port.Tx, what, query string, args ...any) ([]domain.Address, error) {
	rows, err := r.db.q(t).Query(ctx, query, args...)
	if err != nil {
		return nil, translate(err, what)
	}
	defer rows.Close()

	out := []domain.Address{}
	for rows.Next() {
		var row addressRow
		if err := rows.Scan(row.targets()...); err != nil {
			return nil, translate(err, "scanning an address")
		}
		out = append(out, row.domain())
	}
	return out, translate(rows.Err(), what)
}

// Addresses lists an organisation's LIVE addresses.
//
// Main office first, then in the order they were added, so an owner sees them
// as they built the list (ADR-0024 amendment 1 §1.8). The id breaks a tie:
// it is a UUIDv7, so it is time-ordered too.
func (r *OrganizationRepository) Addresses(ctx context.Context, id domain.OrganizationID) ([]domain.Address, error) {
	return r.queryAddresses(ctx, nil,
		fmt.Sprintf("listing addresses for organization %s", id), `
		SELECT`+addressColumns("a")+`
		  FROM organization_addresses a
		 WHERE a.organization_id = $1 AND a.removed_at IS NULL
		 ORDER BY a.is_main_office DESC, a.created_at, a.id`, string(id))
}

// AllAddresses lists every address, removed ones included.
func (r *OrganizationRepository) AllAddresses(ctx context.Context, t port.Tx, id domain.OrganizationID) ([]domain.Address, error) {
	return r.queryAddresses(ctx, t,
		fmt.Sprintf("listing every address for organization %s", id), `
		SELECT`+addressColumns("a")+`
		  FROM organization_addresses a
		 WHERE a.organization_id = $1
		 ORDER BY a.is_main_office DESC, a.created_at, a.id`, string(id))
}

// Workplace reads the organisation's kind.
func (r *OrganizationRepository) Workplace(ctx context.Context, t port.Tx, id domain.OrganizationID) (domain.Workplace, error) {
	var w string
	if err := r.db.q(t).QueryRow(ctx,
		`SELECT workplace FROM organizations WHERE id = $1`, string(id)).Scan(&w); err != nil {
		return "", translate(err, fmt.Sprintf("reading the workplace of organization %s", id))
	}
	return domain.Workplace(w), nil
}

// LockWorkplace locks the organisation's row and reads its kind.
//
// FOR UPDATE on the one row every kind-dependent write touches first, so two
// such writes serialise rather than each passing a check the other is about to
// invalidate (ADR-0024 §Invariants).
func (r *OrganizationRepository) LockWorkplace(ctx context.Context, t port.Tx, id domain.OrganizationID) (domain.Workplace, error) {
	var w string
	if err := r.db.q(t).QueryRow(ctx,
		`SELECT workplace FROM organizations WHERE id = $1 FOR UPDATE`, string(id)).Scan(&w); err != nil {
		return "", translate(err, fmt.Sprintf("locking organization %s", id))
	}
	return domain.Workplace(w), nil
}

// AddAddress writes a further address, live, and never the main office.
func (r *OrganizationRepository) AddAddress(ctx context.Context, t port.Tx, a *domain.Address) (*domain.Address, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating address id: %w", err)
	}
	var addedBy *string
	if a.AddedBy != nil {
		s := string(*a.AddedBy)
		addedBy = &s
	}

	var row addressRow
	err = r.db.q(t).QueryRow(ctx, `
		INSERT INTO organization_addresses AS a
		    (id, organization_id, country, city, postal_code, street1, street2,
		     is_main_office, added_by)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, NULLIF($7, ''), false, $8)
		RETURNING`+addressColumns("a"),
		id.String(), string(a.OrganizationID), a.Country, a.City, a.PostalCode,
		a.Street1, a.Street2, addedBy).Scan(row.targets()...)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("adding an address to organization %s", a.OrganizationID))
	}
	out := row.domain()
	return &out, nil
}

// RemoveAddress stamps one live address removed.
func (r *OrganizationRepository) RemoveAddress(ctx context.Context, t port.Tx, id domain.AddressID, by domain.HirerID, at time.Time) error {
	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE organization_addresses
		   SET removed_at = $2, removed_by = $3, updated_at = $2
		 WHERE id = $1 AND removed_at IS NULL`, string(id), at, string(by))
	if err != nil {
		return translate(err, fmt.Sprintf("removing address %s", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("address %s is unknown or already removed: %w", id, port.ErrNotFound)
	}
	return nil
}

// GoFullyRemote stamps every live address removed and flips the kind.
//
// Both in the caller's transaction, which already holds the organisation's
// lock: the kind and the addresses never disagree, not even for a moment.
func (r *OrganizationRepository) GoFullyRemote(ctx context.Context, t port.Tx, id domain.OrganizationID, by domain.HirerID, at time.Time) error {
	q := r.db.q(t)
	if _, err := q.Exec(ctx, `
		UPDATE organization_addresses
		   SET removed_at = $2, removed_by = $3, updated_at = $2
		 WHERE organization_id = $1 AND removed_at IS NULL`, string(id), at, string(by)); err != nil {
		return translate(err, fmt.Sprintf("removing the addresses of organization %s", id))
	}
	tag, err := q.Exec(ctx, `
		UPDATE organizations SET workplace = 'fully_remote', updated_at = $2
		 WHERE id = $1`, string(id), at)
	if err != nil {
		return translate(err, fmt.Sprintf("making organization %s fully remote", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("organization %s: %w", id, port.ErrNotFound)
	}
	return nil
}
