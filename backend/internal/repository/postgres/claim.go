package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// ClaimRepository owns claims and their evidence.
//
// A claim is an evidence bundle: up to five merged PRs, up to twenty optional
// projects, and one or more skills those PRs demonstrate (ADR-0003).
type ClaimRepository struct{ db *DB }

// Claims returns the claim repository.
func (db *DB) Claims() *ClaimRepository { return &ClaimRepository{db: db} }

var _ port.ClaimRepository = (*ClaimRepository)(nil)

// ByID reads a claim with its evidence and skills.
func (r *ClaimRepository) ByID(ctx context.Context, id domain.ClaimID) (*domain.Claim, error) {
	return r.byID(ctx, r.db.pool, id)
}

// byID reads through a caller-supplied querier.
//
// Replace reads back through its own transaction: reading through the pool
// would return the state BEFORE the uncommitted writes, which is a silent
// wrong answer rather than an error.
func (r *ClaimRepository) byID(ctx context.Context, q querier, id domain.ClaimID) (*domain.Claim, error) {
	var c domain.Claim
	err := q.QueryRow(ctx, `
		SELECT id, user_id, status, version, submitted_at, evaluated_at, withdrawn_at,
		       locked_until, created_at, updated_at
		FROM claims WHERE id = $1`, string(id),
	).Scan(&c.ID, &c.UserID, &c.Status, &c.Version, &c.SubmittedAt, &c.EvaluatedAt,
		&c.WithdrawnAt, &c.LockedUntil, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("claim %s", id))
	}

	if c.PREvidence, err = r.prEvidence(ctx, q, id); err != nil {
		return nil, err
	}
	if !scoresApply(c.Status) {
		for i := range c.PREvidence {
			c.PREvidence[i].Scores = nil
		}
	}
	if c.Status == domain.ClaimDraft {
		// A draft carries no judgement. Reporting when it was last submitted
		// or scored would describe evidence it has since replaced.
		c.SubmittedAt, c.EvaluatedAt, c.LockedUntil = nil, nil, nil
	}
	if c.ProjectEvidence, err = r.projectEvidence(ctx, q, id); err != nil {
		return nil, err
	}
	if c.Skills, err = r.claimSkills(ctx, q, id); err != nil {
		return nil, err
	}
	return &c, nil
}

// ListByUser reads a contributor's claims, newest first.
//
// Summaries only — no evidence. A list that expanded every claim's evidence
// would issue three queries per row for information the caller is not showing.
func (r *ClaimRepository) ListByUser(ctx context.Context, id domain.UserID) ([]port.ClaimSummary, error) {
	// Counted in SQL. Loading the evidence to call len() on it would be two
	// extra round trips per claim to produce two integers.
	rows, err := r.db.pool.Query(ctx, `
		SELECT c.id, c.status, c.version,
		       coalesce(nominated.slug, ''),
		       (SELECT count(*) FROM claim_pr_evidence  e WHERE e.claim_id = c.id),
		       (SELECT count(*) FROM claim_skills       s WHERE s.claim_id = c.id),
		       c.submitted_at, c.evaluated_at, c.locked_until
		FROM claims c
		LEFT JOIN LATERAL (
		    SELECT s.slug
		    FROM claim_skills cs
		    JOIN skills s ON s.id = cs.skill_id
		    WHERE cs.claim_id = c.id AND cs.is_nominated_primary
		    LIMIT 1
		) nominated ON true
		WHERE c.user_id = $1
		ORDER BY c.created_at DESC`, string(id))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("listing claims for %s", id))
	}
	defer rows.Close()

	var out []port.ClaimSummary
	for rows.Next() {
		var c port.ClaimSummary
		if err := rows.Scan(&c.ID, &c.Status, &c.Version, &c.NominatedPrimary,
			&c.PRCount, &c.SkillCount, &c.SubmittedAt, &c.EvaluatedAt,
			&c.LockedUntil); err != nil {
			return nil, translate(err, "scanning claim")
		}
		out = append(out, c)
	}
	return out, translate(rows.Err(), "listing claims")
}

