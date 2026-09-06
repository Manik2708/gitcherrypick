package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// UserRepository is the contributor side of storage.
//
// Contributors exist only through GitHub (ADR-0002), so a user row and its
// GitHub identity are written together and read together — a user without an
// identity is a state this repository cannot produce.
type UserRepository struct{ db *DB }

// Users returns the contributor repository.
func (db *DB) Users() *UserRepository { return &UserRepository{db: db} }

var _ port.UserRepository = (*UserRepository)(nil)

// AvailabilityWindow is how long a stated availability stays visible to hirers
// before it lapses (ADR-0002 §6).
//
// It lives here because SetAvailability is the only place it is applied, and
// because a constant in the repository cannot drift from the SQL that uses it.
const AvailabilityWindow = 15 * 24 * time.Hour

// The projection every read shares.
//
// One constant rather than a string per method: a column added to users has to
// appear in scanContributor as well, and having both in one place is what makes
// that obvious rather than a runtime scan error in whichever method was missed.
const contributorColumns = `
	u.id, u.display_name,
	coalesce(u.email, ''), coalesce(u.avatar_url, ''),
	coalesce(u.bio, ''), coalesce(u.location, ''),
	u.overall_score, u.generalist_score, u.created_at,
	i.github_user_id, i.github_login,
	a.status, a.expires_at, a.reminded_at, a.updated_at`

const contributorFrom = `
	FROM users u
	JOIN user_github_identities i ON i.user_id = u.id
	LEFT JOIN user_availability a ON a.user_id = u.id`

// ByID reads one contributor.
func (r *UserRepository) ByID(ctx context.Context, id domain.UserID) (*domain.Contributor, error) {
	row := r.db.pool.QueryRow(ctx,
		`SELECT`+contributorColumns+contributorFrom+`
		 WHERE u.id = $1 AND u.deactivated_at IS NULL`, string(id))

	c, err := scanContributor(row)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("user %s", id))
	}
	return c, nil
}

// ByGitHubUserID resolves the identity an OAuth callback carries.
//
// Keyed on the numeric id, never the login: a login can be changed or given to
// somebody else, and treating it as identity would hand one contributor's
// account to whoever claimed their old name.
func (r *UserRepository) ByGitHubUserID(ctx context.Context, githubUserID int64) (*domain.Contributor, error) {
	row := r.db.pool.QueryRow(ctx,
		`SELECT`+contributorColumns+contributorFrom+`
		 WHERE i.github_user_id = $1 AND i.user_id IS NOT NULL AND u.deactivated_at IS NULL`,
		githubUserID)

	c, err := scanContributor(row)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("github user %d", githubUserID))
	}
	return c, nil
}

