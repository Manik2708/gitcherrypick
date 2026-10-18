package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// Candidates on a role, and the two-phase disclosure (ADR-0023).
//
// There is no round. A shortlist was always one-to-one with a role, so the
// people it held hang off the role itself — which is also the only thing a
// contact request could ever have been about.
//
// STAGING DISCLOSES NOTHING. Contacting is the irreversible act: it writes one
// contact request per staged candidate in one transaction and stamps them. The
// rule it serves is that a contacted contributor can never be un-contacted —
// once somebody has been told an organization is interested, that is a fact —
// and a rule that strict is only tolerable if there is a moment before it
// binds.
//
// Candidates belong to the ORGANIZATION through the role, not to the recruiter
// who staged them: a list that vanished when a recruiter left would be worse
// than useless.

// ContactWindow is how long a contributor has to answer before a request
// lapses.
const ContactWindow = 30 * 24 * time.Hour

// OverdueFlagRatio is the share of an organization's contacted roles that must
// be past their promised date before admins are told (ADR-0005).
//
// Flagged, never blocked: blocking would punish a hirer for a candidate's
// silence as readily as for their own neglect.
const OverdueFlagRatio = 0.8

// Stage puts somebody on a role and discloses nothing.
//
// No contact request is written here. That is the entire difference between
// this and ADR-0005's original design, and it is what gives a mis-click a
// moment in which it can still be undone.
func (r *RoleRepository) Stage(ctx context.Context, c *domain.RoleCandidate, role domain.RoleID) (*domain.RoleCandidate, error) {
	var out domain.RoleCandidate
	err := r.db.pool.QueryRow(ctx, `
		WITH staged AS (
		    INSERT INTO role_candidates (role_id, user_id, note, added_by)
		    SELECT $1, $2, NULLIF($3, ''), $4
		    -- A closed role takes nobody new. Staging somebody for a withdrawn
		    -- job would only ever mislead them.
		    WHERE EXISTS (SELECT 1 FROM roles WHERE id = $1 AND status <> 'closed')
		    RETURNING user_id, coalesce(note, '') AS note, added_by, notified_at, added_at
		)
		SELECT s.user_id, u.display_name, coalesce(gi.github_login, ''),
		       s.note, `+hirerRefColumns("author")+`, s.notified_at, s.added_at
		FROM staged s
		JOIN users u ON u.id = s.user_id
		JOIN hirer_accounts author ON author.id = s.added_by
		LEFT JOIN user_github_identities gi ON gi.user_id = s.user_id`,
		string(role), string(c.UserID), c.Note, string(c.AddedBy.ID),
	).Scan(scanTargets(
		[]any{&out.UserID, &out.DisplayName, &out.GitHubLogin, &out.Note},
		&out.AddedBy,
		[]any{&out.NotifiedAt, &out.AddedAt})...)
	if err != nil {
		return nil, translate(err, "staging a candidate")
	}
	return &out, nil
}

// Unstage takes somebody off a role.
//
// Permitted only while notified_at IS NULL. Afterwards the contributor was
// told, and deleting the row would destroy the record of a disclosure that
// already happened — so it reports a conflict rather than a not-found.
func (r *RoleRepository) Unstage(ctx context.Context, role domain.RoleID, user domain.UserID) error {
	tag, err := r.db.pool.Exec(ctx,
		`DELETE FROM role_candidates
		 WHERE role_id = $1 AND user_id = $2 AND notified_at IS NULL`,
		string(role), string(user))
	if err != nil {
		return translate(err, "unstaging a candidate")
	}
	if tag.RowsAffected() == 0 {
		var notifiedAt *time.Time
		if err := r.db.pool.QueryRow(ctx,
			`SELECT notified_at FROM role_candidates WHERE role_id = $1 AND user_id = $2`,
			string(role), string(user)).Scan(&notifiedAt); err != nil {
			return translate(err, "unstaging a candidate")
		}
		if notifiedAt != nil {
			// The timestamp travels with the refusal: a hirer told "you cannot
			// remove this" needs to know when the disclosure happened, since
			// that is the fact the rule protects.
			return &port.NotifiedEntryError{NotifiedAt: *notifiedAt}
		}
		return fmt.Errorf("candidate %s: %w", user, port.ErrNotFound)
	}
	return nil
}

