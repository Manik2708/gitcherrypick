package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// ContactRepository is the consent record.
//
// The asymmetry is the design (ADR-0005): a contributor sees the organization,
// the promised date and the payment disclosure; the hirer sees a status and,
// only after acceptance, an email. Releasing contact details on the strength of
// one side's interest is what makes sourcing tools feel extractive.
type ContactRepository struct{ db *DB }

// Contacts returns the consent repository.
func (db *DB) Contacts() *ContactRepository { return &ContactRepository{db: db} }

var _ port.ContactRepository = (*ContactRepository)(nil)

const contactColumns = `
	cr.id, cr.shortlist_id, cr.user_id, cr.organization_id, cr.requested_by,
	cr.status, cr.tentative_result_date, cr.responded_at, cr.email_released_at,
	cr.expires_at, cr.created_at,
	coalesce(o.name, ''), (o.verified_at IS NOT NULL), (o.payment_verified_at IS NOT NULL),
	u.display_name, coalesce(gi.github_login, ''),
	CASE WHEN cr.email_released_at IS NOT NULL THEN coalesce(u.email, '') ELSE '' END`

// contactFrom joins the organization every contact request is about.
const contactFrom = `
	FROM contact_requests cr
	LEFT JOIN organizations o ON o.id = cr.organization_id
	JOIN users u ON u.id = cr.user_id
	LEFT JOIN user_github_identities gi ON gi.user_id = cr.user_id`

// ByID reads one request.
func (r *ContactRepository) ByID(ctx context.Context, id domain.ContactID) (*domain.ContactRequest, error) {
	cr, err := scanContact(r.db.pool.QueryRow(ctx,
		`SELECT`+contactColumns+contactFrom+` WHERE cr.id = $1`, string(id)))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("contact request %s", id))
	}
	return cr, nil
}

// ListForUser reads a contributor's own requests.
//
// Their own, and nothing else: nothing about shortlist membership is visible
// to a contributor beyond the requests addressed to them, so there is no query
// here that could widen it.
func (r *ContactRepository) ListForUser(ctx context.Context, id domain.UserID, status *domain.ContactRequestStatus) ([]domain.ContactRequest, error) {
	var filter *string
	if status != nil {
		v := string(*status)
		filter = &v
	}
	return r.list(ctx, `
		SELECT`+contactColumns+contactFrom+`
		WHERE cr.user_id = $1
		  AND ($2::contact_request_status IS NULL OR cr.status = $2::contact_request_status)
		ORDER BY cr.created_at DESC`, string(id), filter)
}

// ListForShortlist reads a round's requests for the hirer side.
func (r *ContactRepository) ListForShortlist(ctx context.Context, id domain.ShortlistID) ([]domain.ContactRequest, error) {
	return r.list(ctx, `
		SELECT`+contactColumns+contactFrom+`
		WHERE cr.shortlist_id = $1
		ORDER BY cr.created_at`, string(id))
}

// Respond records the contributor's answer.
//
// email_released_at is stamped only on acceptance. The email itself lives in
// users.email; this column records that release was AUTHORISED and when, which
// is the fact worth keeping — a hirer who saw an address once cannot unsee it,
// so the audit trail matters more than the value.
//
// The WHERE clause requires status='pending', so answering twice affects no
// row and reports a conflict rather than silently changing a decision.
func (r *ContactRepository) Respond(ctx context.Context, t port.Tx, id domain.ContactID, accept bool, at time.Time) (*domain.ContactRequest, error) {
	status := domain.ContactDeclined
	if accept {
		status = domain.ContactAccepted
	}

	// Both parameters are cast explicitly. Used bare, $2 is inferred as the
	// enum in the SET and as text in the comparison, and $3 as timestamptz in
	// one branch and unknown in the other — Postgres refuses to deduce two
	// types for one parameter.
	cr, err := scanContact(r.db.q(t).QueryRow(ctx, `
		UPDATE contact_requests cr
		SET status            = $2::contact_request_status,
		    responded_at      = $3::timestamptz,
		    email_released_at = CASE WHEN $2::contact_request_status = 'accepted' THEN $3::timestamptz END
		WHERE cr.id = $1 AND cr.status = 'pending'
		RETURNING cr.id, cr.shortlist_id, cr.user_id, cr.organization_id, cr.requested_by,
		          cr.status, cr.tentative_result_date, cr.responded_at, cr.email_released_at,
		          cr.expires_at, cr.created_at, '', false, false, '', '', ''`,
		string(id), string(status), at))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("responding to contact request %s", id))
	}

	// RETURNING cannot reach a joined table, so the organization is read back
	// rather than left blank: the response to an answer shows the same company
	// the request did.
	if err := r.db.q(t).QueryRow(ctx,
		`SELECT coalesce(name, ''), (verified_at IS NOT NULL), (payment_verified_at IS NOT NULL)
		 FROM organizations WHERE id = $1`, string(cr.OrganizationID),
	).Scan(&cr.OrganizationName, &cr.OrganizationVerified, &cr.PaymentVerified); err != nil {
		return nil, translate(err, "reading the requesting organization")
	}
	return cr, nil
}

// ExpireStale lapses unanswered requests.
//
// Only pending ones. An accepted request whose expiry passes is not expired —
// the email was released and that cannot be undone by a clock.
func (r *ContactRepository) ExpireStale(ctx context.Context, now time.Time) (int, error) {
	tag, err := r.db.pool.Exec(ctx, `
		UPDATE contact_requests
		SET status = 'expired'
		WHERE status = 'pending' AND expires_at <= $1`, now)
	if err != nil {
		return 0, translate(err, "expiring contact requests")
	}
	return int(tag.RowsAffected()), nil
}

func (r *ContactRepository) list(ctx context.Context, query string, args ...any) ([]domain.ContactRequest, error) {
	rows, err := r.db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, translate(err, "listing contact requests")
	}
	defer rows.Close()

	var out []domain.ContactRequest
	for rows.Next() {
		cr, err := scanContact(rows)
		if err != nil {
			return nil, translate(err, "scanning contact request")
		}
		out = append(out, *cr)
	}
	return out, translate(rows.Err(), "listing contact requests")
}

func scanContact(row rowScanner) (*domain.ContactRequest, error) {
	var cr domain.ContactRequest
	if err := row.Scan(&cr.ID, &cr.ShortlistID, &cr.UserID, &cr.OrganizationID, &cr.RequestedBy,
		&cr.Status, &cr.TentativeResultDate, &cr.RespondedAt, &cr.EmailReleasedAt,
		&cr.ExpiresAt, &cr.CreatedAt, &cr.OrganizationName, &cr.OrganizationVerified, &cr.PaymentVerified,
		&cr.DisplayName, &cr.GitHubLogin, &cr.Email); err != nil {
		return nil, err
	}
	return &cr, nil
}
