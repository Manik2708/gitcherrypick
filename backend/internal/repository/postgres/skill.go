package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// SkillRepository owns the catalogue, standings, and the (user, PR, skill)
// links standing is derived from.
//
// The catalogue is curated, never free text: otherwise the population claiming
// "Go" ranks separately from the one claiming "Golang", and anyone can mint a
// skill in which they are trivially top-ranked (ADR-0003).
type SkillRepository struct{ db *DB }

// Skills returns the catalogue repository.
func (db *DB) Skills() *SkillRepository { return &SkillRepository{db: db} }

var _ port.SkillRepository = (*SkillRepository)(nil)

// PrimaryThreshold is the number of DISTINCT SCORED PRs at which a skill
// promotes. Five or more is primary and ranked; one to four is secondary,
// visible and score-contributing but never ranked (ADR-0003).
const PrimaryThreshold = 5

// The projection every read shares. skillColumnsBare is the same list without
// the table alias, for RETURNING — deriving one from the other by slicing off a
// prefix strips only the first occurrence, which is a bug that compiles.
const (
	skillColumns     = `s.id, s.slug, s.name, s.description, s.category, s.scoring_mode, s.is_active`
	skillColumnsBare = `id, slug, name, description, category, scoring_mode, is_active`
)

// BySlug reads one catalogue entry, with its aliases.
func (r *SkillRepository) BySlug(ctx context.Context, slug string) (*domain.Skill, error) {
	var s domain.Skill
	err := r.db.pool.QueryRow(ctx,
		`SELECT `+skillColumns+`,
		        coalesce(array_agg(a.alias ORDER BY a.alias) FILTER (WHERE a.alias IS NOT NULL), '{}')
		 FROM skills s
		 LEFT JOIN skill_aliases a ON a.skill_id = s.id
		 WHERE s.slug = $1
		 GROUP BY s.id`, slug,
	).Scan(&s.ID, &s.Slug, &s.Name, &s.Description, &s.Category, &s.ScoringMode, &s.IsActive, &s.Aliases)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("skill %q", slug))
	}
	return &s, nil
}

// Search resolves a query against names and aliases.
//
// An alias matches for LOOKUP and is never itself claimable — that is what
// stops the claiming population fragmenting across spellings. The caller gets
// the canonical skill plus how it was found, so a controller can tell a
// contributor "'golang' is an alias for 'go'; claim 'go' instead" rather than
// silently substituting.
//
// Exact matches first, then trigram similarity. Ordering by similarity alone
// would rank a close misspelling above the thing actually named.
func (r *SkillRepository) Search(ctx context.Context, query string) ([]port.SkillMatch, error) {
	rows, err := r.db.pool.Query(ctx, `
		WITH matches AS (
		    SELECT s.id,
		           CASE WHEN s.slug = $1 OR s.name ILIKE $1 THEN 'name' ELSE 'alias' END AS via,
		           CASE WHEN s.slug = $1 OR s.name ILIKE $1 THEN NULL ELSE a.alias END AS matched_alias,
		           CASE WHEN s.slug = $1 OR s.name ILIKE $1 OR a.alias = $1 THEN 1 ELSE 0 END AS exact,
		           greatest(similarity(s.name, $1), coalesce(similarity(a.alias::text, $1), 0)) AS score
		    FROM skills s
		    LEFT JOIN skill_aliases a ON a.skill_id = s.id
		    WHERE s.is_active
		      AND (s.slug = $1 OR s.name ILIKE $1 OR a.alias = $1
		           OR similarity(s.name, $1) > 0.3
		           OR similarity(a.alias::text, $1) > 0.3)
		)
		SELECT DISTINCT ON (s.id) `+skillColumns+`, m.via, coalesce(m.matched_alias::text, '')
		FROM matches m
		JOIN skills s ON s.id = m.id
		ORDER BY s.id, m.exact DESC, m.score DESC`, query)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("searching skills for %q", query))
	}
	defer rows.Close()

	var out []port.SkillMatch
	for rows.Next() {
		var m port.SkillMatch
		if err := rows.Scan(&m.Skill.ID, &m.Skill.Slug, &m.Skill.Name, &m.Skill.Description,
			&m.Skill.Category, &m.Skill.ScoringMode, &m.Skill.IsActive,
			&m.MatchedVia, &m.MatchedAlias); err != nil {
			return nil, translate(err, "scanning skill match")
		}
		out = append(out, m)
	}
	return out, translate(rows.Err(), "searching skills")
}