// Create opens an empty draft.
func (r *ClaimRepository) Create(ctx context.Context, userID domain.UserID) (*domain.Claim, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating claim id: %w", err)
	}

	var c domain.Claim
	err = r.db.pool.QueryRow(ctx, `
		INSERT INTO claims (id, user_id, status, version)
		VALUES ($1, $2, 'draft', 1)
		RETURNING id, user_id, status, version, created_at, updated_at`,
		id.String(), string(userID),
	).Scan(&c.ID, &c.UserID, &c.Status, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, translate(err, "creating claim")
	}
	return &c, nil
}

// Replace is the whole-claim write behind PUT /claims/{id}.
//
// The claims table has no editable scalar columns, so an edit is a REPLACE of
// evidence and skills together — the atomic form of the three POST
// sub-resources (ADR-0008 §6). Delete-then-insert rather than diffing: a diff
// would have to decide what "the same evidence row" means, and position is the
// only candidate key a contributor controls.
//
// Two refusals, both as errors rather than silent no-ops:
//
//   - locked_until in the future -> port.ErrConflict. The anti-reroll rule,
//     stated at the edit rather than at the submit.
//   - a stale version -> port.ErrVersionStale. Two tabs editing one draft must
//     not silently clobber each other.
func (r *ClaimRepository) Replace(ctx context.Context, t port.Tx, id domain.ClaimID, expectedVersion int, c *domain.Claim) (*domain.Claim, error) {
	return r.write(ctx, t, id, &expectedVersion, c)
}

// ReplaceEvidence writes a sub-resource without consuming the version.
//
// The version is the concurrency token for the WHOLE-CLAIM edit: it exists so
// two tabs doing PUT /claims/{id} cannot clobber each other. Setting evidence
// or skills is a different operation, and bumping there would make a
// contributor's next PUT fail with a stale version they never caused.
//
// It shares Replace's body rather than repeating it, so the lock check cannot
// be enforced in one path and forgotten in the other.
func (r *ClaimRepository) ReplaceEvidence(ctx context.Context, t port.Tx, id domain.ClaimID, c *domain.Claim) (*domain.Claim, error) {
	return r.write(ctx, t, id, nil, c)
}

// write is the one path that replaces a claim's contents.
//
// expectedVersion nil means "not a versioned edit": the version is neither
// checked nor bumped.
func (r *ClaimRepository) write(ctx context.Context, t port.Tx, id domain.ClaimID, expectedVersion *int, c *domain.Claim) (*domain.Claim, error) {
	q := r.db.q(t)

	// The lock is checked first and separately, so a locked claim reports
	// "locked" rather than the version conflict a bumped row would produce.
	var lockedUntil *time.Time
	var current int
	if err := q.QueryRow(ctx,
		`SELECT locked_until, version FROM claims WHERE id = $1 FOR UPDATE`, string(id)).
		Scan(&lockedUntil, &current); err != nil {
		return nil, translate(err, fmt.Sprintf("claim %s", id))
	}
	if lockedUntil != nil && lockedUntil.After(time.Now()) {
		return nil, fmt.Errorf("claim %s is locked until %s: %w",
			id, lockedUntil.Format(time.RFC3339), port.ErrConflict)
	}
	if expectedVersion != nil && current != *expectedVersion {
		return nil, fmt.Errorf("claim %s is at version %d, not %d: %w",
			id, current, *expectedVersion, port.ErrVersionStale)
	}

	// Editing an evaluated claim returns it to draft either way. The old scores
	// stand until a new submission replaces them (ADR-0003).
	bump := "version"
	if expectedVersion != nil {
		bump = "version + 1"
	}
	if _, err := q.Exec(ctx, `
		UPDATE claims
		SET version = `+bump+`, status = 'draft', updated_at = now()
		WHERE id = $1`, string(id)); err != nil {
		return nil, translate(err, "updating the claim")
	}

	for _, table := range []string{"claim_pr_evidence", "claim_project_evidence", "claim_skills"} {
		if _, err := q.Exec(ctx, `DELETE FROM `+table+` WHERE claim_id = $1`, string(id)); err != nil {
			return nil, translate(err, "clearing "+table)
		}
	}
	if err := r.insertEvidence(ctx, q, id, c); err != nil {
		return nil, err
	}
	return r.byID(ctx, q, id)
}

