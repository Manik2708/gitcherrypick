package postgres

import (
	"context"
	"fmt"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// ProfileRepository owns what a contributor says about themselves (ADR-0018).
//
// Separate from UserRepository because the two hold different things with
// different lifetimes: `users` is what GitHub supplies and a sign-in callback
// overwrites, this is typed by the person and must survive every one of them.
type ProfileRepository struct{ db *DB }

// Profiles returns the contributor-profile repository.
func (db *DB) Profiles() *ProfileRepository { return &ProfileRepository{db: db} }

var _ port.ProfileRepository = (*ProfileRepository)(nil)

// WorkPreferences returns what shape of work somebody will take.
//
// A contributor who has never opened the form gets a ZERO-VALUED row rather
// than a not-found: everybody starts here, and absence is not an error. The
// zero value is also the safe one — every flag false, so a profile nobody
// filled in surfaces nobody (ADR-0018 §12).
func (r *ProfileRepository) WorkPreferences(ctx context.Context, id domain.UserID) (*domain.WorkPreferences, error) {
	out := domain.WorkPreferences{UserID: id}
	err := r.db.pool.QueryRow(ctx, `
		SELECT open_to_remote, open_to_internships, open_to_onsite, open_to_contract,
		       coalesce(current_country, ''), office_yoe,
		       coalesce(first_pr_url, ''), coalesce(latest_pr_url, ''),
		       first_pr_authored_at, latest_pr_authored_at, first_pr_verified_at
		  FROM user_work_preferences
		 WHERE user_id = $1`, string(id),
	).Scan(&out.OpenToRemote, &out.OpenToInternships, &out.OpenToOnsite,
		&out.OpenToContract, &out.CurrentCountry, &out.OfficeYOE,
		&out.FirstPRURL, &out.LatestPRURL,
		&out.FirstPRAuthoredAt, &out.LatestPRAuthoredAt, &out.FirstPRVerifiedAt)
	if err != nil {
		if isNotFound(err) {
			// Stated stays false. A row's ABSENCE is the only way to tell
			// "never asked" from "asked, and the answer was no to everything"
			// — and only the first should be prompted.
			return &out, nil
		}
		return nil, translate(err, fmt.Sprintf("work preferences for %s", id))
	}
	out.Stated = true
	return &out, nil
}

// SaveWorkPreferences replaces them.
//
// An upsert, because the endpoint is a PUT and a profile form submits every
// field it shows. A partial write would leave a flag set that the person had
// just cleared.
func (r *ProfileRepository) SaveWorkPreferences(ctx context.Context, t port.Tx, w *domain.WorkPreferences) error {
	_, err := r.db.q(t).Exec(ctx, `
		INSERT INTO user_work_preferences
		    (user_id, open_to_remote, open_to_internships, open_to_onsite,
		     open_to_contract, current_country, office_yoe,
		     first_pr_url, latest_pr_url)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7,
		        NULLIF($8, ''), NULLIF($9, ''))
		ON CONFLICT (user_id) DO UPDATE SET
		    open_to_remote      = excluded.open_to_remote,
		    open_to_internships = excluded.open_to_internships,
		    open_to_onsite      = excluded.open_to_onsite,
		    open_to_contract    = excluded.open_to_contract,
		    current_country     = excluded.current_country,
		    office_yoe          = excluded.office_yoe,
		    latest_pr_url       = excluded.latest_pr_url,
		    -- WRITE-ONCE. coalesce keeps whatever is already there, so a second
		    -- value cannot overwrite the first even if one reaches this far.
		    -- The service refuses it earlier with a readable error; this is the
		    -- guarantee that does not depend on anybody remembering to.
		    first_pr_url        = coalesce(user_work_preferences.first_pr_url,
		                                   excluded.first_pr_url),
		    -- The dates follow their URLs. The latest one is editable, so a new
		    -- URL means the old authored date is wrong and must not survive it;
		    -- the first one is write-once, so its date is kept exactly as
		    -- coalesce keeps the URL. Writing them here rather than leaving
		    -- them to the verification pass is what stops a changed latest URL
		    -- carrying the previous pull request's date until a job catches up.
		    latest_pr_authored_at = CASE
		        WHEN user_work_preferences.latest_pr_url IS DISTINCT FROM excluded.latest_pr_url
		        THEN NULL ELSE user_work_preferences.latest_pr_authored_at END`,
		string(w.UserID), w.OpenToRemote, w.OpenToInternships, w.OpenToOnsite,
		w.OpenToContract, w.CurrentCountry, w.OfficeYOE,
		w.FirstPRURL, w.LatestPRURL)
	return translate(err, fmt.Sprintf("saving work preferences for %s", w.UserID))
}

// Compensation returns what a contributor expects to be paid.
//
// A HIRER NEVER READS THIS (ADR-0018 §2). No hirer-facing query calls it, and
// the reason it is its own method against its own table is so that none can
// reach it by accident — a query that widened to read work preferences cannot
// pick this up on the way past.
func (r *ProfileRepository) Compensation(ctx context.Context, id domain.UserID) (*domain.Compensation, error) {
	out := domain.Compensation{UserID: id}
	err := r.db.pool.QueryRow(ctx, `
		SELECT coalesce(currency, ''), hourly_rate, yearly_amount
		  FROM user_compensation
		 WHERE user_id = $1`, string(id),
	).Scan(&out.Currency, &out.HourlyRate, &out.YearlyAmount)
	if err != nil {
		if isNotFound(err) {
			// Nothing stated is not an error, and is different from zero.
			return &out, nil
		}
		return nil, translate(err, fmt.Sprintf("compensation for %s", id))
	}
	return &out, nil
}

// SaveCompensation replaces it.
func (r *ProfileRepository) SaveCompensation(ctx context.Context, t port.Tx, c *domain.Compensation) error {
	_, err := r.db.q(t).Exec(ctx, `
		INSERT INTO user_compensation (user_id, currency, hourly_rate, yearly_amount)
		VALUES ($1, NULLIF($2, ''), $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET
		    currency      = excluded.currency,
		    hourly_rate   = excluded.hourly_rate,
		    yearly_amount = excluded.yearly_amount`,
		string(c.UserID), c.Currency, c.HourlyRate, c.YearlyAmount)
	return translate(err, fmt.Sprintf("saving compensation for %s", c.UserID))
}

// SaveVerifiedPRDates records what a verification pass established (ADR-0019 §7).
//
// Separate from SaveWorkPreferences because the two have different authors:
// that one writes what the contributor typed, this writes what we checked. The
// retry job calls this and holds nothing the person typed.
//
// It never INSERTS. A verification for somebody with no preferences row is a
// verification of nothing, and creating a row here would manufacture a profile
// the contributor never filled in — which `Stated` is specifically there to
// distinguish.
func (r *ProfileRepository) SaveVerifiedPRDates(ctx context.Context, t port.Tx, d port.VerifiedPRDates) error {
	_, err := r.db.q(t).Exec(ctx, `
		UPDATE user_work_preferences
		   SET first_pr_authored_at  = coalesce($2, first_pr_authored_at),
		       latest_pr_authored_at = coalesce($3, latest_pr_authored_at),
		       first_pr_verified_at  = coalesce($4, first_pr_verified_at)
		 WHERE user_id = $1`,
		string(d.UserID), d.FirstAuthoredAt, d.LatestAuthoredAt, d.VerifiedAt)
	return translate(err, fmt.Sprintf("saving verified pull request dates for %s", d.UserID))
}

// PendingVerification drains the retry queue: a first pull request stated but
// never confirmed.
//
// Oldest first, so somebody whose save failed during an outage last week is not
// stuck behind everybody who saved since. Bounded by the caller, because this
// is a queue drain and a job that read every row would be one query away from
// being a report.
func (r *ProfileRepository) PendingVerification(ctx context.Context, limit int) ([]domain.UserID, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT user_id
		  FROM user_work_preferences
		 WHERE first_pr_url IS NOT NULL
		   AND first_pr_verified_at IS NULL
		 ORDER BY updated_at
		 LIMIT $1`, limit)
	if err != nil {
		return nil, translate(err, "listing profiles awaiting verification")
	}
	defer rows.Close()

	var out []domain.UserID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, translate(err, "scanning a profile awaiting verification")
		}
		out = append(out, domain.UserID(id))
	}
	return out, translate(rows.Err(), "listing profiles awaiting verification")
}
