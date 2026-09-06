package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

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
func (r *SkillRepository) UserSkills(ctx context.Context, t port.Tx, id domain.UserID) ([]domain.UserSkill, error) {
	// rubric_version is not a column on user_skills: it belongs to the
	// EVALUATION that produced the score, and a sweep reaches skills one at a
	// time, so two of a contributor's skills can legitimately sit on different
	// versions. Reading it from the judgement keeps the two from drifting.
	rows, err := r.db.q(t).Query(ctx, `
		SELECT us.user_id, us.skill_id, s.slug, s.name, us.standing, us.distinct_pr_count,
		       coalesce(us.score, 0), coalesce(us.pr_component, 0), coalesce(us.project_component, 0),
		       us.promoted_at, coalesce(judged.rubric_version, '')
		FROM user_skills us
		JOIN skills s ON s.id = us.skill_id
		LEFT JOIN LATERAL (
		    SELECT e.rubric_version
		    FROM pr_skill_scores pss
		    JOIN evaluations e ON e.id = pss.evaluation_id
		    WHERE pss.skill_id = us.skill_id AND e.status = 'succeeded'
		      AND EXISTS (
		          SELECT 1 FROM claims c
		          WHERE c.id = e.claim_id AND c.user_id = us.user_id
		      )
		    ORDER BY e.completed_at DESC NULLS LAST
		    LIMIT 1
		) judged ON true
		WHERE us.user_id = $1
		ORDER BY us.score DESC NULLS LAST, s.slug`, string(id))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("reading skills for %s", id))
	}
	defer rows.Close()

	var out []domain.UserSkill
	for rows.Next() {
		var us domain.UserSkill
		if err := rows.Scan(&us.UserID, &us.SkillID, &us.Slug, &us.Name, &us.Standing, &us.DistinctPRCount,
			&us.Score, &us.PRComponent, &us.ProjectComponent, &us.PromotedAt, &us.RubricVersion); err != nil {
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
func (r *SkillRepository) SetLinkStatus(ctx context.Context, t port.Tx, links []port.PRLink, status domain.PRLinkStatus, reason *domain.RejectionReason) error {
	if len(links) == 0 {
		return nil
	}
	q := r.db.q(t)
	for _, l := range links {
		if _, err := q.Exec(ctx, `
			UPDATE user_skill_pr_links
			SET status = $6, rejection_reason = $7::skill_rejection_reason, resolved_at = now()
			WHERE user_id = $1 AND skill_id = $2 AND repo_owner = $3 AND repo_name = $4 AND pr_number = $5`,
			string(l.UserID), string(l.SkillID), l.RepoOwner, l.RepoName, l.PRNumber,
			string(status), nullableReason(reason)); err != nil {
			return translate(err, fmt.Sprintf("setting link status for %s/%s#%d", l.RepoOwner, l.RepoName, l.PRNumber))
		}
	}
	return nil
}

// nullableReason keeps an unrejected link's reason NULL rather than ”.
func nullableReason(r *domain.RejectionReason) *string {
	if r == nil {
		return nil
	}
	s := string(*r)
	return &s
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
	// No surviving evidence means no standing. The row is DELETED rather than
	// left at zero: a row saying "secondary, 0 PRs" asserts the contributor
	// holds the skill, which is exactly what withdrawing the last claim
	// stopped being true.
	var remaining int
	if err := r.db.q(t).QueryRow(ctx, `
		SELECT count(*) FROM user_skill_pr_links
		WHERE user_id = $1 AND skill_id = $2 AND status = 'scored'`,
		string(id), string(skillID)).Scan(&remaining); err != nil {
		return nil, translate(err, "counting surviving evidence")
	}
	if remaining == 0 {
		if _, err := r.db.q(t).Exec(ctx,
			`DELETE FROM user_skills WHERE user_id = $1 AND skill_id = $2`,
			string(id), string(skillID)); err != nil {
			return nil, translate(err, "dropping an unevidenced standing")
		}
		return nil, nil
	}

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
		string(id), string(skillID), domain.PrimaryThreshold,
	).Scan(&us.UserID, &us.SkillID, &us.Standing, &us.DistinctPRCount,
		&us.Score, &us.PRComponent, &us.ProjectComponent, &us.PromotedAt)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("recomputing standing for %s/%s", id, skillID))
	}
	return &us, nil
}

// LinkJudgedEvidence attaches the PRs already judged against one skill.
//
// Driven from pr_skill_scores rather than from the claim's evidence: a PR the
// model disqualified for this skill has no score row, and attaching it would
// credit the contributor with evidence that did not survive.
func (r *SkillRepository) LinkJudgedEvidence(ctx context.Context, t port.Tx, id domain.UserID,
	claimID domain.ClaimID, skillID domain.SkillID) error {

	if _, err := r.db.q(t).Exec(ctx, `
		INSERT INTO user_skill_pr_links
		    (user_id, skill_id, repo_owner, repo_name, pr_number, claim_id, status)
		SELECT $1, $2, s.repo_owner, s.repo_name, s.pr_number, $3, 'scored'
		FROM pr_skill_scores s
		JOIN evaluations e ON e.id = s.evaluation_id
		WHERE e.claim_id = $3 AND s.skill_id = $2
		ON CONFLICT (user_id, skill_id, repo_owner, repo_name, pr_number) DO NOTHING`,
		string(id), string(skillID), string(claimID)); err != nil {
		return translate(err, "attaching judged evidence")
	}
	return nil
}