// Enrich caches the facts fetched from GitHub against each evidence row.
func (r *ClaimRepository) Enrich(ctx context.Context, t port.Tx, id domain.ClaimID, prs []domain.PREvidence) error {
	q := r.db.q(t)
	for _, pr := range prs {
		if pr.Facts == nil {
			continue
		}
		f := pr.Facts
		if _, err := q.Exec(ctx, `
			UPDATE claim_pr_evidence
			SET merged_at = $3, title = $4, author_github_user_id = $5,
			    additions = $6, deletions = $7, changed_files = $8,
			    review_comment_count = $9, review_count = $10, participant_count = $11,
			    enriched_at = now()
			WHERE claim_id = $1 AND position = $2`,
			string(id), pr.Position, f.MergedAt, f.Title, f.AuthorUserID,
			f.Additions, f.Deletions, f.ChangedFiles,
			f.ReviewComments, f.Reviews, f.Participants); err != nil {
			return translate(err, fmt.Sprintf("enriching position %d", pr.Position))
		}
	}
	return nil
}

// insertEvidence writes PR evidence, projects and skills for a claim.
func (r *ClaimRepository) insertEvidence(ctx context.Context, q querier, id domain.ClaimID, c *domain.Claim) error {
	for _, pr := range c.PREvidence {
		// A parsed row's canonical URL, or the raw text when it did not parse.
		// repo_owner/repo_name/pr_number are NOT NULL, so an unparseable row
		// stores empty values and is recognised by its invalid_reason.
		url := pr.RawURL
		if pr.RepoOwner != "" {
			url = fmt.Sprintf("https://github.com/%s/%s/pull/%d", pr.RepoOwner, pr.RepoName, pr.PRNumber)
		}
		role := pr.Role
		if role == "" {
			role = domain.RoleAuthor
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO claim_pr_evidence
			    (id, claim_id, pr_url, repo_owner, repo_name, pr_number, position, role, invalid_reason)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8)`,
			string(id), url, pr.RepoOwner, pr.RepoName, pr.PRNumber, pr.Position, string(role),
			pr.InvalidReason); err != nil {
			return translate(err, fmt.Sprintf("adding PR evidence at position %d", pr.Position))
		}
	}

	for _, p := range c.ProjectEvidence {
		url := fmt.Sprintf("https://github.com/%s/%s", p.RepoOwner, p.RepoName)

		// The evidence id is returned so a maintainer declaration can hang off
		// it in the same transaction. Declaring one against a project row that
		// did not commit would be a dangling claim to authority.
		var evidenceID string
		if err := q.QueryRow(ctx, `
			INSERT INTO claim_project_evidence
			    (id, claim_id, repo_url, repo_owner, repo_name, contribution_summary)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, NULLIF($5, ''))
			RETURNING id`,
			string(id), url, p.RepoOwner, p.RepoName, p.ContributionSummary,
		).Scan(&evidenceID); err != nil {
			return translate(err, fmt.Sprintf("adding project %s/%s", p.RepoOwner, p.RepoName))
		}

		if p.Maintainer == nil {
			continue
		}

		// Written WITHOUT a verdict. validated stays NULL until the model
		// rules, so a contributor cannot assert their way to the reach bonus
		// (ADR-0005).
		sources := make([]string, 0, len(p.Maintainer.Sources))
		for _, s := range p.Maintainer.Sources {
			sources = append(sources, string(s))
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO project_maintainer_declarations
			    (id, project_evidence_id, sources, other_evidence)
			VALUES (gen_random_uuid(), $1::uuid, $2::maintainer_source[], NULLIF($3, ''))`,
			evidenceID, sources, p.Maintainer.OtherEvidence); err != nil {
			return translate(err,
				fmt.Sprintf("declaring maintainer status on %s/%s", p.RepoOwner, p.RepoName))
		}
	}

	for _, s := range c.Skills {
		origin := s.Origin
		if origin == "" {
			origin = domain.UserDeclared
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO claim_skills (id, claim_id, skill_id, origin, is_nominated_primary)
			VALUES (gen_random_uuid(), $1,
			        (SELECT id FROM skills WHERE slug = $2), $3, $4)`,
			string(id), s.Slug, string(origin), s.IsNominatedPrimary); err != nil {
			return translate(err, fmt.Sprintf("adding skill %q", s.Slug))
		}
	}
	return nil
}

// SetStatus moves a claim through the pipeline.
func (r *ClaimRepository) SetStatus(ctx context.Context, t port.Tx, id domain.ClaimID, status domain.ClaimStatus) error {
	// $2 is cast once, explicitly. Used bare it is inferred as claim_status in
	// the SET and as text in the comparisons, and Postgres refuses to deduce
	// two types for one parameter.
	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE claims
		SET status = $2::claim_status,
		    submitted_at = CASE WHEN $2::claim_status = 'queued'    THEN now() ELSE submitted_at END,
		    withdrawn_at = CASE WHEN $2::claim_status = 'withdrawn' THEN now() ELSE withdrawn_at END,
		    updated_at = now()
		WHERE id = $1`, string(id), string(status))
	if err != nil {
		return translate(err, fmt.Sprintf("setting status of %s", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("claim %s: %w", id, port.ErrNotFound)
	}
	return nil
}

// SetEvaluated stamps the result and starts the seven-day lock.
func (r *ClaimRepository) SetEvaluated(ctx context.Context, t port.Tx, id domain.ClaimID, at time.Time, lockedUntil time.Time) error {
	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE claims
		SET status = 'evaluated', evaluated_at = $2, locked_until = $3, updated_at = now()
		WHERE id = $1`, string(id), at, lockedUntil)
	if err != nil {
		return translate(err, fmt.Sprintf("marking %s evaluated", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("claim %s: %w", id, port.ErrNotFound)
	}
	return nil
}

// DecideSuggestion accepts or dismisses an inert AI suggestion.
//
// A decision is final in both directions: the WHERE clause requires both
// stamps to be null, so re-deciding affects no row and reports a conflict
// rather than silently toggling.
//
// Only an ai_suggested row can be decided. A declared skill is not a
// suggestion and must not be reachable through this path.
func (r *ClaimRepository) DecideSuggestion(ctx context.Context, t port.Tx, id domain.ClaimID, skillID domain.SkillID, accept bool) (*domain.ClaimSkill, error) {
	column := "dismissed_at"
	if accept {
		column = "accepted_at"
	}

	var decided domain.ClaimSkill
	err := r.db.q(t).QueryRow(ctx, `
		UPDATE claim_skills cs
		SET `+column+` = $3
		FROM skills s
		WHERE cs.skill_id = s.id
		  AND cs.claim_id = $1 AND cs.skill_id = $2
		  AND cs.origin = 'ai_suggested'
		  AND cs.accepted_at IS NULL AND cs.dismissed_at IS NULL
		RETURNING cs.skill_id, s.slug, s.name, cs.origin, cs.accepted_at, cs.dismissed_at`,
		string(id), string(skillID), r.db.now()).
		Scan(&decided.SkillID, &decided.Slug, &decided.Name,
			&decided.Origin, &decided.AcceptedAt, &decided.DismissedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// No row updated has three different causes, and the caller owes the
		// contributor a different answer for each. The existing row is what
		// tells them apart, so it is returned ALONGSIDE the conflict.
		existing, lookupErr := r.claimSkill(ctx, t, id, skillID)
		if lookupErr != nil {
			return nil, lookupErr
		}
		return existing, fmt.Errorf("suggestion %s on claim %s is not undecided: %w",
			skillID, id, port.ErrConflict)
	}
	if err != nil {
		return nil, translate(err, "deciding suggestion")
	}
	return &decided, nil
}

// EvaluatedFingerprint reads the evidence the last judgement covered.
func (r *ClaimRepository) EvaluatedFingerprint(ctx context.Context, id domain.ClaimID) (string, error) {
	var fingerprint []byte
	err := r.db.pool.QueryRow(ctx, `
		SELECT evidence_fingerprint
		FROM evaluations
		WHERE claim_id = $1 AND status = 'succeeded'
		ORDER BY completed_at DESC NULLS LAST
		LIMIT 1`, string(id)).Scan(&fingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		// Never judged. Not an error: this is the ordinary case for a first
		// submission, and every submission is a first one once.
		return "", nil
	}
	if err != nil {
		return "", translate(err, fmt.Sprintf("reading the judged fingerprint for %s", id))
	}
	return string(fingerprint), nil
}

// claimSkill reads one declared or suggested skill, or reports that the claim
// never carried it.
func (r *ClaimRepository) claimSkill(ctx context.Context, t port.Tx, id domain.ClaimID, skillID domain.SkillID) (*domain.ClaimSkill, error) {
	var found domain.ClaimSkill
	err := r.db.q(t).QueryRow(ctx, `
		SELECT cs.skill_id, s.slug, s.name, cs.origin, cs.accepted_at, cs.dismissed_at
		FROM claim_skills cs JOIN skills s ON s.id = cs.skill_id
		WHERE cs.claim_id = $1 AND cs.skill_id = $2`, string(id), string(skillID)).
		Scan(&found.SkillID, &found.Slug, &found.Name,
			&found.Origin, &found.AcceptedAt, &found.DismissedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("skill %s on claim %s: %w", skillID, id, port.ErrNotFound)
	}
	if err != nil {
		return nil, translate(err, "reading a claim skill")
	}
	return &found, nil
}

// Fingerprint identifies a claim's evidence.
//
// Resubmitting unchanged evidence should be refused without spending a model
// call, and comparing fingerprints is how (ADR-0003). Sorted before hashing so
// reordering the same five PRs produces the same fingerprint — the evidence is
// a set, and position is presentation.
func (r *ClaimRepository) Fingerprint(ctx context.Context, id domain.ClaimID) (string, error) {
	return Fingerprint(ctx, r.db.pool, string(id))
}

// Fingerprint hashes a claim's evidence, given anything that can query.
//
// Exported and querier-taking so the integration harness seeds the same value
// the service would compute, rather than a stand-in that can silently diverge.
func Fingerprint(ctx context.Context, q Querier, id string) (string, error) {
	rows, err := q.Query(ctx, `
		SELECT repo_owner || '/' || repo_name || '#' || pr_number FROM claim_pr_evidence WHERE claim_id = $1
		UNION ALL
		SELECT 'project:' || repo_owner || '/' || repo_name FROM claim_project_evidence WHERE claim_id = $1
		UNION ALL
		SELECT 'skill:' || s.slug FROM claim_skills cs JOIN skills s ON s.id = cs.skill_id
		 WHERE cs.claim_id = $1 AND cs.origin = 'user_declared'`, id)
	if err != nil {
		return "", translate(err, fmt.Sprintf("fingerprinting claim %s", id))
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var part string
		if err := rows.Scan(&part); err != nil {
			return "", translate(err, "scanning fingerprint part")
		}
		parts = append(parts, part)
	}
	if err := rows.Err(); err != nil {
		return "", translate(err, "fingerprinting")
	}

	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

// PendingSweep lists claims to re-queue when the active rubric changes.
//
// Withdrawn claims are excluded: there is nothing to rank, so re-judging one
// would spend a model call to produce a score nobody can see.
func (r *ClaimRepository) PendingSweep(ctx context.Context, fromVersion string, limit int) ([]domain.ClaimID, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT DISTINCT c.id
		FROM claims c
		JOIN evaluations e ON e.claim_id = c.id
		WHERE c.status = 'evaluated'
		  AND e.rubric_version = $1
		ORDER BY c.id
		LIMIT $2`, fromVersion, limit)
	if err != nil {
		return nil, translate(err, "listing claims to sweep")
	}
	defer rows.Close()

	var out []domain.ClaimID
	for rows.Next() {
		var id domain.ClaimID
		if err := rows.Scan(&id); err != nil {
			return nil, translate(err, "scanning claim id")
		}
		out = append(out, id)
	}
	return out, translate(rows.Err(), "listing claims to sweep")
}

// --- evidence reads ----------------------------------------------------------

func (r *ClaimRepository) prEvidence(ctx context.Context, q querier, id domain.ClaimID) ([]domain.PREvidence, error) {
	rows, err := q.Query(ctx, `
		SELECT position, pr_url, repo_owner, repo_name, pr_number, role, invalid_reason,
		       merged_at, coalesce(title, ''), author_github_user_id,
		       coalesce(additions, 0), coalesce(deletions, 0), coalesce(changed_files, 0),
		       coalesce(review_comment_count, 0), coalesce(review_count, 0),
		       coalesce(participant_count, 0), enriched_at
		FROM claim_pr_evidence WHERE claim_id = $1 ORDER BY position`, string(id))
	if err != nil {
		return nil, translate(err, "reading PR evidence")
	}
	defer rows.Close()

	var out []domain.PREvidence
	for rows.Next() {
		var (
			pr         domain.PREvidence
			facts      domain.PRFacts
			authorID   *int64
			enrichedAt *time.Time
		)
		if err := rows.Scan(&pr.Position, &pr.RawURL, &pr.RepoOwner, &pr.RepoName, &pr.PRNumber, &pr.Role,
			&pr.InvalidReason, &facts.MergedAt, &facts.Title, &authorID,
			&facts.Additions, &facts.Deletions, &facts.ChangedFiles,
			&facts.ReviewComments, &facts.Reviews, &facts.Participants, &enrichedAt); err != nil {
			return nil, translate(err, "scanning PR evidence")
		}
		// Facts are attached only once enrichment has run. A zero-valued
		// PRFacts would read as "a PR with no activity", which is a different
		// claim from "not yet fetched".
		if enrichedAt != nil {
			if authorID != nil {
				facts.AuthorUserID = *authorID
			}
			facts.Merged = facts.MergedAt != nil
			pr.Facts = &facts
		}
		out = append(out, pr)
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err, "reading PR evidence")
	}
	if err := r.attachPRScores(ctx, q, id, out); err != nil {
		return nil, err
	}
	return out, nil
}