// Create adds a catalogue entry and its aliases.
//
// Takes a Tx because approving a skill request creates the skill and decides
// the request together: a skill with no request that produced it, or a request
// marked approved with no skill, are both states nothing could explain.
func (r *SkillRepository) Create(ctx context.Context, t port.Tx, s *domain.Skill) (*domain.Skill, error) {
	q := r.db.q(t)

	id := s.ID
	if id == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generating skill id: %w", err)
		}
		id = domain.SkillID(generated.String())
	}

	mode := s.ScoringMode
	if mode == "" {
		mode = domain.ScoringStandard
	}

	var created domain.Skill
	err := q.QueryRow(ctx, `
		INSERT INTO skills (id, slug, name, description, category, scoring_mode)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+skillColumnsBare,
		string(id), s.Slug, s.Name, s.Description, s.Category, string(mode),
	).Scan(&created.ID, &created.Slug, &created.Name, &created.Description,
		&created.Category, &created.ScoringMode, &created.IsActive)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("creating skill %q", s.Slug))
	}

	for _, alias := range s.Aliases {
		if _, err := q.Exec(ctx,
			`INSERT INTO skill_aliases (skill_id, alias) VALUES ($1, $2)`,
			string(created.ID), alias); err != nil {
			return nil, translate(err, fmt.Sprintf("adding alias %q", alias))
		}
	}
	created.Aliases = s.Aliases
	return &created, nil
}

// UserSkills reads a contributor's standings, best first.
func (r *SkillRepository) UserSkills(ctx context.Context, id domain.UserID) ([]domain.UserSkill, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT us.user_id, us.skill_id, s.slug, us.standing, us.distinct_pr_count,
		       coalesce(us.score, 0), coalesce(us.pr_component, 0), coalesce(us.project_component, 0),
		       us.promoted_at
		FROM user_skills us
		JOIN skills s ON s.id = us.skill_id
		WHERE us.user_id = $1
		ORDER BY us.score DESC NULLS LAST, s.slug`, string(id))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("reading skills for %s", id))
	}
	defer rows.Close()

	var out []domain.UserSkill
	for rows.Next() {
		var us domain.UserSkill
		if err := rows.Scan(&us.UserID, &us.SkillID, &us.Slug, &us.Standing, &us.DistinctPRCount,
			&us.Score, &us.PRComponent, &us.ProjectComponent, &us.PromotedAt); err != nil {
			return nil, translate(err, "scanning user skill")
		}
		out = append(out, us)
	}
	return out, translate(rows.Err(), "reading user skills")
}

// LinkPairs writes (user, PR, skill) rows as pending.
//
// This joins the SUBMITTING transaction, and that is the whole point: the
// primary key on (user_id, skill_id, repo_owner, repo_name, pr_number) is what
// stops the same PR evidencing the same skill twice, and it has to be enforced
// BEFORE a model call is spent rather than after (ADR-0007).
//
// A conflict is returned rather than ignored. The contributor is told which
// claim already owns the pair, which they cannot be if the insert silently
// does nothing.
func (r *SkillRepository) LinkPairs(ctx context.Context, t port.Tx, links []port.PRLink) error {
	if len(links) == 0 {
		return nil
	}
	q := r.db.q(t)
	for _, l := range links {
		if _, err := q.Exec(ctx, `
			INSERT INTO user_skill_pr_links
			    (user_id, skill_id, repo_owner, repo_name, pr_number, claim_id, status)
			VALUES ($1, $2, $3, $4, $5, $6, 'pending')`,
			string(l.UserID), string(l.SkillID), l.RepoOwner, l.RepoName, l.PRNumber, string(l.ClaimID)); err != nil {
			return translate(err, fmt.Sprintf("linking %s/%s#%d", l.RepoOwner, l.RepoName, l.PRNumber))
		}
	}
	return nil
}

// SetLinkStatus moves links to scored or rejected once a judgement lands.
func (r *SkillRepository) SetLinkStatus(ctx context.Context, t port.Tx, links []port.PRLink, status domain.PRLinkStatus) error {
	if len(links) == 0 {
		return nil
	}
	q := r.db.q(t)
	for _, l := range links {
		if _, err := q.Exec(ctx, `
			UPDATE user_skill_pr_links
			SET status = $6, resolved_at = now()
			WHERE user_id = $1 AND skill_id = $2 AND repo_owner = $3 AND repo_name = $4 AND pr_number = $5`,
			string(l.UserID), string(l.SkillID), l.RepoOwner, l.RepoName, l.PRNumber, string(status)); err != nil {
			return translate(err, fmt.Sprintf("setting link status for %s/%s#%d", l.RepoOwner, l.RepoName, l.PRNumber))
		}
	}
	return nil
}

