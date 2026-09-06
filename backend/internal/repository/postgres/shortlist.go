package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// ShortlistRepository owns hiring rounds and the two-phase disclosure.
//
// Shortlisting is staged then confirmed (ADR-0008 §3a). Adding an entry
// discloses NOTHING; confirm creates every unnotified entry's contact request
// in one transaction and is irreversible. The rule it serves is that a
// shortlisted contributor can never be un-shortlisted — once someone has been
// told an organization is interested, that is a fact — and a rule that strict
// is only tolerable if there is a moment before it binds.
//
// Rounds belong to the ORGANIZATION, not the recruiter who made them: one that
// vanished when a recruiter left the company would be worse than useless.
type ShortlistRepository struct{ db *DB }

// Shortlists returns the hiring-round repository.
func (db *DB) Shortlists() *ShortlistRepository { return &ShortlistRepository{db: db} }

var _ port.ShortlistRepository = (*ShortlistRepository)(nil)

// ContactWindow is how long a contributor has to answer before a request
// lapses.
const ContactWindow = 30 * 24 * time.Hour

// OverdueFlagRatio is the share of an organization's open rounds that must be
// past their promised date before admins are told (ADR-0005).
//
// Flagged, never blocked: blocking would punish a hirer for a candidate's
// silence as readily as for their own neglect.
const OverdueFlagRatio = 0.8

const shortlistColumns = `
	s.id, s.organization_id, s.name, coalesce(s.description, ''), s.status,
	s.tentative_result_date, s.created_by, s.created_at, s.updated_at,
	s.first_confirmed_at, s.closed_at`

// ByID reads a round with its entries.
func (r *ShortlistRepository) ByID(ctx context.Context, id domain.ShortlistID) (*domain.Shortlist, error) {
	s, err := scanShortlist(r.db.pool.QueryRow(ctx,
		`SELECT`+shortlistColumns+` FROM shortlists s WHERE s.id = $1`, string(id)))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("shortlist %s", id))
	}
	if s.Entries, err = r.entries(ctx, id); err != nil {
		return nil, err
	}
	return s, nil
}

// ListByOrganization reads an org's rounds, newest first.
func (r *ShortlistRepository) ListByOrganization(ctx context.Context, id domain.OrganizationID, status *domain.ShortlistStatus) ([]domain.Shortlist, error) {
	query := `SELECT` + shortlistColumns + `
		FROM shortlists s
		WHERE s.organization_id = $1
		  AND ($2::shortlist_status IS NULL OR s.status = $2::shortlist_status)
		ORDER BY s.created_at DESC`

	var filter *string
	if status != nil {
		v := string(*status)
		filter = &v
	}

	rows, err := r.db.pool.Query(ctx, query, string(id), filter)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("listing shortlists for %s", id))
	}
	defer rows.Close()

	var out []domain.Shortlist
	for rows.Next() {
		s, err := scanShortlist(rows)
		if err != nil {
			return nil, translate(err, "scanning shortlist")
		}
		out = append(out, *s)
	}
	return out, translate(rows.Err(), "listing shortlists")
}

// Create opens a DRAFT round.
//
// Draft, not open: a round that notified people the moment it was created is
// what the confirm step exists to prevent.
func (r *ShortlistRepository) Create(ctx context.Context, s *domain.Shortlist) (*domain.Shortlist, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating shortlist id: %w", err)
	}

	created, err := scanShortlist(r.db.pool.QueryRow(ctx, `
		INSERT INTO shortlists
		    (id, organization_id, name, description, tentative_result_date, created_by, status)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, 'draft')
		RETURNING id, organization_id, name, coalesce(description, ''), status,
		          tentative_result_date, created_by, created_at, updated_at,
		          first_confirmed_at, closed_at`,
		id.String(), string(s.OrganizationID), s.Name, s.Description,
		s.TentativeResultDate, string(s.CreatedBy)))
	if err != nil {
		return nil, translate(err, "creating shortlist")
	}
	return created, nil
}