// scoresApply reports whether a claim's stored scores still describe its
// evidence.
//
// They do not once it has been edited back into a draft: the judgement was
// made against evidence that has since changed, and showing it beside the new
// evidence would attribute a number to a PR nobody scored.
func scoresApply(status domain.ClaimStatus) bool {
	return status == domain.ClaimEvaluated
}

// attachPRScores fills in what each PR was judged to be worth, per skill.
//
// Keyed on POSITION rather than on the repo triple: a contributor may evidence
// the same PR twice at different positions, and the score belongs to the
// position they put it in.
//
// No filter for zero scores is needed: ck_pr_skill_score_range enforces
// score > 0, because a zero-scoring skill is DROPPED rather than stored as a
// zero (ADR-0003). A row existing here means the model scored it.
func (r *ClaimRepository) attachPRScores(ctx context.Context, q querier, id domain.ClaimID, prs []domain.PREvidence) error {
	if len(prs) == 0 {
		return nil
	}

	rows, err := q.Query(ctx, `
		SELECT pss.position, s.slug, pss.score, pss.dimension_scores
		FROM pr_skill_scores pss
		JOIN evaluations e ON e.id = pss.evaluation_id
		JOIN skills s ON s.id = pss.skill_id
		WHERE e.claim_id = $1 AND e.status = 'succeeded'
		  -- An undecided suggestion's judgement is stored but not shown.
		  -- Reporting its score would leak the number the contributor has not
		  -- opted into, which is the whole point of inertness (ADR-0003 §10).
		  AND NOT EXISTS (
		      SELECT 1 FROM claim_skills cs
		      WHERE cs.claim_id = e.claim_id AND cs.skill_id = pss.skill_id
		        AND cs.origin = 'ai_suggested'
		        AND cs.accepted_at IS NULL AND cs.dismissed_at IS NULL
		  )
		ORDER BY pss.position, s.slug`, string(id))
	if err != nil {
		return translate(err, "reading PR scores")
	}
	defer rows.Close()

	byPosition := make(map[int][]domain.PRScore, len(prs))
	dimensions := make(map[int]map[string]domain.Dimension, len(prs))
	for rows.Next() {
		var (
			position int
			score    domain.PRScore
			raw      []byte
		)
		if err := rows.Scan(&position, &score.Skill, &score.Score, &raw); err != nil {
			return translate(err, "scanning PR score")
		}
		byPosition[position] = append(byPosition[position], score)

		// The dimensions of the FIRST skill scored on this PR. They describe
		// the pull request — substance, complexity, the conversation — not the
		// skill it was matched against, so every pair on one PR repeats them.
		if _, seen := dimensions[position]; !seen && len(raw) > 0 {
			var d map[string]domain.Dimension
			if err := json.Unmarshal(raw, &d); err == nil && len(d) > 0 {
				dimensions[position] = d
			}
		}
	}
	if err := rows.Err(); err != nil {
		return translate(err, "reading PR scores")
	}

	for i := range prs {
		prs[i].Scores = byPosition[prs[i].Position]
		prs[i].Dimensions = dimensions[prs[i].Position]
	}
	return r.attachPRRejections(ctx, q, id, prs)
}

