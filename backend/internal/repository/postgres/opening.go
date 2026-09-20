package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// OpeningRepository owns published adverts (ADR-0020).
//
// The property this file holds is the one in Matching: A STATED BAR IS NOT
// CLEARED BY AN ABSENT SCORE. Every comparison below is written so that a null
// on the contributor's side fails, because these figures are evidence the
// platform produced rather than anything somebody typed — and an absent one is
// not a small one, it is a claim that was never submitted.
type OpeningRepository struct{ db *DB }

// Openings returns the advert repository.
func (db *DB) Openings() *OpeningRepository { return &OpeningRepository{db: db} }

var _ port.OpeningRepository = (*OpeningRepository)(nil)

const openingColumns = `
	o.id, o.role_id, o.min_overall_score, o.min_generalist_score, o.min_oss_yoe,
	o.published_at, o.published_by, o.withdrawn_at,
	o.created_by, o.created_at, o.updated_at`

func scanOpening(row interface{ Scan(...any) error }) (*domain.Opening, error) {
	var (
		out         domain.Opening
		id, roleID  string
		publishedBy *string
		createdBy   string
	)
	if err := row.Scan(&id, &roleID, &out.MinOverallScore, &out.MinGeneralistScore,
		&out.MinOSSYOE, &out.PublishedAt, &publishedBy, &out.WithdrawnAt,
		&createdBy, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return nil, err
	}
	out.ID, out.RoleID = domain.OpeningID(id), domain.RoleID(roleID)
	out.CreatedBy = domain.HirerID(createdBy)
	if publishedBy != nil {
		h := domain.HirerID(*publishedBy)
		out.PublishedBy = &h
	}
	return &out, nil
}

// ByRole reads the advert on a role.
func (r *OpeningRepository) ByRole(ctx context.Context, id domain.RoleID) (*domain.Opening, error) {
	return r.byRole(ctx, nil, id)
}

func (r *OpeningRepository) byRole(ctx context.Context, t port.Tx, id domain.RoleID) (*domain.Opening, error) {
	out, err := scanOpening(r.db.q(t).QueryRow(ctx,
		`SELECT`+openingColumns+` FROM role_openings o WHERE o.role_id = $1`, string(id)))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("reading the opening on role %s", id))
	}
	if err := r.hydrateSkills(ctx, t, []*domain.Opening{out}); err != nil {
		return nil, err
	}
	return out, nil
}

// hydrateSkills fills in the per-skill bar, resolved to slugs.
//
// One query for the whole set: a bar naming a uuid is a bar nobody can read,
// and a list of twenty openings would otherwise be twenty round trips to say so.
func (r *OpeningRepository) hydrateSkills(ctx context.Context, t port.Tx, openings []*domain.Opening) error {
	if len(openings) == 0 {
		return nil
	}
	byID := make(map[domain.OpeningID]*domain.Opening, len(openings))
	ids := make([]string, 0, len(openings))
	for _, o := range openings {
		byID[o.ID] = o
		ids = append(ids, string(o.ID))
		o.Skills = []domain.OpeningSkill{}
	}

	rows, err := r.db.q(t).Query(ctx, `
		SELECT os.opening_id, os.skill_id, s.slug, s.name, os.min_score
		  FROM role_opening_skills os
		  JOIN skills s ON s.id = os.skill_id
		 WHERE os.opening_id = ANY($1::uuid[])
		 ORDER BY s.slug`, ids)
	if err != nil {
		return translate(err, "reading opening skills")
	}
	defer rows.Close()

	for rows.Next() {
		var openingID, skillID string
		var skill domain.OpeningSkill
		if err := rows.Scan(&openingID, &skillID, &skill.Slug, &skill.Name, &skill.MinScore); err != nil {
			return translate(err, "scanning an opening skill")
		}
		skill.SkillID = domain.SkillID(skillID)
		if o := byID[domain.OpeningID(openingID)]; o != nil {
			o.Skills = append(o.Skills, skill)
		}
	}
	return translate(rows.Err(), "reading opening skills")
}