// Contact tells every staged candidate, in one transaction.
//
// This is the irreversible act. It writes one contact request per staged
// candidate, stamps them, and records that this role has now announced itself
// — atomically, because a half-contacted role would have told some people and
// not others.
//
// The promised date is COPIED onto each request rather than read back through
// the role, so nothing the company does later can rewrite what a contributor
// was told.
//
// Idempotent by construction: the SELECT matches only notified_at IS NULL, so
// contacting again with nobody new tells nobody.
func (r *RoleRepository) Contact(ctx context.Context, t port.Tx, role domain.RoleID, by domain.HirerID, resultBy time.Time) ([]domain.ContactRequest, error) {
	q := r.db.q(t)

	var status string
	if err := q.QueryRow(ctx,
		`SELECT status FROM roles WHERE id = $1 FOR UPDATE`, string(role)).Scan(&status); err != nil {
		return nil, translate(err, fmt.Sprintf("role %s", role))
	}
	switch domain.RoleStatus(status) {
	case domain.RoleClosed:
		return nil, fmt.Errorf("role %s is closed: %w", role, port.ErrConflict)
	case domain.RoleDraft:
		// A draft is not a commitment (ADR-0019 §4). Telling somebody about a
		// salary nobody has signed off is the thing opening a role exists to
		// prevent.
		return nil, fmt.Errorf("role %s is a draft: %w", role, port.ErrConflict)
	}

	rows, err := q.Query(ctx, `
		WITH staged AS (
		    SELECT c.user_id
		    FROM role_candidates c
		    WHERE c.role_id = $1 AND c.notified_at IS NULL
		    FOR UPDATE
		),
		created AS (
		    INSERT INTO contact_requests
		        (id, role_id, user_id, organization_id, requested_by,
		         status, tentative_result_date, expires_at)
		    SELECT gen_random_uuid(), r.id, staged.user_id, r.organization_id, $2,
		           'pending', $3::date, now() + $4::interval
		    FROM staged
		    JOIN roles r ON r.id = $1
		    RETURNING id, role_id, user_id, organization_id, requested_by,
		              status, tentative_result_date, expires_at
		),
		stamped AS (
		    UPDATE role_candidates
		    SET notified_at = now()
		    WHERE role_id = $1 AND user_id IN (SELECT user_id FROM staged)
		)
		SELECT c.id, c.user_id, c.organization_id,
		       `+hirerRefColumns("author")+`,
		       c.role_id, c.status, c.tentative_result_date, c.expires_at
		FROM created c
		JOIN hirer_accounts author ON author.id = c.requested_by`,
		string(role), string(by), resultBy, ContactWindow.String())
	if err != nil {
		return nil, translate(err, fmt.Sprintf("contacting candidates on role %s", role))
	}
	defer rows.Close()

	var out []domain.ContactRequest
	for rows.Next() {
		var cr domain.ContactRequest
		if err := rows.Scan(scanTargets(
			[]any{&cr.ID, &cr.UserID, &cr.OrganizationID},
			&cr.RequestedBy,
			[]any{&cr.RoleID, &cr.Status, &cr.TentativeResultDate, &cr.ExpiresAt})...); err != nil {
			return nil, translate(err, "scanning contact request")
		}
		out = append(out, cr)
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err, "contacting candidates")
	}

	// Stamped even when nobody new was told: the first contact is what makes
	// the role answerable for its promised date, and a second pass must not
	// move that date forward.
	if _, err := q.Exec(ctx, `
		UPDATE roles
		SET first_contacted_at = coalesce(first_contacted_at, now()), updated_at = now()
		WHERE id = $1`, string(role)); err != nil {
		return nil, translate(err, "stamping the role's first contact")
	}
	return out, nil
}