// attachPRRejections marks the PRs that counted toward nothing.
//
// A rejected link is kept rather than deleted (ADR-0007), so the verdict is
// still readable — which is what lets a claim read say "this one did not
// count, and here is why" instead of silently omitting it.
func (r *ClaimRepository) attachPRRejections(ctx context.Context, q querier, id domain.ClaimID, prs []domain.PREvidence) error {
	rows, err := q.Query(ctx, `
		SELECT e.position, coalesce(l.rejection_reason::text, '')
		FROM claim_pr_evidence e
		JOIN user_skill_pr_links l
		  ON l.claim_id = e.claim_id AND l.repo_owner = e.repo_owner
		 AND l.repo_name = e.repo_name AND l.pr_number = e.pr_number
		WHERE e.claim_id = $1 AND l.status = 'rejected'`, string(id))
	if err != nil {
		return translate(err, "reading PR rejections")
	}
	defer rows.Close()

	rejected := map[int]string{}
	for rows.Next() {
		var (
			position int
			reason   string
		)
		if err := rows.Scan(&position, &reason); err != nil {
			return translate(err, "scanning PR rejection")
		}
		rejected[position] = reason
	}
	if err := rows.Err(); err != nil {
		return translate(err, "reading PR rejections")
	}

	for i := range prs {
		// Only when nothing survived: a PR that scored for one skill and was
		// rejected for another still counted.
		if reason, ok := rejected[prs[i].Position]; ok && len(prs[i].Scores) == 0 {
			prs[i].Rejected = true
			prs[i].RejectionReason = reason
		}
	}
	return nil
}