// Save creates or replaces the bar. It NEVER publishes: drafting the numbers
// and committing them in public are separate acts (ADR-0020 §9), and an upsert
// that also went live would make the authority setting unenforceable for
// anybody who could write one.
func (r *OpeningRepository) Save(ctx context.Context, t port.Tx, o *domain.Opening) (*domain.Opening, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating opening id: %w", err)
	}

	var openingID string
	err = r.db.q(t).QueryRow(ctx, `
		INSERT INTO role_openings
		    (id, role_id, min_overall_score, min_generalist_score, min_oss_yoe, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (role_id) DO UPDATE SET
		    min_overall_score    = excluded.min_overall_score,
		    min_generalist_score = excluded.min_generalist_score,
		    min_oss_yoe          = excluded.min_oss_yoe
		RETURNING id`,
		id.String(), string(o.RoleID), o.MinOverallScore, o.MinGeneralistScore,
		o.MinOSSYOE, string(o.CreatedBy)).Scan(&openingID)
	if err != nil {
		return nil, translate(err, "saving the opening")
	}

	// Replace the skill bar wholesale. The form submits every row it shows, so
	// a merge would leave a skill somebody had just removed.
	if _, err := r.db.q(t).Exec(ctx,
		`DELETE FROM role_opening_skills WHERE opening_id = $1`, openingID); err != nil {
		return nil, translate(err, "clearing the skill bar")
	}
	for _, s := range o.Skills {
		if _, err := r.db.q(t).Exec(ctx, `
			INSERT INTO role_opening_skills (opening_id, skill_id, min_score)
			VALUES ($1, $2, $3) ON CONFLICT (opening_id, skill_id) DO UPDATE
			SET min_score = excluded.min_score`,
			openingID, string(s.SkillID), s.MinScore); err != nil {
			return nil, translate(err, "writing a skill bar")
		}
	}
	return r.byRole(ctx, t, o.RoleID)
}

// Publish stamps it live, and clears any earlier withdrawal.
//
// published_at is coalesced rather than overwritten: it is the only record of
// how long something has been advertised, and re-publishing after a withdrawal
// should not make an old job look new.
func (r *OpeningRepository) Publish(ctx context.Context, t port.Tx, id domain.RoleID, by domain.HirerID, at time.Time) (*domain.Opening, error) {
	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE role_openings
		   SET published_at = coalesce(published_at, $2),
		       published_by = coalesce(published_by, $3),
		       withdrawn_at = NULL
		 WHERE role_id = $1`, string(id), at, string(by))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("publishing the opening on role %s", id))
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("role %s has no opening: %w", id, port.ErrNotFound)
	}
	return r.byRole(ctx, t, id)
}

// Withdraw takes it down without deleting it.
func (r *OpeningRepository) Withdraw(ctx context.Context, t port.Tx, id domain.RoleID, at time.Time) (*domain.Opening, error) {
	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE role_openings SET withdrawn_at = $2
		 WHERE role_id = $1 AND published_at IS NOT NULL AND withdrawn_at IS NULL`,
		string(id), at)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("withdrawing the opening on role %s", id))
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("role %s has no live opening: %w", id, port.ErrConflict)
	}
	return r.byRole(ctx, t, id)
}

// liveOpenings is every advert a contributor could conceivably see: published,
// not withdrawn, on a role that is still open.
//
// The role's status is part of it rather than a separate check, because
// closing a role must take its advert down — otherwise a withdrawn job leaves
// a posting behind and a contributor reads about work nobody is offering.
const liveOpenings = `
	live AS (
	    SELECT o.id AS opening_id, r.id AS role_id, r.organization_id,
	           o.min_overall_score, o.min_generalist_score, o.min_oss_yoe,
	           r.opened_at
	      FROM role_openings o
	      JOIN roles r ON r.id = o.role_id
	     WHERE o.published_at IS NOT NULL
	       AND o.withdrawn_at IS NULL
	       AND r.status = 'open'
	)`