// Update edits a round's presentation and its promised date.
//
// Editing tentative_result_date does NOT rewrite contact requests already
// sent: those carry their own copy, so the record of what a contributor was
// told survives a later edit (ADR-0005).
func (r *ShortlistRepository) Update(ctx context.Context, id domain.ShortlistID, name, description *string, date *time.Time) (*domain.Shortlist, error) {
	updated, err := scanShortlist(r.db.pool.QueryRow(ctx, `
		UPDATE shortlists
		SET name                  = coalesce($2, name),
		    description           = coalesce($3, description),
		    tentative_result_date = coalesce($4, tentative_result_date),
		    updated_at            = now()
		WHERE id = $1 AND status <> 'closed'
		RETURNING id, organization_id, name, coalesce(description, ''), status,
		          tentative_result_date, created_by, created_at, updated_at,
		          first_confirmed_at, closed_at`,
		string(id), name, description, date))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("updating shortlist %s", id))
	}
	return updated, nil
}

// Close ends a round.
func (r *ShortlistRepository) Close(ctx context.Context, id domain.ShortlistID) (*domain.Shortlist, error) {
	closed, err := scanShortlist(r.db.pool.QueryRow(ctx, `
		UPDATE shortlists
		SET status = 'closed', closed_at = now(), updated_at = now()
		WHERE id = $1 AND status <> 'closed'
		RETURNING id, organization_id, name, coalesce(description, ''), status,
		          tentative_result_date, created_by, created_at, updated_at,
		          first_confirmed_at, closed_at`,
		string(id)))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("closing shortlist %s", id))
	}
	// The entries come back with it: closing reports how many people the round
	// reached, and that count is the last thing anyone will read about it.
	if closed.Entries, err = r.entries(ctx, id); err != nil {
		return nil, err
	}
	return closed, nil
}

// AddEntry stages a candidate and discloses nothing.
//
// No contact request is written here. That is the entire difference between
// this and ADR-0005's original design, and it is what gives a mis-click a
// moment in which it can still be undone.
func (r *ShortlistRepository) AddEntry(ctx context.Context, e *domain.ShortlistEntry) (*domain.ShortlistEntry, error) {
	var out domain.ShortlistEntry
	err := r.db.pool.QueryRow(ctx, `
		WITH staged AS (
		    INSERT INTO shortlist_entries (shortlist_id, user_id, note, added_by)
		    SELECT $1, $2, NULLIF($3, ''), $4
		    WHERE EXISTS (SELECT 1 FROM shortlists WHERE id = $1 AND status <> 'closed')
		    RETURNING shortlist_id, user_id, coalesce(note, '') AS note,
		              added_by, notified_at, added_at
		)
		SELECT s.shortlist_id, s.user_id, u.display_name, coalesce(gi.github_login, ''),
		       s.note, s.added_by, s.notified_at, s.added_at
		FROM staged s
		JOIN users u ON u.id = s.user_id
		LEFT JOIN user_github_identities gi ON gi.user_id = s.user_id`,
		string(e.ShortlistID), string(e.UserID), e.Note, string(e.AddedBy),
	).Scan(&out.ShortlistID, &out.UserID, &out.DisplayName, &out.GitHubLogin,
		&out.Note, &out.AddedBy, &out.NotifiedAt, &out.AddedAt)
	if err != nil {
		// No rows means the WHERE EXISTS failed: the round is closed or gone.
		// Staging someone for a finished search would only ever mislead them.
		return nil, translate(err, "staging shortlist entry")
	}
	return &out, nil
}