func (r *ClaimRepository) projectEvidence(ctx context.Context, q querier, id domain.ClaimID) ([]domain.ProjectEvidence, error) {
	// LEFT JOIN: most projects carry no declaration, and an inner join would
	// silently drop the ones that do not.
	rows, err := q.Query(ctx, `
		SELECT e.repo_owner, e.repo_name, coalesce(e.contribution_summary, ''),
		       d.sources, coalesce(d.other_evidence, ''),
		       d.validated, d.validated_at, coalesce(d.validation_notes, '')
		FROM claim_project_evidence e
		LEFT JOIN project_maintainer_declarations d ON d.project_evidence_id = e.id
		WHERE e.claim_id = $1
		ORDER BY e.repo_owner, e.repo_name`, string(id))
	if err != nil {
		return nil, translate(err, "reading project evidence")
	}
	defer rows.Close()

	var out []domain.ProjectEvidence
	for rows.Next() {
		var (
			p           domain.ProjectEvidence
			sources     []string
			other       string
			validated   *bool
			validatedAt *time.Time
			notes       string
		)
		if err := rows.Scan(&p.RepoOwner, &p.RepoName, &p.ContributionSummary,
			&sources, &other, &validated, &validatedAt, &notes); err != nil {
			return nil, translate(err, "scanning project evidence")
		}

		// sources is NULL when no declaration exists, which is how the two
		// cases are told apart — a declaration with an empty source list is
		// still a declaration.
		if sources != nil {
			declared := make([]domain.MaintainerSource, 0, len(sources))
			for _, s := range sources {
				declared = append(declared, domain.MaintainerSource(s))
			}
			p.Maintainer = &domain.MaintainerDeclaration{
				Sources: declared, OtherEvidence: other,
				Validated: validated, ValidatedAt: validatedAt, ValidationNotes: notes,
			}
		}
		out = append(out, p)
	}
	return out, translate(rows.Err(), "reading project evidence")
}