// clearedOpenings is the bar, applied.
//
// EVERY comparison fails on a null from the contributor. That is the whole
// decision of ADR-0020 §5: these figures are evidence the platform produced,
// and somebody with no overall score has not been judged rather than scored
// low. Showing them a role asking for 60 would promise a match that does not
// exist, and the remedy is a claim rather than a filter that looks away.
//
// $1 contributor, $2 now.
const clearedOpenings = `
	cleared AS (
	    SELECT live.*
	      FROM live
	      JOIN roles r ON r.id = live.role_id
	      JOIN users u ON u.id = $1
	      LEFT JOIN user_work_preferences w ON w.user_id = u.id
	      LEFT JOIN user_compensation comp ON comp.user_id = u.id
	     WHERE (live.min_overall_score IS NULL
	            OR (u.overall_score IS NOT NULL
	                AND u.overall_score >= live.min_overall_score))

	       AND (live.min_generalist_score IS NULL
	            OR (u.generalist_score IS NOT NULL
	                AND u.generalist_score >= live.min_generalist_score))

	       -- Verified years, dated from AUTHORSHIP (ADR-0019 §7). An
	       -- unverified first pull request is unknown, and unknown clears
	       -- nothing.
	       AND (live.min_oss_yoe IS NULL
	            OR (w.first_pr_verified_at IS NOT NULL
	                AND w.first_pr_authored_at IS NOT NULL
	                AND w.first_pr_authored_at
	                    <= $2::timestamptz - (live.min_oss_yoe || ' years')::interval))

	       -- Where they are. No stated countries on the role means anywhere;
	       -- a contributor who has not said where they are is admitted only by
	       -- a role that hires anywhere (ADR-0019 §6).
	       AND (NOT EXISTS (SELECT 1 FROM role_eligible_countries c
	                         WHERE c.role_id = live.role_id)
	            OR (coalesce(w.current_country, '') <> ''
	                AND EXISTS (SELECT 1 FROM role_eligible_countries c
	                             WHERE c.role_id = live.role_id
	                               AND c.country = w.current_country)))

	       -- EVERY skill bar, at PRIMARY standing only (ADR-0005). "Not
	       -- exists a bar this person fails" rather than a count, so a bar
	       -- naming a skill they have never claimed fails on absence.
	       AND NOT EXISTS (
	               SELECT 1
	                 FROM role_opening_skills os
	                 LEFT JOIN user_skills us
	                        ON us.skill_id = os.skill_id
	                       AND us.user_id = u.id
	                       AND us.standing = 'primary'
	                WHERE os.opening_id = live.opening_id
	                  AND (us.score IS NULL OR us.score < os.min_score))

	       -- WHAT THEY SAID THEY WOULD PREFER. This is the contributor's OWN
	       -- view, so filtering it by their own preference is the point — it
	       -- is not a gate on who may reach them (ADR-0021 §3).
	       --
	       -- Freelance reads a flag like every other engagement now. It used
	       -- to key off availability_status, which made it the one shape
	       -- expressed differently from the other four and put this CASE arm
	       -- out of step with itself.
	       AND (CASE r.engagement
	                -- A permanent role reads the LOCATION, because "full time"
	                -- alone says nothing about where, and remote and onsite
	                -- are the two flags that do.
	                WHEN 'full_time' THEN
	                    CASE WHEN r.location = 'remote'
	                         THEN coalesce(w.open_to_remote, false)
	                         ELSE coalesce(w.open_to_onsite, false) END
	                WHEN 'contract'   THEN coalesce(w.open_to_contract, false)
	                WHEN 'internship' THEN coalesce(w.open_to_internships, false)
	                WHEN 'freelance'  THEN coalesce(w.open_to_freelance, false)
	            END)

	       -- WHAT THEY EXPECT TO BE PAID. The one place this figure is ever
	       -- used, and it travels no further: it keeps roles paying less than
	       -- they asked out of their way, and no hirer sees it (ADR-0018 §5).
	       --
	       -- A role in another currency is not compared, because comparing it
	       -- needs a rate nobody here has — and hiding a job over an exchange
	       -- rate the platform guessed would be worse than showing it.
	       AND (coalesce(comp.currency, '') = ''
	            OR r.currency IS NULL
	            OR r.currency <> comp.currency
	            OR ((comp.yearly_amount IS NULL OR r.yearly_ctc IS NULL
	                 OR r.yearly_ctc >= comp.yearly_amount)
	            AND (comp.hourly_rate IS NULL OR r.hourly_rate IS NULL
	                 OR r.hourly_rate >= comp.hourly_rate)))
	)`