// RemoveEntry deletes a staged candidate.
//
// Permitted only while notified_at IS NULL. Afterwards the contributor was
// told, and deleting the entry would destroy the record of a disclosure that
// already happened — so it reports a conflict rather than a not-found.
func (r *ShortlistRepository) RemoveEntry(ctx context.Context, id domain.ShortlistID, user domain.UserID) error {
	tag, err := r.db.pool.Exec(ctx,
		`DELETE FROM shortlist_entries
		 WHERE shortlist_id = $1 AND user_id = $2 AND notified_at IS NULL`,
		string(id), string(user))
	if err != nil {
		return translate(err, "removing shortlist entry")
	}
	if tag.RowsAffected() == 0 {
		var notifiedAt *time.Time
		if err := r.db.pool.QueryRow(ctx,
			`SELECT notified_at FROM shortlist_entries
			 WHERE shortlist_id = $1 AND user_id = $2`,
			string(id), string(user)).Scan(&notifiedAt); err != nil {
			return translate(err, "removing shortlist entry")
		}
		if notifiedAt != nil {
			// The timestamp travels with the refusal: a hirer told "you cannot
			// remove this" needs to know when the disclosure happened, since
			// that is the fact the rule protects.
			return &port.NotifiedEntryError{NotifiedAt: *notifiedAt}
		}
		return fmt.Errorf("entry %s: %w", user, port.ErrNotFound)
	}
	return nil
}

// Confirm notifies every unnotified entry, in one transaction.
//
// This is the irreversible act. It writes one contact request per staged
// entry, stamps the entries, and opens the round — atomically, because a
// partially confirmed round would have told some people and not others.
//
// tentative_result_date is COPIED onto each request rather than joined at read
// time, so a later edit to the round cannot rewrite what a contributor was
// told.
//
// Idempotent by construction: the SELECT matches only notified_at IS NULL, so
// a second confirm with nothing new notifies nobody.
func (r *ShortlistRepository) Confirm(ctx context.Context, t port.Tx, id domain.ShortlistID) ([]domain.ContactRequest, error) {
	q := r.db.q(t)

	var status string
	if err := q.QueryRow(ctx,
		`SELECT status FROM shortlists WHERE id = $1 FOR UPDATE`, string(id)).Scan(&status); err != nil {
		return nil, translate(err, fmt.Sprintf("shortlist %s", id))
	}
	if status == string(domain.ShortlistClosed) {
		return nil, fmt.Errorf("shortlist %s is closed: %w", id, port.ErrConflict)
	}

	rows, err := q.Query(ctx, `
		WITH staged AS (
		    SELECT e.user_id
		    FROM shortlist_entries e
		    WHERE e.shortlist_id = $1 AND e.notified_at IS NULL
		    FOR UPDATE
		),
		created AS (
		    INSERT INTO contact_requests
		        (id, shortlist_id, user_id, organization_id, requested_by,
		         status, tentative_result_date, expires_at)
		    SELECT gen_random_uuid(), s.id, staged.user_id, s.organization_id, s.created_by,
		           'pending', s.tentative_result_date, now() + $2::interval
		    FROM staged
		    JOIN shortlists s ON s.id = $1
		    RETURNING id, shortlist_id, user_id, organization_id, requested_by,
		              status, tentative_result_date, expires_at
		),
		stamped AS (
		    UPDATE shortlist_entries
		    SET notified_at = now()
		    WHERE shortlist_id = $1 AND user_id IN (SELECT user_id FROM staged)
		)
		SELECT id, shortlist_id, user_id, organization_id, requested_by,
		       status, tentative_result_date, expires_at
		FROM created`, string(id), ContactWindow.String())
	if err != nil {
		return nil, translate(err, fmt.Sprintf("confirming shortlist %s", id))
	}
	defer rows.Close()

	var out []domain.ContactRequest
	for rows.Next() {
		var cr domain.ContactRequest
		if err := rows.Scan(&cr.ID, &cr.ShortlistID, &cr.UserID, &cr.OrganizationID,
			&cr.RequestedBy, &cr.Status, &cr.TentativeResultDate, &cr.ExpiresAt); err != nil {
			return nil, translate(err, "scanning contact request")
		}
		out = append(out, cr)
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err, "confirming shortlist")
	}

	if _, err := q.Exec(ctx, `
		UPDATE shortlists
		SET status = 'open',
		    first_confirmed_at = coalesce(first_confirmed_at, now()),
		    updated_at = now()
		WHERE id = $1`, string(id)); err != nil {
		return nil, translate(err, "opening shortlist")
	}
	return out, nil
}