func (r *ClaimRepository) claimSkills(ctx context.Context, q querier, id domain.ClaimID) ([]domain.ClaimSkill, error) {
	// LEFT JOIN user_skills: standing is the contributor's position in the
	// skill overall, and a claim read reports what it moved. An inner join
	// would drop a skill that has been declared but not yet scored, which is
	// exactly the state a draft is in.
	rows, err := q.Query(ctx, `
		SELECT cs.skill_id, s.slug, s.name, s.scoring_mode, cs.origin, cs.is_nominated_primary,
		       cs.accepted_at, cs.dismissed_at, cs.rejected_at, coalesce(cs.rationale, ''),
		       us.score, coalesce(us.standing, 'secondary'),
		       coalesce(us.distinct_pr_count, 0)
		FROM claim_skills cs
		JOIN skills s ON s.id = cs.skill_id
		JOIN claims c ON c.id = cs.claim_id
		LEFT JOIN user_skills us ON us.skill_id = cs.skill_id AND us.user_id = c.user_id
		WHERE cs.claim_id = $1
		ORDER BY cs.is_nominated_primary DESC, s.slug`, string(id))
	if err != nil {
		return nil, translate(err, "reading claim skills")
	}
	defer rows.Close()

	var out []domain.ClaimSkill
	for rows.Next() {
		var cs domain.ClaimSkill
		if err := rows.Scan(&cs.SkillID, &cs.Slug, &cs.Name, &cs.ScoringMode, &cs.Origin, &cs.IsNominatedPrimary,
			&cs.AcceptedAt, &cs.DismissedAt, &cs.RejectedAt, &cs.Rationale,
			&cs.Score, &cs.Standing, &cs.DistinctPRCount); err != nil {
			return nil, translate(err, "scanning claim skill")
		}
		out = append(out, cs)
	}
	return out, translate(rows.Err(), "reading claim skills")
}