// Matching answers the contributor's read: what they clear, and how many they
// do not.
//
// ONE query for both numbers. Two could disagree about a row that changed
// between them, and the count is the whole of what a contributor is told about
// the rest — a count that contradicts the list under it is worse than no count.
func (r *OpeningRepository) Matching(ctx context.Context, m port.OpeningMatch) (*port.OpeningResults, error) {
	limit := m.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	offset := m.Offset
	if offset < 0 {
		offset = 0
	}

	rows, err := r.db.pool.Query(ctx, `
	WITH`+liveOpenings+`,
	`+clearedOpenings+`
	SELECT (SELECT count(*) FROM cleared)                              AS matched,
	       (SELECT count(*) FROM live) - (SELECT count(*) FROM cleared) AS missed,
	       `+openingColumns+`,
	       org.name
	  FROM cleared
	  JOIN role_openings o ON o.id = cleared.opening_id
	  JOIN organizations org ON org.id = cleared.organization_id
	 ORDER BY cleared.opened_at DESC NULLS LAST
	 LIMIT $3 OFFSET $4`,
		string(m.UserID), r.db.now(), limit, offset)
	if err != nil {
		return nil, translate(err, "matching openings")
	}
	defer rows.Close()

	out := port.OpeningResults{Openings: []domain.Opening{}}
	refs := []*domain.Opening{}
	for rows.Next() {
		var (
			o                     domain.Opening
			id, roleID, createdBy string
			publishedBy           *string
		)
		if err := rows.Scan(&out.Matched, &out.Missed,
			&id, &roleID, &o.MinOverallScore, &o.MinGeneralistScore, &o.MinOSSYOE,
			&o.PublishedAt, &publishedBy, &o.WithdrawnAt,
			&createdBy, &o.CreatedAt, &o.UpdatedAt, &o.OrganizationName); err != nil {
			return nil, translate(err, "scanning an opening")
		}
		o.ID, o.RoleID = domain.OpeningID(id), domain.RoleID(roleID)
		o.CreatedBy = domain.HirerID(createdBy)
		if publishedBy != nil {
			h := domain.HirerID(*publishedBy)
			o.PublishedBy = &h
		}
		out.Openings = append(out.Openings, o)
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err, "matching openings")
	}

	// An empty page still has to report the counts, and the row-carried ones
	// are gone with the rows. Asked again rather than inferred, because "none
	// matched" and "none exist" are the two states this read exists to tell
	// apart (ADR-0020 §6).
	if len(out.Openings) == 0 {
		if err := r.countsOnly(ctx, m.UserID, &out); err != nil {
			return nil, err
		}
		return &out, nil
	}

	for i := range out.Openings {
		refs = append(refs, &out.Openings[i])
	}
	if err := r.hydrateSkills(ctx, nil, refs); err != nil {
		return nil, err
	}
	return &out, nil
}

// countsOnly answers the two numbers when the page is empty.
func (r *OpeningRepository) countsOnly(ctx context.Context, user domain.UserID, out *port.OpeningResults) error {
	err := r.db.pool.QueryRow(ctx, `
	WITH`+liveOpenings+`,
	`+clearedOpenings+`
	SELECT (SELECT count(*) FROM cleared),
	       (SELECT count(*) FROM live) - (SELECT count(*) FROM cleared)`,
		string(user), r.db.now()).Scan(&out.Matched, &out.Missed)
	return translate(err, "counting openings")
}