// SetSkillScore writes a skill's score and its components.
func (r *SkillRepository) SetSkillScore(ctx context.Context, t port.Tx, id domain.UserID,
	skillID domain.SkillID, score, prComponent, projectComponent float64) error {

	// NULL, not zero, when nothing survived. ck_user_skill_score_range forbids
	// a stored zero precisely because it would claim a measurement: a skill
	// whose every PR was disqualified has not been scored low, it has not been
	// scored at all (ADR-0007).
	var stored *float64
	if score > 0 {
		stored = &score
	}

	if _, err := r.db.q(t).Exec(ctx, `
		UPDATE user_skills
		SET score = $3, pr_component = $4, project_component = $5, updated_at = now()
		WHERE user_id = $1 AND skill_id = $2`,
		string(id), string(skillID), stored, prComponent, projectComponent); err != nil {
		return translate(err, fmt.Sprintf("scoring skill %s for %s", skillID, id))
	}
	return nil
}

// UnlinkClaim drops every link a claim holds, in the withdrawing transaction.
func (r *SkillRepository) UnlinkClaim(ctx context.Context, t port.Tx, id domain.ClaimID) error {
	if _, err := r.db.q(t).Exec(ctx,
		`DELETE FROM user_skill_pr_links WHERE claim_id = $1`, string(id)); err != nil {
		return translate(err, fmt.Sprintf("releasing evidence for claim %s", id))
	}
	return nil
}

// ConflictingPairs reports which triples another claim already holds.
//
// Scoped to the SAME user: the uniqueness rule is per contributor, because two
// people may each evidence the same public PR for the same skill — what is
// forbidden is one person spending it twice (ADR-0007).
func (r *SkillRepository) ConflictingPairs(ctx context.Context, links []port.PRLink) ([]port.PairConflict, error) {
	var out []port.PairConflict

	for _, l := range links {
		var c port.PairConflict
		err := r.db.pool.QueryRow(ctx, `
			SELECT l.claim_id, s.slug, s.name
			FROM user_skill_pr_links l
			JOIN skills s ON s.id = l.skill_id
			WHERE l.user_id = $1 AND l.skill_id = $2
			  AND l.repo_owner = $3 AND l.repo_name = $4 AND l.pr_number = $5
			  AND l.claim_id <> $6
			LIMIT 1`,
			string(l.UserID), string(l.SkillID), l.RepoOwner, l.RepoName, l.PRNumber,
			string(l.ClaimID),
		).Scan(&c.ClaimID, &c.SkillSlug, &c.SkillName)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, translate(err, "checking for a conflicting evidence pair")
		}
		c.Link = l
		out = append(out, c)
	}
	return out, nil
}

// RequestByID reads one catalogue request whatever its state.
func (r *SkillRepository) RequestByID(ctx context.Context, id domain.RequestID) (*port.SkillRequest, error) {
	var s port.SkillRequest
	err := r.db.pool.QueryRow(ctx, `
		SELECT sr.id, sr.user_id, u.display_name, sr.proposed_name, sr.rationale,
		       sr.status, coalesce(sr.decision_reason, ''), sr.created_at, sr.reviewed_at
		FROM skill_requests sr
		JOIN users u ON u.id = sr.user_id
		WHERE sr.id = $1`, string(id),
	).Scan(&s.ID, &s.RequestedBy.ID, &s.RequestedBy.DisplayName, &s.ProposedName,
		&s.Rationale, &s.Status, &s.Reason, &s.CreatedAt, &s.ReviewedAt)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("skill request %s", id))
	}
	return &s, nil
}

// CollidesWith reports an exact clash between a proposed entry and the
// catalogue.
//
// Terms are checked in the order the admin supplied them — slug first, then
// aliases — so the reported collision is the first one they would fix.
func (r *SkillRepository) CollidesWith(ctx context.Context, slug string, aliases []string) (*port.SkillCollision, error) {
	terms := make([]string, 0, len(aliases)+1)
	if slug != "" {
		terms = append(terms, slug)
	}
	terms = append(terms, aliases...)

	for i, term := range terms {
		var (
			collision    port.SkillCollision
			matchedAlias bool
		)
		err := r.db.pool.QueryRow(ctx, `
			SELECT s.id, s.slug, s.name, coalesce(s.category, ''), a.alias IS NOT NULL
			FROM skills s
			LEFT JOIN skill_aliases a ON a.skill_id = s.id AND a.alias::text = $1
			WHERE s.slug = $1 OR a.alias::text = $1
			ORDER BY (s.slug = $1) DESC
			LIMIT 1`, term,
		).Scan(&collision.Skill.ID, &collision.Skill.Slug, &collision.Skill.Name,
			&collision.Skill.Category, &matchedAlias)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, translate(err, fmt.Sprintf("checking %q against the catalogue", term))
		}

		collision.Term = term
		collision.TermIsAlias = slug != "" && i > 0
		collision.MatchedAlias = matchedAlias
		return &collision, nil
	}
	return nil, nil
}

