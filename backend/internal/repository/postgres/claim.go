package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

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
func (r *ClaimRepository) ListByUser(ctx context.Context, id domain.UserID) ([]domain.Claim, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT id, user_id, status, version, submitted_at, evaluated_at, withdrawn_at,
		       locked_until, created_at, updated_at
		FROM claims WHERE user_id = $1
		ORDER BY created_at DESC`, string(id))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("listing claims for %s", id))
	}
	defer rows.Close()

	var out []domain.Claim
	for rows.Next() {
		var c domain.Claim
		if err := rows.Scan(&c.ID, &c.UserID, &c.Status, &c.Version, &c.SubmittedAt,
			&c.EvaluatedAt, &c.WithdrawnAt, &c.LockedUntil, &c.CreatedAt, &c.UpdatedAt); err != nil {
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
	if current != expectedVersion {
		return nil, fmt.Errorf("claim %s is at version %d, not %d: %w",
			id, current, expectedVersion, port.ErrVersionStale)
	}

	// Editing an evaluated claim returns it to draft. The old scores stand
	// until a new submission replaces them (ADR-0003).
	if _, err := q.Exec(ctx, `
		UPDATE claims
		SET version = version + 1, status = 'draft', updated_at = now()
		WHERE id = $1`, string(id)); err != nil {
		return nil, translate(err, "bumping claim version")
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

// insertEvidence writes PR evidence, projects and skills for a claim.
func (r *ClaimRepository) insertEvidence(ctx context.Context, q querier, id domain.ClaimID, c *domain.Claim) error {
	for _, pr := range c.PREvidence {
		url := fmt.Sprintf("https://github.com/%s/%s/pull/%d", pr.RepoOwner, pr.RepoName, pr.PRNumber)
		role := pr.Role
		if role == "" {
			role = domain.RoleAuthor
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO claim_pr_evidence
			    (id, claim_id, pr_url, repo_owner, repo_name, pr_number, position, role)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7)`,
			string(id), url, pr.RepoOwner, pr.RepoName, pr.PRNumber, pr.Position, string(role)); err != nil {
			return translate(err, fmt.Sprintf("adding PR evidence at position %d", pr.Position))
		}
	}

	for _, p := range c.ProjectEvidence {
		url := fmt.Sprintf("https://github.com/%s/%s", p.RepoOwner, p.RepoName)
		if _, err := q.Exec(ctx, `
			INSERT INTO claim_project_evidence
			    (id, claim_id, repo_url, repo_owner, repo_name, contribution_summary)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, NULLIF($5, ''))`,
			string(id), url, p.RepoOwner, p.RepoName, p.ContributionSummary); err != nil {
			return translate(err, fmt.Sprintf("adding project %s/%s", p.RepoOwner, p.RepoName))
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
func (r *ClaimRepository) DecideSuggestion(ctx context.Context, t port.Tx, id domain.ClaimID, skillID domain.SkillID, accept bool) error {
	column := "dismissed_at"
	if accept {
		column = "accepted_at"
	}
	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE claim_skills
		SET `+column+` = now()
		WHERE claim_id = $1 AND skill_id = $2
		  AND origin = 'ai_suggested'
		  AND accepted_at IS NULL AND dismissed_at IS NULL`,
		string(id), string(skillID))
	if err != nil {
		return translate(err, "deciding suggestion")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("suggestion %s on claim %s is not undecided: %w", skillID, id, port.ErrConflict)
	}
	return nil
}

// Fingerprint identifies a claim's evidence.
//
// Resubmitting unchanged evidence should be refused without spending a model
// call, and comparing fingerprints is how (ADR-0003). Sorted before hashing so
// reordering the same five PRs produces the same fingerprint — the evidence is
// a set, and position is presentation.
func (r *ClaimRepository) Fingerprint(ctx context.Context, id domain.ClaimID) (string, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT repo_owner || '/' || repo_name || '#' || pr_number FROM claim_pr_evidence WHERE claim_id = $1
		UNION ALL
		SELECT 'project:' || repo_owner || '/' || repo_name FROM claim_project_evidence WHERE claim_id = $1
		UNION ALL
		SELECT 'skill:' || s.slug FROM claim_skills cs JOIN skills s ON s.id = cs.skill_id
		 WHERE cs.claim_id = $1 AND cs.origin = 'user_declared'`, string(id))
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
		SELECT position, repo_owner, repo_name, pr_number, role, invalid_reason,
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
		if err := rows.Scan(&pr.Position, &pr.RepoOwner, &pr.RepoName, &pr.PRNumber, &pr.Role,
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
	return out, translate(rows.Err(), "reading PR evidence")
}

func (r *ClaimRepository) projectEvidence(ctx context.Context, q querier, id domain.ClaimID) ([]domain.ProjectEvidence, error) {
	rows, err := q.Query(ctx, `
		SELECT repo_owner, repo_name, coalesce(contribution_summary, '')
		FROM claim_project_evidence WHERE claim_id = $1 ORDER BY repo_owner, repo_name`, string(id))
	if err != nil {
		return nil, translate(err, "reading project evidence")
	}
	defer rows.Close()

	var out []domain.ProjectEvidence
	for rows.Next() {
		var p domain.ProjectEvidence
		if err := rows.Scan(&p.RepoOwner, &p.RepoName, &p.ContributionSummary); err != nil {
			return nil, translate(err, "scanning project evidence")
		}
		out = append(out, p)
	}
	return out, translate(rows.Err(), "reading project evidence")
}

func (r *ClaimRepository) claimSkills(ctx context.Context, q querier, id domain.ClaimID) ([]domain.ClaimSkill, error) {
	rows, err := q.Query(ctx, `
		SELECT cs.skill_id, s.slug, cs.origin, cs.is_nominated_primary,
		       cs.accepted_at, cs.dismissed_at, cs.rejected_at
		FROM claim_skills cs
		JOIN skills s ON s.id = cs.skill_id
		WHERE cs.claim_id = $1
		ORDER BY cs.is_nominated_primary DESC, s.slug`, string(id))
	if err != nil {
		return nil, translate(err, "reading claim skills")
	}
	defer rows.Close()

	var out []domain.ClaimSkill
	for rows.Next() {
		var cs domain.ClaimSkill
		if err := rows.Scan(&cs.SkillID, &cs.Slug, &cs.Origin, &cs.IsNominatedPrimary,
			&cs.AcceptedAt, &cs.DismissedAt, &cs.RejectedAt); err != nil {
			return nil, translate(err, "scanning claim skill")
		}
		out = append(out, cs)
	}
	return out, translate(rows.Err(), "reading claim skills")
}