// OverdueRatios drives the daily flagging job.
//
// Only OPEN rounds count. A draft promised nothing to anyone, so counting one
// would flag an organization for failing to deliver on a search it never
// announced — and a closed round is finished, so it cannot be late.
func (r *ShortlistRepository) OverdueRatios(ctx context.Context, now time.Time) ([]port.OverdueOrg, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT o.id, o.name,
		       count(*) AS open_shortlists,
		       count(*) FILTER (WHERE s.tentative_result_date < $1::date) AS overdue,
		       (count(*) FILTER (WHERE s.tentative_result_date < $1::date))::float8 / count(*) AS ratio
		FROM shortlists s
		JOIN organizations o ON o.id = s.organization_id
		WHERE s.status = 'open'
		GROUP BY o.id, o.name
		ORDER BY ratio DESC, o.name`, now)
	if err != nil {
		return nil, translate(err, "computing overdue ratios")
	}
	defer rows.Close()

	var out []port.OverdueOrg
	for rows.Next() {
		var o port.OverdueOrg
		if err := rows.Scan(&o.OrganizationID, &o.Name, &o.OpenShortlists, &o.Overdue, &o.Ratio); err != nil {
			return nil, translate(err, "scanning overdue ratio")
		}
		out = append(out, o)
	}
	return out, translate(rows.Err(), "computing overdue ratios")
}

func (r *ShortlistRepository) entries(ctx context.Context, id domain.ShortlistID) ([]domain.ShortlistEntry, error) {
	// Joined to the contributor, like AddEntry: a round is a list of people,
	// and every shape that renders one needs their name.
	// LEFT JOIN the contact request: an entry has none until the round is
	// confirmed, and the address comes back only once the contributor
	// released it (ADR-0005).
	rows, err := r.db.pool.Query(ctx, `
		SELECT e.shortlist_id, e.user_id, u.display_name, coalesce(gi.github_login, ''),
		       coalesce(e.note, ''), e.added_by, e.notified_at, e.added_at,
		       coalesce(cr.status::text, ''),
		       CASE WHEN cr.email_released_at IS NOT NULL THEN u.email ELSE '' END
		FROM shortlist_entries e
		JOIN users u ON u.id = e.user_id
		LEFT JOIN user_github_identities gi ON gi.user_id = e.user_id
		LEFT JOIN contact_requests cr
		       ON cr.shortlist_id = e.shortlist_id AND cr.user_id = e.user_id
		WHERE e.shortlist_id = $1
		ORDER BY e.added_at`, string(id))
	if err != nil {
		return nil, translate(err, "reading shortlist entries")
	}
	defer rows.Close()

	var out []domain.ShortlistEntry
	for rows.Next() {
		var e domain.ShortlistEntry
		if err := rows.Scan(&e.ShortlistID, &e.UserID, &e.DisplayName, &e.GitHubLogin,
			&e.Note, &e.AddedBy, &e.NotifiedAt, &e.AddedAt,
			&e.ContactStatus, &e.Email); err != nil {
			return nil, translate(err, "scanning shortlist entry")
		}
		out = append(out, e)
	}
	return out, translate(rows.Err(), "reading shortlist entries")
}

func scanShortlist(row rowScanner) (*domain.Shortlist, error) {
	var s domain.Shortlist
	if err := row.Scan(&s.ID, &s.OrganizationID, &s.Name, &s.Description, &s.Status,
		&s.TentativeResultDate, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt,
		&s.FirstConfirmedAt, &s.ClosedAt); err != nil {
		return nil, err
	}
	return &s, nil
}