// MatchSkill finds the catalogue entry a proposed name duplicates.
//
// Matched against names AND aliases, because most noise is spelling variants
// and no admin should spend attention on them (ADR-0003). Whether a match
// BLOCKS a request is the service's decision; this only reports it.
func (r *SkillRepository) MatchSkill(ctx context.Context, proposedName string) (*domain.Skill, error) {
	var s domain.Skill

	// Compared on a NORMALISED form — lowercased, with everything that is not
	// a letter or digit removed — as well as raw.
	//
	// Trigram similarity alone misses the commonest duplicate there is: a name
	// written with a space. similarity('Go', 'Go Lang') is about 0.28, well
	// under any usable threshold, while normalising both to "golang" makes it
	// an exact hit against the existing alias.
	//
	// Exact matches are ordered ahead of fuzzy ones, so "PostgreSQL" resolves
	// to postgres rather than to whatever else happens to be 0.6 similar.
	err := r.db.pool.QueryRow(ctx, `
		WITH proposed AS (
			SELECT $1::text AS raw,
			       regexp_replace(lower($1::text), '[^a-z0-9]', '', 'g') AS norm
		)
		SELECT s.id, s.slug, s.name, s.category
		FROM skills s
		CROSS JOIN proposed p
		LEFT JOIN skill_aliases a ON a.skill_id = s.id
		WHERE s.name ILIKE p.raw
		   OR s.slug = p.norm
		   OR a.alias::text = p.norm
		   OR regexp_replace(lower(s.name), '[^a-z0-9]', '', 'g') = p.norm
		   OR regexp_replace(lower(a.alias::text), '[^a-z0-9]', '', 'g') = p.norm
		   OR similarity(s.name, p.raw) > 0.6
		   OR similarity(a.alias::text, p.raw) > 0.6
		ORDER BY (s.name ILIKE p.raw OR s.slug = p.norm OR a.alias::text = p.norm) DESC,
		         similarity(s.name, p.raw) DESC
		LIMIT 1`, proposedName,
	).Scan(&s.ID, &s.Slug, &s.Name, &s.Category)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("matching skill %q", proposedName))
	}
	return &s, nil
}

// RequestsSince reads a contributor's recent proposals, oldest first.
func (r *SkillRepository) RequestsSince(ctx context.Context, userID domain.UserID, since time.Time) ([]port.SkillRequest, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT id, user_id, proposed_name, rationale, status,
		       coalesce(decision_reason, ''), created_at, reviewed_at
		FROM skill_requests
		WHERE user_id = $1::uuid AND created_at >= $2
		ORDER BY created_at`, string(userID), since)
	if err != nil {
		return nil, translate(err, "listing recent skill requests")
	}
	defer rows.Close()

	var out []port.SkillRequest
	for rows.Next() {
		var s port.SkillRequest
		if err := rows.Scan(&s.ID, &s.RequestedBy.ID, &s.ProposedName, &s.Rationale,
			&s.Status, &s.Reason, &s.CreatedAt, &s.ReviewedAt); err != nil {
			return nil, translate(err, "scanning a skill request")
		}
		out = append(out, s)
	}
	return out, translate(rows.Err(), "listing recent skill requests")
}

// CreateRequest inserts one proposal. Policy lives in the service.
func (r *SkillRepository) CreateRequest(ctx context.Context, userID domain.UserID, proposedName, rationale string) (domain.RequestID, error) {
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
	// Joined, unlike the rate-limit read below: an admin deciding whether to
	// add a skill is weighing who is asking, and a uuid does not answer that.
	rows, err := r.db.pool.Query(ctx, `
		SELECT sr.id, sr.user_id, u.display_name, sr.proposed_name, sr.rationale,
		       sr.status, coalesce(sr.decision_reason, ''), sr.created_at, sr.reviewed_at
		FROM skill_requests sr
		JOIN users u ON u.id = sr.user_id
		WHERE sr.status = $1::skill_request_status
		ORDER BY sr.created_at`, status)
	if err != nil {
		return nil, translate(err, "listing skill requests")
	}
	defer rows.Close()

	var out []port.SkillRequest
	for rows.Next() {
		var sr port.SkillRequest
		if err := rows.Scan(&sr.ID, &sr.RequestedBy.ID, &sr.RequestedBy.DisplayName,
			&sr.ProposedName, &sr.Rationale, &sr.Status, &sr.Reason,
			&sr.CreatedAt, &sr.ReviewedAt); err != nil {
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