// Create writes the user and its GitHub identity.
//
// Both or neither: it takes a Tx so the caller can join it to the session
// write that follows, and because a user row with no identity could never be
// signed into again.
//
// No availability row is written. A new contributor is invisible to search
// until they choose to be visible (ADR-0002), and defaulting them to
// "not looking" would be a statement they never made.
func (r *UserRepository) Create(ctx context.Context, t port.Tx, c *domain.Contributor) (*domain.Contributor, error) {
	q := r.db.q(t)

	id := c.ID
	if id == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generating user id: %w", err)
		}
		id = domain.UserID(generated.String())
	}

	var created domain.Contributor
	err := q.QueryRow(ctx, `
		INSERT INTO users (id, display_name, email, avatar_url, bio, location)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''))
		RETURNING id, display_name, coalesce(email, ''), coalesce(avatar_url, ''),
		          coalesce(bio, ''), coalesce(location, ''), created_at`,
		string(id), c.DisplayName, c.Email, c.AvatarURL, c.Bio, c.Location,
	).Scan(&created.ID, &created.DisplayName, &created.Email, &created.AvatarURL,
		&created.Bio, &created.Location, &created.CreatedAt)
	if err != nil {
		return nil, translate(err, "creating user")
	}

	identityID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating identity id: %w", err)
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO user_github_identities (id, user_id, github_user_id, github_login)
		VALUES ($1, $2, $3, $4)`,
		identityID.String(), string(created.ID), c.GitHubUserID, c.GitHubLogin); err != nil {
		return nil, translate(err, "creating github identity")
	}

	created.GitHubUserID = c.GitHubUserID
	created.GitHubLogin = c.GitHubLogin
	return &created, nil
}

// RefreshGitHubLogin updates the handle attached to a GitHub identity.
//
// The numeric id is the identity and never changes; the login is a display
// handle that does. Keeping the first one seen would show a name the
// contributor no longer answers to.
func (r *UserRepository) RefreshGitHubLogin(ctx context.Context, githubUserID int64, login string) error {
	if login == "" {
		return nil
	}
	_, err := r.db.pool.Exec(ctx, `
		UPDATE user_github_identities SET github_login = $2
		WHERE github_user_id = $1 AND github_login IS DISTINCT FROM $2`,
		githubUserID, login)
	return translate(err, fmt.Sprintf("refreshing the login for %d", githubUserID))
}

// SetAvailability states availability and resets the 15-day window.
//
// Upsert rather than insert-or-update: a contributor may have no row yet, and
// a returning one must keep the same primary key so nothing that referenced
// them is orphaned.
func (r *UserRepository) SetAvailability(ctx context.Context, id domain.UserID, status domain.AvailabilityStatus) (*domain.Availability, error) {
	var a domain.Availability
	err := r.db.pool.QueryRow(ctx, `
		INSERT INTO user_availability (user_id, status, expires_at, updated_at)
		VALUES ($1, $2, now() + $3::interval, now())
		ON CONFLICT (user_id) DO UPDATE
		  SET status     = EXCLUDED.status,
		      expires_at = EXCLUDED.expires_at,
		      updated_at = now(),
		      -- Cleared so the next lapse earns a fresh reminder. Leaving it
		      -- set would silence the warning for a contributor who renewed
		      -- once and then went quiet again.
		      reminded_at = NULL
		RETURNING status, expires_at, updated_at, reminded_at`,
		string(id), string(status), AvailabilityWindow.String(),
	).Scan(&a.Status, &a.ExpiresAt, &a.LastSetAt, &a.RemindedAt)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("setting availability for %s", id))
	}
	return &a, nil
}

// LapsingSoon lists contributors whose window closes within the given duration
// and who have not been reminded since they last set it.
//
// The reminded_at comparison is against updated_at rather than a fixed date:
// a contributor who renewed after being reminded becomes eligible again, which
// is what makes the reminder per-window rather than once per lifetime.
func (r *UserRepository) LapsingSoon(ctx context.Context, within time.Duration, limit int) ([]domain.Contributor, error) {
	rows, err := r.db.pool.Query(ctx,
		`SELECT`+contributorColumns+contributorFrom+`
		 WHERE u.deactivated_at IS NULL
		   AND a.expires_at IS NOT NULL
		   AND a.status <> 'not_looking'
		   AND a.expires_at > now()
		   AND a.expires_at <= now() + $1::interval
		   AND (a.reminded_at IS NULL OR a.reminded_at < a.updated_at)
		 ORDER BY a.expires_at
		 LIMIT $2`, within.String(), limit)
	if err != nil {
		return nil, translate(err, "listing lapsing contributors")
	}
	defer rows.Close()

	var out []domain.Contributor
	for rows.Next() {
		c, err := scanContributor(rows)
		if err != nil {
			return nil, translate(err, "scanning lapsing contributor")
		}
		out = append(out, *c)
	}
	return out, translate(rows.Err(), "listing lapsing contributors")
}

// MarkReminded records that the pre-lapse warning was sent.
func (r *UserRepository) MarkReminded(ctx context.Context, ids []domain.UserID) error {
	if len(ids) == 0 {
		return nil
	}
	raw := make([]string, len(ids))
	for i, id := range ids {
		raw[i] = string(id)
	}
	_, err := r.db.pool.Exec(ctx,
		`UPDATE user_availability SET reminded_at = now() WHERE user_id = ANY($1)`, raw)
	return translate(err, "marking reminders sent")
}

// SetUserScores writes both user-level numbers.
//
// Both, in one statement, because ADR-0007 computes them in the same
// transaction from the same ordered list: they cannot disagree, and a method
// that could write one without the other is how they eventually would.
func (r *UserRepository) SetUserScores(ctx context.Context, t port.Tx, id domain.UserID, overall, generalist *float64) error {
	tag, err := r.db.q(t).Exec(ctx,
		`UPDATE users SET overall_score = $2, generalist_score = $3, updated_at = now()
		 WHERE id = $1`, string(id), overall, generalist)
	if err != nil {
		return translate(err, fmt.Sprintf("setting scores for %s", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("setting scores for %s: %w", id, port.ErrNotFound)
	}
	return nil
}

// scanContributor reads the shared projection.
//
// Availability is LEFT JOINed, so its columns are null for a contributor who
// has never stated any — which is different from one who said "not looking",
// and the domain keeps them different by leaving the pointer nil.
func scanContributor(row pgx.Row) (*domain.Contributor, error) {
	var (
		c          domain.Contributor
		status     *string
		expiresAt  *time.Time
		remindedAt *time.Time
		lastSetAt  *time.Time
	)
	err := row.Scan(
		&c.ID, &c.DisplayName, &c.Email, &c.AvatarURL, &c.Bio, &c.Location,
		&c.OverallScore, &c.GeneralistScore, &c.CreatedAt,
		&c.GitHubUserID, &c.GitHubLogin,
		&status, &expiresAt, &remindedAt, &lastSetAt,
	)
	if err != nil {
		return nil, err
	}
	if status != nil {
		c.Availability = &domain.Availability{
			Status:     domain.AvailabilityStatus(*status),
			ExpiresAt:  expiresAt,
			RemindedAt: remindedAt,
		}
		if lastSetAt != nil {
			c.Availability.LastSetAt = *lastSetAt
		}
	}
	return &c, nil
}