// OverdueRatios drives the daily flagging job.
//
// Only roles that have ANNOUNCED THEMSELVES count — first_contacted_at is set.
// A job nobody was told about promised nothing to anyone, so counting one would
// flag an organization for failing to deliver on a search it never announced;
// a closed role is finished, so it cannot be late.
//
// The promised date comes off the CONTACT REQUESTS rather than the role: it is
// what the people who were told were actually promised, and the latest of them
// is the one still outstanding.
func (r *RoleRepository) OverdueRatios(ctx context.Context, now time.Time) ([]port.OverdueOrg, error) {
	rows, err := r.db.pool.Query(ctx, `
		WITH promised AS (
		    SELECT r.id, r.organization_id,
		           max(cr.tentative_result_date) AS result_by
		      FROM roles r
		      JOIN contact_requests cr ON cr.role_id = r.id
		     WHERE r.first_contacted_at IS NOT NULL AND r.status <> 'closed'
		     GROUP BY r.id, r.organization_id
		)
		SELECT o.id, o.name,
		       count(*) AS open_roles,
		       count(*) FILTER (WHERE p.result_by < $1::date) AS overdue,
		       (count(*) FILTER (WHERE p.result_by < $1::date))::float8 / count(*) AS ratio
		FROM promised p
		JOIN organizations o ON o.id = p.organization_id
		GROUP BY o.id, o.name
		ORDER BY ratio DESC, o.name`, now)
	if err != nil {
		return nil, translate(err, "computing overdue ratios")
	}
	defer rows.Close()

	var out []port.OverdueOrg
	for rows.Next() {
		var o port.OverdueOrg
		if err := rows.Scan(&o.OrganizationID, &o.Name, &o.OpenRoles, &o.Overdue, &o.Ratio); err != nil {
			return nil, translate(err, "scanning overdue ratio")
		}
		out = append(out, o)
	}
	return out, translate(rows.Err(), "computing overdue ratios")
}

// ContactRequestsForRole is what each contacted person answered.
func (r *RoleRepository) ContactRequestsForRole(ctx context.Context, role domain.RoleID) ([]domain.ContactRequest, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT c.id, c.user_id, c.organization_id, `+hirerRefColumns("author")+`,
		       c.role_id, c.status, c.tentative_result_date, c.expires_at,
		       c.responded_at, c.email_released_at,
		       u.display_name, coalesce(gi.github_login, ''),
		       -- The address only where the contributor released it, off the
		       -- same stamp every other read of it uses.
		       CASE WHEN c.email_released_at IS NOT NULL THEN coalesce(u.email, '') ELSE '' END
		FROM contact_requests c
		JOIN hirer_accounts author ON author.id = c.requested_by
		JOIN users u ON u.id = c.user_id
		LEFT JOIN user_github_identities gi ON gi.user_id = c.user_id
		WHERE c.role_id = $1
		ORDER BY c.created_at`, string(role))
	if err != nil {
		return nil, translate(err, "reading contact requests for a role")
	}
	defer rows.Close()

	var out []domain.ContactRequest
	for rows.Next() {
		var cr domain.ContactRequest
		if err := rows.Scan(scanTargets(
			[]any{&cr.ID, &cr.UserID, &cr.OrganizationID},
			&cr.RequestedBy,
			[]any{&cr.RoleID, &cr.Status, &cr.TentativeResultDate, &cr.ExpiresAt,
				&cr.RespondedAt, &cr.EmailReleasedAt,
				&cr.DisplayName, &cr.GitHubLogin, &cr.Email})...); err != nil {
			return nil, translate(err, "scanning a contact request")
		}
		out = append(out, cr)
	}
	return out, translate(rows.Err(), "reading contact requests for a role")
}