// RecomputeStanding derives standing from the count of DISTINCT SCORED PRs.
//
// Standing is derived, never declared — which is why there is no SetStanding.
// Promotion at the fifth is automatic and needs no resubmission: the evidence
// already exists and has already been judged (ADR-0003).
//
// Only status='scored' counts. A rejected link is excluded, so a contributor
// with five PRs of which one was disqualified stays secondary — which is the
// rule ADR-0007 §3 exists to state.
//
// promoted_at is stamped on the transition and never cleared on demotion: it
// records that a promotion happened, and a later withdrawal does not unmake
// the fact.
func (r *SkillRepository) RecomputeStanding(ctx context.Context, t port.Tx, id domain.UserID, skillID domain.SkillID) (*domain.UserSkill, error) {
	var us domain.UserSkill
	err := r.db.q(t).QueryRow(ctx, `
		WITH scored AS (
		    SELECT count(*) AS n
		    FROM user_skill_pr_links
		    WHERE user_id = $1 AND skill_id = $2 AND status = 'scored'
		)
		INSERT INTO user_skills (user_id, skill_id, distinct_pr_count, standing, promoted_at, updated_at)
		SELECT $1, $2, scored.n,
		       CASE WHEN scored.n >= $3 THEN 'primary' ELSE 'secondary' END::skill_standing,
		       CASE WHEN scored.n >= $3 THEN now() END,
		       now()
		FROM scored
		ON CONFLICT (user_id, skill_id) DO UPDATE
		  SET distinct_pr_count = EXCLUDED.distinct_pr_count,
		      standing          = EXCLUDED.standing,
		      -- Kept once set. Demotion does not unmake the fact that a
		      -- promotion happened.
		      promoted_at       = coalesce(user_skills.promoted_at, EXCLUDED.promoted_at),
		      updated_at        = now()
		RETURNING user_id, skill_id, standing, distinct_pr_count,
		          coalesce(score, 0), coalesce(pr_component, 0), coalesce(project_component, 0), promoted_at`,
		string(id), string(skillID), PrimaryThreshold,
	).Scan(&us.UserID, &us.SkillID, &us.Standing, &us.DistinctPRCount,
		&us.Score, &us.PRComponent, &us.ProjectComponent, &us.PromotedAt)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("recomputing standing for %s/%s", id, skillID))
	}
	return &us, nil
}

// CreateRequest proposes a new catalogue entry.
//
// Deduped against names AND aliases before it reaches the queue: most noise is
// spelling variants and no admin should spend attention on them (ADR-0003).
// Returns port.ErrConflict when an existing skill already covers it.
func (r *SkillRepository) CreateRequest(ctx context.Context, userID domain.UserID, proposedName, rationale string) (domain.RequestID, error) {
	var existing string
	err := r.db.pool.QueryRow(ctx, `
		SELECT s.slug
		FROM skills s
		LEFT JOIN skill_aliases a ON a.skill_id = s.id
		WHERE s.name ILIKE $1 OR s.slug = $1 OR a.alias = $1
		   OR similarity(s.name, $1) > 0.6
		   OR similarity(a.alias::text, $1) > 0.6
		LIMIT 1`, proposedName).Scan(&existing)
	switch {
	case err == nil:
		return "", fmt.Errorf("%q is already covered by skill %q: %w", proposedName, existing, port.ErrConflict)
	case !errors.Is(err, pgx.ErrNoRows):
		return "", translate(err, "checking for an existing skill")
	}

	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generating request id: %w", err)
	}
	if _, err := r.db.pool.Exec(ctx, `
		INSERT INTO skill_requests (id, user_id, proposed_name, rationale)
		VALUES ($1, $2, $3, $4)`,
		id.String(), string(userID), proposedName, rationale); err != nil {
		return "", translate(err, "creating skill request")
	}
	return domain.RequestID(id.String()), nil
}

// PendingRequests drains the admin queue, oldest first — a queue, not a feed.
func (r *SkillRepository) PendingRequests(ctx context.Context, status string) ([]port.SkillRequest, error) {
	if status == "" {
		status = "pending"
	}
	rows, err := r.db.pool.Query(ctx, `
		SELECT id, user_id, proposed_name, rationale, status,
		       coalesce(decision_reason, ''), created_at, reviewed_at
		FROM skill_requests
		WHERE status = $1::skill_request_status
		ORDER BY created_at`, status)
	if err != nil {
		return nil, translate(err, "listing skill requests")
	}
	defer rows.Close()

	var out []port.SkillRequest
	for rows.Next() {
		var sr port.SkillRequest
		if err := rows.Scan(&sr.ID, &sr.UserID, &sr.ProposedName, &sr.Rationale,
			&sr.Status, &sr.Reason, &sr.CreatedAt, &sr.ReviewedAt); err != nil {
			return nil, translate(err, "scanning skill request")
		}
		out = append(out, sr)
	}
	return out, translate(rows.Err(), "listing skill requests")
}

// DecideRequest approves or rejects, in the transaction that created the skill.
//
// A decision is final. Re-deciding returns port.ErrConflict rather than
// overwriting — the catalogue is not edited through this queue.
func (r *SkillRepository) DecideRequest(ctx context.Context, t port.Tx, id domain.RequestID, by domain.AdminID, approve bool, reason string, created *domain.Skill) error {
	status := "rejected"
	var createdID *string
	if approve {
		status = "approved"
		if created == nil {
			return fmt.Errorf("approving %s: no skill was created", id)
		}
		s := string(created.ID)
		createdID = &s
	}

	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE skill_requests
		SET status = $2::skill_request_status, reviewed_by = $3, reviewed_at = now(),
		    decision_reason = NULLIF($4, ''), created_skill_id = $5, updated_at = now()
		WHERE id = $1 AND status = 'pending'`,
		string(id), status, string(by), reason, createdID)
	if err != nil {
		return translate(err, fmt.Sprintf("deciding skill request %s", id))
	}
	if tag.RowsAffected() == 0 {
		// Either it does not exist or it was already decided. Both are a
		// conflict from the caller's point of view: the queue no longer holds
		// this request.
		return fmt.Errorf("skill request %s is not pending: %w", id, port.ErrConflict)
	}
	return nil
}
